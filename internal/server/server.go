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
	// maxJSONBodyBytes caps POST bodies to prevent OOM from a hostile client.
	maxJSONBodyBytes = 64 << 10
	// wsWriteTimeout bounds how long broadcast() will wait on one client
	// before dropping it, so a paused tab cannot stall scroll-sync for
	// everyone behind it in the snapshot.
	wsWriteTimeout = 2 * time.Second
)

// state holds the server's mutable shared state. All access is guarded
// by mu; broadcast iterates over a snapshot of wsClients so a slow
// client cannot block others.
type state struct {
	mu            sync.Mutex
	file          string
	fileDir       string // directory of the originally-served file; /render path-overrides must stay inside it
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

// doRender re-renders the currently watched file, updates the cache and
// version counter, and returns the new version.
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

// broadcast sends msg as a single text frame to every connected client.
// A short write deadline is applied per client so one paused tab cannot
// stall the loop; clients whose write fails or times out are dropped.
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

// loopbackHosts is the set of hostnames we treat as same-machine.
var loopbackHosts = map[string]struct{}{
	"localhost": {}, "127.0.0.1": {}, "::1": {}, "[::1]": {},
}

// originAllowed enforces a loopback Host (defends against DNS rebinding —
// a malicious page that resolves evil.com to 127.0.0.1 can't get a browser
// to send our private API requests with Host: evil.com) and a loopback
// Origin when present (defends against cross-tab CSRF). Port is
// intentionally not checked so tests using httptest's random port still
// work; the loopback bind in serve() guarantees only local processes can
// reach us at all.
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

// guardOrigin rejects any request that doesn't originate from a loopback
// Host/Origin. Returns true if the request may proceed.
func guardOrigin(w http.ResponseWriter, r *http.Request) bool {
	if originAllowed(r) {
		return true
	}
	http.Error(w, "forbidden", http.StatusForbidden)
	return false
}

// newHandler builds the HTTP handler tree for a state.
func newHandler(s *state) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		handleIndex(s, w, r)
	})
	mux.HandleFunc("/reload", func(w http.ResponseWriter, r *http.Request) { handleReload(s, w, r) })
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) { handleWS(s, w, r) })
	mux.HandleFunc("/render", func(w http.ResponseWriter, r *http.Request) { handleRender(s, w, r) })
	mux.HandleFunc("/scroll", func(w http.ResponseWriter, r *http.Request) { handleScroll(s, w, r) })
	return mux
}

func handleIndex(s *state, w http.ResponseWriter, r *http.Request) {
	if !guardOrigin(w, r) {
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
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

func handleReload(s *state, w http.ResponseWriter, r *http.Request) {
	if !guardOrigin(w, r) {
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.mu.Lock()
	v := s.renderVersion
	s.mu.Unlock()
	writeJSON(w, map[string]any{"version": v})
}

// pathInsideDir returns true if cleanPath resolves to a path inside dir
// (or equals dir). Both arguments must be absolute and clean.
func pathInsideDir(cleanPath, dir string) bool {
	if cleanPath == dir {
		return true
	}
	sep := string(os.PathSeparator)
	if !strings.HasSuffix(dir, sep) {
		dir += sep
	}
	return strings.HasPrefix(cleanPath, dir)
}

func handleRender(s *state, w http.ResponseWriter, r *http.Request) {
	if !guardOrigin(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
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
	v := s.doRender()
	payload, _ := json.Marshal(map[string]any{"type": "reload", "version": v})
	s.broadcast(string(payload))
	writeJSON(w, map[string]any{"ok": true, "version": v})
}

func handleScroll(s *state, w http.ResponseWriter, r *http.Request) {
	if !guardOrigin(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	data, ok := readJSONBody(w, r)
	if !ok {
		return
	}
	line := jsonInt(data["line"])
	payload, _ := json.Marshal(map[string]any{"type": "scroll", "line": line})
	s.broadcast(string(payload))
	writeJSON(w, map[string]any{"ok": true, "line": line})
}

func handleWS(s *state, w http.ResponseWriter, r *http.Request) {
	if !guardOrigin(w, r) {
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
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

// readJSONBody decodes the request body into a string-keyed map. Empty or
// invalid bodies yield an empty map so a malformed POST still flows through
// the handler instead of erroring out — keys are looked up defensively.
// Bodies larger than maxJSONBodyBytes are rejected to bound memory.
// The bool return is false (and a 413 response is written) when the cap
// is exceeded; callers must not write further on false.
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

// jsonInt coerces a JSON-decoded value (typically float64) to int. Defaults
// to 0 for missing or non-numeric inputs.
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

// readStdin consumes JSON commands from stdin one per line. Blank or
// invalid lines are silently skipped. The quit callback is invoked on
// {"type":"quit"} and the loop returns immediately. The "render" file
// path is trusted (it comes from the local Neovim plugin over a private
// pipe, not over HTTP) so no path restriction applies here.
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
			v := s.doRender()
			payload, _ := json.Marshal(map[string]any{"type": "reload", "version": v})
			s.broadcast(string(payload))
		case "scroll":
			line := jsonInt(msg["line"])
			payload, _ := json.Marshal(map[string]any{"type": "scroll", "line": line})
			s.broadcast(string(payload))
		}
	}
}

// serve runs the HTTP server and stdin reader concurrently. It returns
// when the HTTP server stops (port-in-use error, ctx cancellation, etc.)
// or when stdin closes/quits via the quit callback.
//
// quit is invoked from the stdin reader on {"type":"quit"}; production
// wires this to os.Exit(0). Tests pass a no-op or signaling channel.
//
// Note: when stdin is os.Stdin (production) the scanner goroutine cannot
// be cancelled — it exits when the process exits. ctx-cancellation paths
// therefore leak this one goroutine; production calls os.Exit before that
// matters, and tests pass bounded readers that EOF naturally.
func serve(ctx context.Context, s *state, stdin io.Reader, quit func()) error {
	s.doRender()

	addr := fmt.Sprintf("127.0.0.1:%d", s.port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}

	srv := &http.Server{
		Handler:           newHandler(s),
		ReadHeaderTimeout: 10 * time.Second,
		ErrorLog:          log.New(io.Discard, "", 0),
	}

	fmt.Fprintf(os.Stdout, "[md-preview] Serving on http://localhost:%d/\n", s.port)

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

// Run starts the HTTP server on 127.0.0.1:port with the initial file and
// theme, reads JSON commands from stdin, broadcasts reload/scroll
// messages over WebSockets to connected browser clients, and blocks
// until stdin closes or the process is interrupted. Returns the first
// fatal error (e.g., port in use). On a {"type":"quit"} stdin command
// the process exits with status 0.
//
// colemak swaps the in-page nav keys to n/e/i (for h/n/e/i layout).
//
// The startup line "[md-preview] Serving on http://localhost:<port>/" is
// written to stdout so external tooling parsing it keeps working.
func Run(file string, port int, theme string, colemak bool) error {
	s := newState(file, port, theme, colemak)
	return serve(context.Background(), s, os.Stdin, func() { os.Exit(0) })
}
