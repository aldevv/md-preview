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
	scrollCoalesce   = 30 * time.Millisecond
)

type state struct {
	mu sync.Mutex
	// file, fileDir, fileDirResolved move together under mu; a stdin
	// "render" into a sibling tree retargets all three so /_img/
	// confinement tracks the active doc. fileDirResolved is the
	// EvalSymlinks of fileDir so served trees that themselves traverse
	// a symlink (macOS /var/folders, NixOS, encfs) accept their own
	// legitimate images.
	file            string
	fileDir         string
	fileDirResolved string
	htmlCache       string
	renderVersion   int
	theme           string
	port            int
	colemak         bool
	fileTree        bool
	fuzzyFinder     bool
	hop             bool
	visual          bool
	keys            map[string]string
	extraCSS        string
	eventLog        io.Writer
	wsClients       map[net.Conn]struct{}

	scrollMu      sync.Mutex
	scrollPending int
	scrollHas     bool
	scrollTimer   *time.Timer
}

func newState(file string, port int, theme string, colemak bool) *state {
	abs, err := filepath.Abs(file)
	if err != nil {
		abs = file
	}
	dir := filepath.Dir(abs)
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		resolved = dir
	}
	return &state{
		file:            abs,
		fileDir:         dir,
		fileDirResolved: resolved,
		port:            port,
		theme:           theme,
		colemak:         colemak,
		wsClients:       make(map[net.Conn]struct{}),
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

// The "file" field on the reload payload carries the current document
// path so the browser click handler keeps relative-href resolution in
// sync after a navigation.
func (s *state) renderAndBroadcast() int {
	v := s.doRender()
	s.mu.Lock()
	file := s.file
	s.mu.Unlock()
	payload, _ := json.Marshal(map[string]any{"type": "reload", "version": v, "file": file})
	s.broadcast(string(payload))
	return v
}

// broadcastScroll coalesces scroll bursts into one frame per
// scrollCoalesce window. nvim fires CursorMoved at ~60Hz; the older
// pending line is dropped when a newer one arrives mid-window.
func (s *state) broadcastScroll(line int) {
	s.scrollMu.Lock()
	s.scrollPending = line
	s.scrollHas = true
	if s.scrollTimer == nil {
		s.scrollTimer = time.AfterFunc(scrollCoalesce, s.flushScroll)
	}
	s.scrollMu.Unlock()
}

func (s *state) flushScroll() {
	s.scrollMu.Lock()
	line := s.scrollPending
	has := s.scrollHas
	s.scrollHas = false
	s.scrollTimer = nil
	s.scrollMu.Unlock()
	if !has {
		return
	}
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

// broadcast fans writes out per-client so a stalled tab only blocks its
// own goroutine for wsWriteTimeout. Returns synchronously so callers
// see ordering across successive frames (e.g. scroll then reload).
func (s *state) broadcast(msg string) {
	frame := wsEncode(msg)
	s.mu.Lock()
	clients := make([]net.Conn, 0, len(s.wsClients))
	for c := range s.wsClients {
		clients = append(clients, c)
	}
	s.mu.Unlock()

	if len(clients) == 0 {
		return
	}

	deadCh := make(chan net.Conn, len(clients))
	var wg sync.WaitGroup
	for _, c := range clients {
		wg.Add(1)
		go func(c net.Conn) {
			defer wg.Done()
			_ = c.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
			if _, err := c.Write(frame); err != nil {
				deadCh <- c
			}
		}(c)
	}
	wg.Wait()
	close(deadCh)

	var dead []net.Conn
	for c := range deadCh {
		dead = append(dead, c)
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

// originAllowed enforces a loopback Host (defends against DNS rebinding,
// a page on evil.com that resolves to 127.0.0.1 cannot send requests with
// Host: evil.com) and a loopback Origin when present (defends against
// cross-tab CSRF). Port is intentionally not checked so httptest's
// random port works; the loopback bind in serve() ensures only local
// processes can reach us.
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
	mux.HandleFunc("/tree", guard(http.MethodGet, s.handleTree))
	mux.HandleFunc("/_img/", guard(http.MethodGet, s.handleImg))
	return mux
}

func (s *state) handleIndex(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	body := s.htmlCache
	theme := s.theme
	port := s.port
	colemak := s.colemak
	fileTree := s.fileTree
	fuzzyFinder := s.fuzzyFinder
	hop := s.hop
	visual := s.visual
	keys := s.keys
	file := s.file
	fileDir := s.fileDir
	extraCSS := s.extraCSS
	s.mu.Unlock()

	// filepath.Dir(file) resolves relative <img src> against the current
	// document; fileDir scopes the /_img/ URL to the served root. They
	// differ after /render switches to a file under a subdir of fileDir.
	body = render.RewriteImgSrc(body, filepath.Dir(file), func(abs string) (string, bool) {
		return imgURLFor(abs, fileDir)
	})

	page := render.BuildPageWithKeys(body, theme, port, extraCSS, colemak, file, fileTree, fuzzyFinder, "", hop, visual, keys)
	encoded := []byte(page)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(encoded)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(encoded)
}

// imgURLFor returns ok=false for paths outside fileDir so the rewriter
// leaves the original src visible-broken in DevTools rather than
// silently masking it as a 403. Caller must snapshot fileDir under
// s.mu so a concurrent file switch can't desync the rewrite.
func imgURLFor(abs, fileDir string) (string, bool) {
	if !pathInsideDir(abs, fileDir) {
		return "", false
	}
	rel, err := filepath.Rel(fileDir, abs)
	if err != nil {
		return "", false
	}
	u := url.URL{Path: "/_img/" + filepath.ToSlash(rel)}
	return u.String(), true
}

// maxImgBytes caps a single /_img/ response so a 50 GB sibling pointed at
// by an <img src> can't be streamed in full.
const maxImgBytes = 100 << 20

func (s *state) handleImg(w http.ResponseWriter, r *http.Request) {
	rel := strings.TrimPrefix(r.URL.Path, "/_img/")
	if rel == "" {
		http.NotFound(w, r)
		return
	}
	s.mu.Lock()
	fileDir := s.fileDir
	fileDirResolved := s.fileDirResolved
	s.mu.Unlock()
	abs := filepath.Clean(filepath.Join(fileDir, filepath.FromSlash(rel)))
	if !pathInsideDir(abs, fileDir) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	// Re-check after EvalSymlinks: pathInsideDir is lexical, so a symlink
	// inside fileDir pointing at /etc/passwd would otherwise be served
	// by http.ServeFile. Compare against the resolved root so legitimate
	// images under symlinked served trees still pass.
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if !pathInsideDir(resolved, fileDirResolved) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	info, err := os.Stat(resolved)
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	if info.Size() > maxImgBytes {
		http.Error(w, "image too large", http.StatusRequestEntityTooLarge)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	http.ServeFile(w, r, resolved)
}

func (s *state) handleReload(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	v := s.renderVersion
	s.mu.Unlock()
	writeJSON(w, map[string]any{"version": v})
}

// handleTree lists every walkable file under fileDir for the Tab-toggle
// sidebar. Forward-slash relative paths so the client can build links
// without OS-specific path handling.
func (s *state) handleTree(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	root := s.fileDir
	s.mu.Unlock()
	files, err := render.WalkableFiles(root, 0)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "walking tree: "+err.Error())
		return
	}
	writeJSON(w, map[string]any{"root": root, "files": files})
}

// pathInsideDir reports whether cleanPath is dir or under dir. Both
// arguments must be absolute and lexically clean.
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
			writeError(w, http.StatusBadRequest, "bad path")
			return
		}
		s.mu.Lock()
		dir := s.fileDir
		dirResolved := s.fileDirResolved
		s.mu.Unlock()
		cleaned := filepath.Clean(abs)
		if !pathInsideDir(cleaned, dir) {
			writeError(w, http.StatusForbidden, "path outside served directory: "+fp)
			return
		}
		// Re-check after EvalSymlinks: a symlink inside fileDir pointing at
		// /etc/passwd passes the lexical check above; without this we'd
		// render it and broadcast the contents to every WS client.
		resolved, err := filepath.EvalSymlinks(cleaned)
		if err != nil {
			writeError(w, http.StatusNotFound, "file not found: "+fp)
			return
		}
		if !pathInsideDir(resolved, dirResolved) {
			writeError(w, http.StatusForbidden, "path outside served directory: "+fp)
			return
		}
		info, statErr := os.Stat(resolved)
		if statErr != nil || info.IsDir() {
			writeError(w, http.StatusNotFound, "file not found: "+fp)
			return
		}
		if !render.IsWalkableExt(resolved) {
			writeError(w, http.StatusUnsupportedMediaType, "unsupported format: "+filepath.Ext(resolved))
			return
		}
		s.mu.Lock()
		switched := s.file != resolved
		s.file = resolved
		s.mu.Unlock()
		if switched {
			s.emitNavigate(resolved)
		}
	}
	v := s.renderAndBroadcast()
	writeJSON(w, map[string]any{"ok": true, "version": v})
}

// emitNavigate writes the "[md-preview] navigate: <path>" line the nvim
// plugin parses to auto-`:edit` the new buffer. Defaults to os.Stdout
// when eventLog is nil so production keeps the stdout contract.
func (s *state) emitNavigate(path string) {
	w := s.eventLog
	if w == nil {
		w = os.Stdout
	}
	fmt.Fprintf(w, "[md-preview] navigate: %s\n", path)
}

// writeError emits the {"error": msg} shape the WS-client click handler
// parses for the toast UI.
func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
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

// readJSONBody returns ok=false only when the body exceeded the size cap
// (413 already written); callers must not write further on false.
// Empty/invalid bodies yield an empty map with ok=true.
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

// readStdin trusts the "render" file path because it comes from the
// local nvim plugin over a private pipe, not over HTTP; the /render
// path-confinement restriction intentionally does NOT apply here.
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
			var navigateTo string
			if fp, _ := msg["file"].(string); fp != "" {
				if abs, err := filepath.Abs(fp); err == nil {
					dir := filepath.Dir(abs)
					resolved, errR := filepath.EvalSymlinks(dir)
					if errR != nil {
						resolved = dir
					}
					s.mu.Lock()
					if s.file != abs {
						navigateTo = abs
					}
					s.file = abs
					s.fileDir = dir
					s.fileDirResolved = resolved
					s.mu.Unlock()
				}
			}
			if navigateTo != "" {
				s.emitNavigate(navigateTo)
			}
			s.renderAndBroadcast()
		case "scroll":
			s.broadcastScroll(jsonInt(msg["line"]))
		}
	}
}

// Options configures Run. Watch enables the mtime-polling file watcher.
// OnListen is invoked with the bound port once net.Listen returns,
// required when Port is 0 (kernel-assigned). EventLog defaults to
// os.Stdout when nil so the navigate-line stdout contract is preserved.
type Options struct {
	File        string
	Port        int
	Theme       string
	Colemak     bool
	FileTree    bool
	FuzzyFinder bool
	Hop         bool
	Visual      bool
	Keys        map[string]string
	Watch       bool
	ExtraCSS    string
	EventLog    io.Writer
	OnListen    func(port int)
}

// serve leaks the stdin scanner goroutine on ctx-cancel when stdin is
// os.Stdin: bufio.Scanner can't be cancelled, and the scanner unblocks
// only when the process exits. Production exits via os.Exit before this
// matters; tests pass bounded readers that EOF naturally.
func serve(ctx context.Context, s *state, stdin io.Reader, quit func(), watch bool, onListen func(int)) error {
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
	s.keys = opts.Keys
	s.extraCSS = opts.ExtraCSS
	s.eventLog = opts.EventLog
	return serve(context.Background(), s, os.Stdin, func() { os.Exit(0) }, opts.Watch, opts.OnListen)
}
