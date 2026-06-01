package main

import (
	"crypto/sha1"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/aldevv/md-preview/internal/config"
	"github.com/aldevv/md-preview/internal/osutil"
	"github.com/aldevv/md-preview/internal/server"
)

// sidecarOptions is the JSON payload the parent hands the sidecar via
// argv. Port is computed deterministically by the parent from the
// file path so the parent can inject the URL synchronously without
// waiting for the child to bind.
type sidecarOptions struct {
	File          string            `json:"file"`
	Theme         string            `json:"theme"`
	Port          int               `json:"port"`
	Colemak       bool              `json:"colemak"`
	FileTree      bool              `json:"file_tree"`
	FuzzyFinder   bool              `json:"fuzzy_finder"`
	Hop           bool              `json:"hop"`
	Visual        bool              `json:"visual"`
	Ask           bool              `json:"ask"`
	AskCommand    string            `json:"ask_command"`
	AskTimeoutSec int               `json:"ask_timeout_sec"`
	Keys          map[string]string `json:"keys"`
	ExtraCSS      string            `json:"extra_css"`
}

const (
	sidecarIdleTimeout = 30 * time.Minute
	sidecarPortBase    = 30000
	sidecarPortRange   = 25000
)

// sidecarPortFor returns the per-file deterministic port. Multiple
// previews of the same file collapse onto the same sidecar (first one
// wins the bind, the rest exit silently); different files get
// different ports with the usual hash-collision probability.
func sidecarPortFor(path string) int {
	sum := sha1.Sum([]byte(path))
	n := binary.BigEndian.Uint32(sum[:4])
	return sidecarPortBase + int(n%uint32(sidecarPortRange))
}

// runSidecar is the `mdp __sidecar <json>` entrypoint. The parent
// spawns it detached and fire-and-forget from the static-render path
// so it never blocks the terminal. We try to bind opts.Port; on
// conflict (another sidecar already owns the per-file port) we exit
// silently. Otherwise we serve /promote until idle timeout or until
// the watch server we start on promote exits.
func runSidecar(args []string, stderr io.Writer) int {
	if len(args) < 1 {
		fmt.Fprintln(stderr, "Usage: mdp __sidecar <json>")
		return 1
	}
	var opts sidecarOptions
	if err := json.Unmarshal([]byte(args[0]), &opts); err != nil {
		return 1
	}

	addr := fmt.Sprintf("127.0.0.1:%d", opts.Port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		// Almost always: port in use, i.e. another sidecar for the same
		// file is already listening. Silent exit is the desired UX.
		return 0
	}

	pr, pw, err := os.Pipe()
	if err != nil {
		_ = ln.Close()
		return 1
	}

	var (
		mu          sync.Mutex
		promotedURL string
		promotedCh  = make(chan struct{})
		watchDoneCh = make(chan error, 1)
		idleResetCh = make(chan struct{}, 1)
	)

	resetIdle := func() {
		select {
		case idleResetCh <- struct{}{}:
		default:
		}
	}

	doPromote := func() string {
		mu.Lock()
		if promotedURL != "" {
			url := promotedURL
			mu.Unlock()
			return url
		}
		mu.Unlock()

		listenCh := make(chan int, 1)
		sopts := server.Options{
			File:          opts.File,
			Port:          0,
			Theme:         opts.Theme,
			Colemak:       opts.Colemak,
			FileTree:      opts.FileTree,
			FuzzyFinder:   opts.FuzzyFinder,
			Hop:           opts.Hop,
			Visual:        opts.Visual,
			Ask:           opts.Ask,
			AskCommand:    opts.AskCommand,
			AskTimeoutSec: opts.AskTimeoutSec,
			Keys:          opts.Keys,
			ExtraCSS:      opts.ExtraCSS,
			Watch:         true,
			Stdin:         pr,
			OnListen:      func(p int) { listenCh <- p },
		}
		go func() { watchDoneCh <- server.Run(sopts) }()

		select {
		case p := <-listenCh:
			url := fmt.Sprintf("http://127.0.0.1:%d/", p)
			mu.Lock()
			promotedURL = url
			mu.Unlock()
			close(promotedCh)
			return url
		case err := <-watchDoneCh:
			fmt.Fprintf(stderr, "mdp __sidecar: watch server failed: %v\n", err)
			return ""
		case <-time.After(10 * time.Second):
			return ""
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/promote", func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		if host != "127.0.0.1" && host != "localhost" && host != "[::1]" && host != "::1" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		resetIdle()
		url := doPromote()
		if url == "" {
			http.Error(w, "promote failed", http.StatusInternalServerError)
			return
		}
		// Query param (not a fragment) so the watch page can see it
		// after the 302 — some Chromium/WebKit configurations strip
		// the URL fragment on cross-origin redirects. The page reads
		// ?open=ask to auto-open the input on first paint so the
		// user only has to click the AI icon once.
		http.Redirect(w, r, url+"?open=ask", http.StatusFound)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})

	httpSrv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() { _ = httpSrv.Serve(ln) }()

	idleTimer := time.NewTimer(sidecarIdleTimeout)
	for {
		select {
		case <-idleResetCh:
			if !idleTimer.Stop() {
				select {
				case <-idleTimer.C:
				default:
				}
			}
			idleTimer.Reset(sidecarIdleTimeout)
		case <-idleTimer.C:
			_ = httpSrv.Close()
			_ = pw.Close()
			return 0
		case <-promotedCh:
			err := <-watchDoneCh
			_ = httpSrv.Close()
			_ = pw.Close()
			if err != nil {
				return 1
			}
			return 0
		case err := <-watchDoneCh:
			_ = httpSrv.Close()
			_ = pw.Close()
			if err != nil {
				return 1
			}
			return 0
		}
	}
}

// startSidecar fires the detached sidecar child and returns the URL
// the page should navigate to on promote. The spawn is fire-and-
// forget: we never block the parent on it, so a slow exec, a
// missing executable, or a port conflict can't hang the terminal.
// When the child loses the bind race (another sidecar already owns
// the per-file port), it exits silently and the page still works
// because it points at the same URL.
func startSidecar(rc resolved, env Environment, stderr io.Writer) string {
	if env.Executable == nil {
		return ""
	}
	port := sidecarPortFor(rc.src)
	url := fmt.Sprintf("http://127.0.0.1:%d", port)
	opts := sidecarOptions{
		File:          rc.src,
		Theme:         rc.theme,
		Port:          port,
		Colemak:       rc.cfg.Colemak,
		FileTree:      rc.cfg.FileTree,
		FuzzyFinder:   rc.cfg.FuzzyFinder,
		Hop:           rc.cfg.Hop,
		Visual:        rc.cfg.Visual,
		Ask:           rc.cfg.Ask,
		AskCommand:    rc.cfg.AskCommand,
		AskTimeoutSec: rc.cfg.AskTimeoutSec,
		Keys:          rc.cfg.Keys,
		ExtraCSS:      config.ExtraCSS(rc.cfg, stderr),
	}
	payload, err := json.Marshal(opts)
	if err != nil {
		return ""
	}
	go func() {
		exe, err := env.Executable()
		if err != nil {
			return
		}
		cmd := exec.Command(exe, "__sidecar", string(payload))
		cmd.Stdin = nil
		cmd.Stdout = nil
		cmd.Stderr = nil
		cmd.SysProcAttr = osutil.DetachAttr()
		if err := cmd.Start(); err != nil {
			return
		}
		_ = cmd.Wait()
	}()
	return url
}
