package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"time"
)

// Options configures Run. Watch enables the mtime-polling file watcher.
// OnListen is invoked with the bound port once net.Listen returns,
// required when Port is 0 (kernel-assigned). EventLog defaults to
// os.Stdout when nil so the navigate-line stdout contract is preserved.
type Options struct {
	File            string
	Port            int
	Theme           string
	Colemak         bool
	FileTree        bool
	FuzzyFinder     bool
	Hop             bool
	Visual          bool
	Ask             bool
	AskCommand      string
	AskTimeoutSec   int
	AskSystemPrompt string
	AskCardWidth    int
	AskCardHeight   int
	Keys            map[string]string
	Watch           bool
	ExtraCSS        string
	EventLog        io.Writer
	OnListen        func(port int)
	// Stdin overrides the default os.Stdin source for the render/scroll/quit
	// JSON line protocol. Nil keeps the default. The sidecar uses this to
	// pass a never-EOF reader so server lifetime isn't tied to the parent
	// closing the config-passing pipe.
	Stdin io.Reader
	// ClientIdleTimeout, when > 0, exits the server (via quit) after the
	// last WS client has been disconnected for this duration. Only kicks
	// in once at least one client has connected; before then the server
	// stays up waiting (sidecar's wall-clock idle handles "never opened"
	// case). Used by the sidecar to clean up when the user closes the
	// preview window.
	ClientIdleTimeout time.Duration
}

// serve leaks the stdin scanner goroutine on ctx-cancel when stdin is
// os.Stdin: bufio.Scanner can't be cancelled, and the scanner unblocks
// only when the process exits. Production exits via os.Exit before this
// matters; tests pass bounded readers that EOF naturally.
// monitorClientIdle calls quit when the server has had at least one
// WS client connect and is now back to zero for the full timeout
// window. Polling once at ~timeout/4 keeps the check cheap and the
// detection latency bounded.
func monitorClientIdle(ctx context.Context, s *state, timeout time.Duration, quit func()) {
	interval := timeout / 4
	if interval < time.Second {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var firstSeen bool
	var idleSince time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.mu.Lock()
			n := len(s.wsClients)
			s.mu.Unlock()
			if n > 0 {
				firstSeen = true
				idleSince = time.Time{}
				continue
			}
			if !firstSeen {
				continue
			}
			if idleSince.IsZero() {
				idleSince = time.Now()
				continue
			}
			if time.Since(idleSince) >= timeout {
				quit()
				return
			}
		}
	}
}

func serve(ctx context.Context, s *state, stdin io.Reader, quit func(), watch bool, onListen func(int), clientIdleTimeout time.Duration) error {
	s.doRender()

	addr := fmt.Sprintf("127.0.0.1:%d", s.port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}

	// When Port==0 the kernel picks; surface it so the rendered page
	// embeds the actual WS port.
	actualPort := ln.Addr().(*net.TCPAddr).Port
	s.mu.Lock()
	s.port = actualPort
	s.mu.Unlock()
	if onListen != nil {
		onListen(actualPort)
	}

	srv := &http.Server{
		Handler:           newHandler(s),
		ReadHeaderTimeout: 10 * time.Second,
		ErrorLog:          log.New(io.Discard, "", 0),
	}

	fmt.Fprintf(os.Stdout, "[md-preview] Serving on http://localhost:%d/\n", actualPort)

	watchCtx, watchCancel := context.WithCancel(ctx)
	defer watchCancel()
	if watch {
		go watchFile(watchCtx, s)
	}
	if clientIdleTimeout > 0 {
		go monitorClientIdle(watchCtx, s, clientIdleTimeout, quit)
	}

	stdinDone := make(chan struct{})
	go func() {
		readStdin(s, stdin, quit)
		close(stdinDone)
	}()

	srvErr := make(chan error, 1)
	go func() {
		err := srv.Serve(ln)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		srvErr <- err
	}()

	select {
	case <-ctx.Done():
		_ = srv.Close()
		return <-srvErr
	case <-stdinDone:
		_ = srv.Close()
		return <-srvErr
	case err := <-srvErr:
		return err
	}
}

// Run blocks until stdin closes or {"type":"quit"} arrives (which calls
// os.Exit(0)). The "[md-preview] Serving on http://localhost:<port>/"
// startup line is parsed by external tooling and must stay on stdout.
func Run(opts Options) error {
	s := newState(opts.File, opts.Port, opts.Theme, opts.Colemak)
	s.fileTree = opts.FileTree
	s.fuzzyFinder = opts.FuzzyFinder
	s.hop = opts.Hop
	s.visual = opts.Visual
	s.ask = opts.Ask
	s.askCommand = opts.AskCommand
	s.askTimeout = time.Duration(opts.AskTimeoutSec) * time.Second
	if s.askTimeout <= 0 {
		s.askTimeout = defaultAskTimeout
	}
	if s.askTimeout > maxAskTimeout {
		s.askTimeout = maxAskTimeout
	}
	s.askSystemPrompt = opts.AskSystemPrompt
	s.askCardWidth = opts.AskCardWidth
	s.askCardHeight = opts.AskCardHeight
	s.keys = opts.Keys
	s.extraCSS = opts.ExtraCSS
	s.eventLog = opts.EventLog
	stdin := opts.Stdin
	if stdin == nil {
		stdin = os.Stdin
	}
	return serve(context.Background(), s, stdin, func() { os.Exit(0) }, opts.Watch, opts.OnListen, opts.ClientIdleTimeout)
}
