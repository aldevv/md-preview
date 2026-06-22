package server

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"path/filepath"
)

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
