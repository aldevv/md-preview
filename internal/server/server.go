package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aldevv/md-preview/internal/render"
)

const (
	maxJSONBodyBytes = 64 << 10
	wsWriteTimeout   = 2 * time.Second
)

type state struct {
	mu            sync.Mutex
	file          string
	fileDir       string // /render path-overrides must stay inside it
	htmlCache     string
	renderVersion int
	theme         string
	port          int
	colemak       bool
	wsClients     map[net.Conn]struct{}
}

func newState(file string, port int, theme string, colemak bool) *state {
	abs, err := filepath.Abs(file)
	if err != nil {
		abs = file
	}
	return &state{
		file:      abs,
		fileDir:   filepath.Dir(abs),
		port:      port,
		theme:     theme,
		colemak:   colemak,
		wsClients: make(map[net.Conn]struct{}),
	}
}

func (s *state) doRender() int {
	s.mu.Lock()
	fp := s.file
	s.mu.Unlock()

	body, _ := render.RenderBody(fp)

	s.mu.Lock()
	s.htmlCache = body
	s.renderVersion++
	v := s.renderVersion
	s.mu.Unlock()
	return v
}

// renderAndBroadcast re-renders and pushes a reload to every WS client.
// Used from both the HTTP /render handler and the stdin "render" command.
func (s *state) renderAndBroadcast() int {
	v := s.doRender()
	payload, _ := json.Marshal(map[string]any{"type": "reload", "version": v})
	s.broadcast(string(payload))
	return v
}

func (s *state) broadcastScroll(line int) {
	payload, _ := json.Marshal(map[string]any{"type": "scroll", "line": line})
	s.broadcast(string(payload))
}

func (s *state) addClient(c net.Conn) {
	s.mu.Lock()
	s.wsClients[c] = struct{}{}
	s.mu.Unlock()
}

func (s *state) removeClient(c net.Conn) {
	s.mu.Lock()
	delete(s.wsClients, c)
	s.mu.Unlock()
}

// broadcast applies a per-client write deadline so one paused tab cannot
// stall scroll-sync for everyone behind it; failed/timed-out clients are
// dropped from the registry.
func (s *state) broadcast(msg string) {
	frame := wsEncode(msg)
	s.mu.Lock()
	clients := make([]net.Conn, 0, len(s.wsClients))
	for c := range s.wsClients {
		clients = append(clients, c)
	}
	s.mu.Unlock()

	var dead []net.Conn
	for _, c := range clients {
		_ = c.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
		if _, err := c.Write(frame); err != nil {
			dead = append(dead, c)
		}
	}
	if len(dead) > 0 {
		s.mu.Lock()
		for _, c := range dead {
			delete(s.wsClients, c)
			_ = c.Close()
		}
		s.mu.Unlock()
	}
}

var loopbackHosts = map[string]struct{}{
	"localhost": {}, "127.0.0.1": {}, "::1": {}, "[::1]": {},
}

// originAllowed enforces a loopback Host (defends against DNS rebinding —
// a malicious page that resolves evil.com to 127.0.0.1 can't get a browser
// to send our private API requests with Host: evil.com) and a loopback
// Origin when present (defends against cross-tab CSRF). Port is intentionally
// not checked so tests on httptest's random port still pass; the loopback
// bind in serve() guarantees only local processes can reach us at all.
func originAllowed(r *http.Request) bool {
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	if _, ok := loopbackHosts[host]; !ok {
		return false
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		u, err := url.Parse(origin)
		if err != nil {
			return false
		}
		if _, ok := loopbackHosts[u.Hostname()]; !ok {
			return false
		}
	}
	return true
}

// guard wraps a handler with the loopback Origin/Host check and a method
// check. Replaces five copies of the same boilerplate at handler entry.
func guard(method string, fn http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !originAllowed(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if r.Method != method {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		fn(w, r)
	}
}

func newHandler(s *state) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", guard(http.MethodGet, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		s.handleIndex(w, r)
	}))
	mux.HandleFunc("/reload", guard(http.MethodGet, s.handleReload))
	mux.HandleFunc("/ws", guard(http.MethodGet, s.handleWS))
	mux.HandleFunc("/render", guard(http.MethodPost, s.handleRender))
	mux.HandleFunc("/scroll", guard(http.MethodPost, s.handleScroll))
	return mux
}

func (s *state) handleIndex(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	body := s.htmlCache
	theme := s.theme
	port := s.port
	colemak := s.colemak
	s.mu.Unlock()

	page := render.BuildPage(body, theme, port, "", colemak)
	encoded := []byte(page)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(encoded)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(encoded)
}

func (s *state) handleReload(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	v := s.renderVersion
	s.mu.Unlock()
	writeJSON(w, map[string]any{"version": v})
}

// pathInsideDir returns true if cleanPath resolves to a path inside dir
// (or equals dir). Both arguments must be absolute and clean.
func pathInsideDir(cleanPath, dir string) bool {
	rel, err := filepath.Rel(dir, cleanPath)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

func (s *state) handleRender(w http.ResponseWriter, r *http.Request) {
	data, ok := readJSONBody(w, r)
	if !ok {
		return
	}
	if fp, _ := data["file"].(string); fp != "" {
		abs, err := filepath.Abs(fp)
		if err != nil {
			http.Error(w, "bad path", http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		dir := s.fileDir
		s.mu.Unlock()
		if !pathInsideDir(filepath.Clean(abs), dir) {
			http.Error(w, "path outside served directory", http.StatusForbidden)
			return
		}
		s.mu.Lock()
		s.file = abs
		s.mu.Unlock()
	}
	v := s.renderAndBroadcast()
	writeJSON(w, map[string]any{"ok": true, "version": v})
}

func (s *state) handleScroll(w http.ResponseWriter, r *http.Request) {
	data, ok := readJSONBody(w, r)
	if !ok {
		return
	}
	line := jsonInt(data["line"])
	s.broadcastScroll(line)
	writeJSON(w, map[string]any{"ok": true, "line": line})
}

func (s *state) handleWS(w http.ResponseWriter, r *http.Request) {
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijacking not supported", http.StatusInternalServerError)
		return
	}
	key := r.Header.Get("Sec-WebSocket-Key")
	conn, brw, err := hj.Hijack()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	resp := "HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + wsAccept(key) + "\r\n\r\n"
	if _, err := brw.WriteString(resp); err != nil {
		_ = conn.Close()
		return
	}
	if err := brw.Flush(); err != nil {
		_ = conn.Close()
		return
	}

	s.addClient(conn)
	defer func() {
		s.removeClient(conn)
		_ = conn.Close()
	}()

	for {
		opcode, _ := wsReadFrame(brw.Reader)
		if opcode == 8 {
			return
		}
	}
}

// readJSONBody decodes the body into a string-keyed map. Empty/invalid
// bodies yield an empty map so a malformed POST still flows through.
// The bool return is false (and a 413 is written) when the cap is
// exceeded; callers must not write further on false.
func readJSONBody(w http.ResponseWriter, r *http.Request) (map[string]any, bool) {
	out := map[string]any{}
	if r.Body == nil {
		return out, true
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)
	defer r.Body.Close()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return out, false
		}
		return out, true
	}
	if len(body) == 0 {
		return out, true
	}
	_ = json.Unmarshal(body, &out)
	return out, true
}

func jsonInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	}
	return 0
}

func writeJSON(w http.ResponseWriter, data any) {
	encoded, _ := json.Marshal(data)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", strconv.Itoa(len(encoded)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(encoded)
}

// readStdin consumes JSON commands from stdin one per line. The "render"
// file path is trusted here (it comes from the local Neovim plugin over a
// private pipe, not over HTTP) so the same path restriction as /render
// does not apply.
func readStdin(s *state, stdin io.Reader, quit func()) {
	scanner := bufio.NewScanner(stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var msg map[string]any
		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}
		mtype, _ := msg["type"].(string)
		switch mtype {
		case "quit":
			quit()
			return
		case "render":
			if fp, _ := msg["file"].(string); fp != "" {
				if abs, err := filepath.Abs(fp); err == nil {
					s.mu.Lock()
					s.file = abs
					s.mu.Unlock()
				}
			}
			s.renderAndBroadcast()
		case "scroll":
			s.broadcastScroll(jsonInt(msg["line"]))
		}
	}
}

// Options configures Run. Watch enables the editor-agnostic file watcher
// (mtime polling). OnListen, if non-nil, is invoked with the actual bound
// port once net.Listen succeeds — useful when Port is 0 (ephemeral) and
// the caller needs the address to open a browser.
type Options struct {
	File     string
	Port     int
	Theme    string
	Colemak  bool
	Watch    bool
	OnListen func(port int)
}

// serve runs the HTTP server and stdin reader concurrently. It returns
// when the HTTP server stops or stdin closes/quits.
//
// When stdin is os.Stdin (production) the scanner goroutine cannot be
// cancelled — it exits when the process exits. ctx-cancellation paths
// therefore leak this one goroutine; production calls os.Exit before
// that matters, and tests pass bounded readers that EOF naturally.
func serve(ctx context.Context, s *state, stdin io.Reader, quit func(), watch bool, onListen func(int)) error {
	s.doRender()

	addr := fmt.Sprintf("127.0.0.1:%d", s.port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}

	// Pick up the ephemeral port assigned by the kernel when Port==0 so
	// the rendered page embeds the correct WS port.
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

// Run starts the server, reads JSON commands from stdin, and blocks until
// stdin closes or the process is interrupted. On {"type":"quit"} the
// process exits with status 0.
//
// The startup line "[md-preview] Serving on http://localhost:<port>/" is
// written to stdout so external tooling parsing it keeps working.
func Run(opts Options) error {
	s := newState(opts.File, opts.Port, opts.Theme, opts.Colemak)
	return serve(context.Background(), s, os.Stdin, func() { os.Exit(0) }, opts.Watch, opts.OnListen)
}
