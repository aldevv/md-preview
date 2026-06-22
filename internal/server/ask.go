package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/aldevv/md-preview/internal/render"
)

const (
	maxAskOutputBytes = 256 << 10
	defaultAskTimeout = 60 * time.Second
	maxAskTimeout     = 600 * time.Second
)

func buildAskBody(systemPrompt, selection, prompt, priorPrompt, priorResponse string) string {
	var b strings.Builder
	if systemPrompt != "" {
		b.WriteString(systemPrompt)
		b.WriteString("\n\n")
	}
	b.WriteString("The user has selected the following text in a markdown document. Answer their question about it concisely. Output plain markdown.\n\n")
	b.WriteString("--- Selection start ---\n")
	b.WriteString(selection)
	b.WriteString("\n--- Selection end ---\n\n")
	if priorPrompt != "" {
		b.WriteString("They previously asked: ")
		b.WriteString(priorPrompt)
		b.WriteString("\n")
		if priorResponse != "" {
			b.WriteString("You answered:\n")
			b.WriteString(priorResponse)
			b.WriteString("\n")
		}
		b.WriteString("This is a follow-up to that exchange.\n\n")
	}
	b.WriteString("Question: ")
	b.WriteString(prompt)
	b.WriteString("\n")
	return b.String()
}

func (s *state) handleAsk(w http.ResponseWriter, r *http.Request) {
	data, ok := readJSONBody(w, r)
	if !ok {
		return
	}
	selection, _ := data["selection"].(string)
	prompt, _ := data["prompt"].(string)
	priorPrompt, _ := data["prior_prompt"].(string)
	priorResponse, _ := data["prior_response"].(string)
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		writeError(w, http.StatusBadRequest, "prompt is required")
		return
	}

	s.mu.Lock()
	cmdStr := s.askCommand
	timeout := s.askTimeout
	systemPrompt := s.askSystemPrompt
	runAsk := s.runAsk
	s.mu.Unlock()
	if cmdStr == "" {
		cmdStr = "claude -p"
	}
	if timeout <= 0 {
		timeout = defaultAskTimeout
	}
	argv := strings.Fields(cmdStr)
	if len(argv) == 0 {
		writeError(w, http.StatusInternalServerError, "ask_command is empty")
		return
	}
	if runAsk == nil {
		runAsk = realRunAsk
	}

	body := buildAskBody(systemPrompt, selection, prompt, priorPrompt, priorResponse)

	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	stdout, stderr, err := runAsk(ctx, argv, []byte(body))
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			writeError(w, http.StatusGatewayTimeout, fmt.Sprintf("ask timed out after %s", timeout))
			return
		}
		var pathErr *exec.Error
		if errors.As(err, &pathErr) {
			writeError(w, http.StatusServiceUnavailable, "ask command not found: "+pathErr.Name)
			return
		}
		snippet := strings.TrimSpace(string(stderr))
		if len(snippet) > 500 {
			snippet = snippet[:500]
		}
		if snippet == "" {
			snippet = err.Error()
		}
		writeError(w, http.StatusBadGateway, "ask command failed: "+snippet)
		return
	}
	raw := string(stdout)
	html := render.RenderBytes(stdout)
	s.mu.Lock()
	curFile := s.file
	s.mu.Unlock()
	appendHistoryAsync(curFile, prompt, html)
	writeJSON(w, map[string]any{"ok": true, "html": html, "raw": raw})
}

// handleAskHistory returns the per-file ask history newest-first.
// File is the server's current document (s.file), not a client-
// supplied path, so a tab from a stale render can't read another
// file's history. Loaded lazily from disk on each call so startup
// isn't paying for it.
func (s *state) handleAskHistory(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	file := s.file
	s.mu.Unlock()
	entries, err := readHistoryFor(file, 50)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "history read failed")
		return
	}
	if entries == nil {
		entries = []historyEntry{}
	}
	writeJSON(w, map[string]any{"entries": entries})
}

// realRunAsk spawns argv with the prompt on stdin, returning stdout +
// stderr capped at maxAskOutputBytes each so a runaway subprocess
// can't OOM the server.
func realRunAsk(ctx context.Context, argv []string, stdin []byte) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdin = bytes.NewReader(stdin)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &cappedWriter{buf: &outBuf, cap: maxAskOutputBytes}
	cmd.Stderr = &cappedWriter{buf: &errBuf, cap: maxAskOutputBytes}
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return outBuf.Bytes(), errBuf.Bytes(), context.DeadlineExceeded
	}
	return outBuf.Bytes(), errBuf.Bytes(), err
}

type cappedWriter struct {
	buf *bytes.Buffer
	cap int
}

func (c *cappedWriter) Write(p []byte) (int, error) {
	rem := c.cap - c.buf.Len()
	if rem <= 0 {
		return len(p), nil
	}
	if len(p) > rem {
		c.buf.Write(p[:rem])
		return len(p), nil
	}
	c.buf.Write(p)
	return len(p), nil
}
