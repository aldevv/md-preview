package server

import (
	"context"
	"os"
	"time"
)

// watchPollInterval is the polling cadence for the editor-agnostic file
// watcher. 250ms is below the perception threshold for "instant" refresh
// and keeps CPU cost negligible (one stat() per tick).
const watchPollInterval = 250 * time.Millisecond

// watchFile polls the currently-served file's mtime+size and triggers a
// renderAndBroadcast whenever either changes. Returns when ctx is cancelled.
//
// Polling is preferred over fsnotify here: no extra dependency, and it
// transparently survives editor save patterns that confuse inotify-style
// watchers (write-rename atomic save, file truncation, brief unlink+recreate
// during `:w`). The cost is a 0–250ms latency between save and reload, which
// is imperceptible for markdown preview.
//
// s.file is re-read each tick so a stdin "render" file-switch (from the
// nvim plugin) takes effect immediately.
func watchFile(ctx context.Context, s *state) {
	var lastMtime time.Time
	var lastSize int64

	s.mu.Lock()
	fp := s.file
	s.mu.Unlock()
	if info, err := os.Stat(fp); err == nil {
		lastMtime = info.ModTime()
		lastSize = info.Size()
	}

	ticker := time.NewTicker(watchPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.mu.Lock()
			fp := s.file
			s.mu.Unlock()
			info, err := os.Stat(fp)
			if err != nil {
				// File temporarily gone (atomic save races); skip this tick.
				continue
			}
			mt, sz := info.ModTime(), info.Size()
			if mt.Equal(lastMtime) && sz == lastSize {
				continue
			}
			lastMtime = mt
			lastSize = sz
			s.renderAndBroadcast()
		}
	}
}
