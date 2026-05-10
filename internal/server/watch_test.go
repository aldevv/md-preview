package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// waitForVersion polls s.renderVersion until it reaches want or the deadline
// expires. Returns the final version seen. Polling beats a fixed sleep — it
// keeps the test fast on a quick watcher and tolerant on a slow CI box.
func waitForVersion(s *state, want int, deadline time.Duration) int {
	end := time.Now().Add(deadline)
	for time.Now().Before(end) {
		s.mu.Lock()
		v := s.renderVersion
		s.mu.Unlock()
		if v >= want {
			return v
		}
		time.Sleep(10 * time.Millisecond)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.renderVersion
}

func TestWatchFile_BumpsVersionOnChange(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "doc.md")
	if err := os.WriteFile(file, []byte("# v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := newState(file, 0, "dark", false)
	s.doRender() // version → 1

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go watchFile(ctx, s)

	// Ensure the watcher captures a baseline mtime before we modify the
	// file — otherwise on a very fast box the change could land in the
	// same tick as baseline-capture and be elided.
	time.Sleep(50 * time.Millisecond)

	// Bump mtime explicitly: same-size + sub-second-resolution filesystems
	// (older ext4 mounted noatime, etc.) can land back-to-back writes with
	// an identical ModTime. Set it forward so the watcher reliably sees a
	// change even on coarse-resolution filesystems.
	if err := os.WriteFile(file, []byte("# v2 longer\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(2 * time.Second)
	_ = os.Chtimes(file, future, future)

	got := waitForVersion(s, 2, 2*time.Second)
	if got < 2 {
		t.Errorf("renderVersion = %d after file change, want >= 2", got)
	}
}

func TestWatchFile_NoChangeNoBump(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "doc.md")
	if err := os.WriteFile(file, []byte("# stable\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := newState(file, 0, "dark", false)
	s.doRender()
	startVersion := s.renderVersion

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go watchFile(ctx, s)

	// Wait long enough for several poll ticks (4 × 250ms) so we'd notice
	// any spurious renders.
	time.Sleep(watchPollInterval*4 + 50*time.Millisecond)

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.renderVersion != startVersion {
		t.Errorf("renderVersion = %d, want %d (no file change)", s.renderVersion, startVersion)
	}
}

func TestWatchFile_StopsOnContextCancel(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "doc.md")
	if err := os.WriteFile(file, []byte("# hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := newState(file, 0, "dark", false)
	s.doRender()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		watchFile(ctx, s)
		close(done)
	}()
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("watchFile did not return after context cancel")
	}
}
