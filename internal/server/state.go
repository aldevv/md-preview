package server

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"path/filepath"
	"sync"
	"time"

	"github.com/aldevv/md-preview/internal/render"
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
	ask             bool
	askCommand      string
	askTimeout      time.Duration
	askSystemPrompt string
	askCardWidth    int
	askCardHeight   int
	// runAsk is the seam tests substitute. Production wires a real
	// exec.CommandContext via realRunAsk. It returns the stdout,
	// stderr, and any exec/timeout error.
	runAsk    func(ctx context.Context, argv []string, stdin []byte) (stdout, stderr []byte, err error)
	keys      map[string]string
	extraCSS  string
	eventLog  io.Writer
	wsClients map[net.Conn]struct{}

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
