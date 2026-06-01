package server

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// historyMaxLines caps the JSONL file; new entries beyond the cap
// rotate the oldest ones out. Plenty for the ad-hoc "show me what I
// asked before" workflow without growing the file unbounded.
const historyMaxLines = 1000

// historyMaxLineLen guards readHistory against a corrupted file
// producing arbitrarily long lines.
const historyMaxLineLen = 1 << 20

type historyEntry struct {
	File   string `json:"file"`
	Prompt string `json:"prompt"`
	HTML   string `json:"html"`
	TS     int64  `json:"ts"`
}

var historyMu sync.Mutex

// historyPath honors XDG_DATA_HOME, falling back to ~/.local/share.
// Returns "" when the home dir can't be resolved (best-effort: caller
// silently skips the write/read).
func historyPath() string {
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, "mdp", "history.jsonl")
}

// appendHistoryAsync writes a single JSON line in a goroutine so it
// never blocks the /ask response. Errors are swallowed: history is
// best-effort.
func appendHistoryAsync(file, prompt, html string) {
	go func() {
		path := historyPath()
		if path == "" {
			return
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return
		}
		entry := historyEntry{File: file, Prompt: prompt, HTML: html, TS: time.Now().Unix()}
		line, err := json.Marshal(entry)
		if err != nil {
			return
		}
		historyMu.Lock()
		defer historyMu.Unlock()
		f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return
		}
		defer f.Close()
		_, _ = f.Write(line)
		_, _ = f.Write([]byte("\n"))
		maybeRotateHistory(path)
	}()
}

// maybeRotateHistory keeps the file under historyMaxLines by
// rewriting it with the tail. Called under historyMu.
func maybeRotateHistory(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), historyMaxLineLen)
	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	_ = f.Close()
	if len(lines) <= historyMaxLines {
		return
	}
	keep := lines[len(lines)-historyMaxLines:]
	tmp := path + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return
	}
	w := bufio.NewWriter(out)
	for _, l := range keep {
		_, _ = w.WriteString(l)
		_ = w.WriteByte('\n')
	}
	if err := w.Flush(); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return
	}
	_ = os.Rename(tmp, path)
}

// readHistoryFor returns up to limit entries newest-first that match
// the given absolute file path. Missing file is not an error; a
// corrupted line is skipped. limit <= 0 means "all matching".
func readHistoryFor(file string, limit int) ([]historyEntry, error) {
	path := historyPath()
	if path == "" {
		return nil, nil
	}
	historyMu.Lock()
	defer historyMu.Unlock()
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), historyMaxLineLen)
	var matches []historyEntry
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var e historyEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue
		}
		if e.File != file {
			continue
		}
		matches = append(matches, e)
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	for i, j := 0, len(matches)-1; i < j; i, j = i+1, j-1 {
		matches[i], matches[j] = matches[j], matches[i]
	}
	if limit > 0 && len(matches) > limit {
		matches = matches[:limit]
	}
	return matches, nil
}
