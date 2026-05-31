package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func writeMD(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	return p
}

func newTestState(t *testing.T, file string) *state {
	t.Helper()
	s := newState(file, 0, "dark", false)
	s.eventLog = io.Discard
	s.doRender()
	return s
}

func TestHandler_GetHTML_ReturnsPage(t *testing.T) {
	dir := t.TempDir()
	file := writeMD(t, dir, "doc.md", "# Hello\n")
	s := newTestState(t, file)
	srv := httptest.NewServer(newHandler(s))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q, want text/html; charset=utf-8", ct)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "<h1") {
		t.Errorf("body missing <h1: %s", body)
	}
	if !strings.Contains(string(body), "Hello") {
		t.Errorf("body missing 'Hello'")
	}
}

func TestHandler_PostRender_BumpsVersion(t *testing.T) {
	dir := t.TempDir()
	file := writeMD(t, dir, "doc.md", "# Hello\n")
	s := newTestState(t, file)
	srv := httptest.NewServer(newHandler(s))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/reload")
	if err != nil {
		t.Fatalf("GET /reload: %v", err)
	}
	var got map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&got)
	resp.Body.Close()
	if v, _ := got["version"].(float64); int(v) != 1 {
		t.Fatalf("initial version = %v, want 1", got["version"])
	}

	resp, err = http.Post(srv.URL+"/render", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("POST /render: %v", err)
	}
	got = map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&got)
	resp.Body.Close()
	if ok, _ := got["ok"].(bool); !ok {
		t.Errorf("ok = %v, want true", got["ok"])
	}
	if v, _ := got["version"].(float64); int(v) != 2 {
		t.Errorf("post-render version = %v, want 2", got["version"])
	}

	resp, err = http.Get(srv.URL + "/reload")
	if err != nil {
		t.Fatalf("GET /reload (2): %v", err)
	}
	got = map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&got)
	resp.Body.Close()
	if v, _ := got["version"].(float64); int(v) != 2 {
		t.Errorf("/reload version = %v, want 2", got["version"])
	}
}

func TestHandler_PostRender_SwitchesFile(t *testing.T) {
	dir := t.TempDir()
	first := writeMD(t, dir, "first.md", "# First\n")
	second := writeMD(t, dir, "second.md", "# Second\n")
	s := newTestState(t, first)
	srv := httptest.NewServer(newHandler(s))
	defer srv.Close()

	body, _ := json.Marshal(map[string]string{"file": second})
	resp, err := http.Post(srv.URL+"/render", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /render: %v", err)
	}
	resp.Body.Close()

	resp, err = http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	// Check for the rendered heading specifically (not just the
	// substring) because bundled JS/CSS assets in the page contain the
	// words "First" and "Second" in unrelated string literals.
	if !strings.Contains(string(page), `>Second</h1>`) {
		t.Errorf("page missing rendered Second heading after switch")
	}
	if strings.Contains(string(page), `>First</h1>`) {
		t.Errorf("page still contains rendered First heading after switch")
	}
}

func TestHandler_PostScroll_OK(t *testing.T) {
	dir := t.TempDir()
	file := writeMD(t, dir, "doc.md", "# Hello\n")
	s := newTestState(t, file)
	srv := httptest.NewServer(newHandler(s))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/scroll", "application/json", strings.NewReader(`{"line":42}`))
	if err != nil {
		t.Fatalf("POST /scroll: %v", err)
	}
	defer resp.Body.Close()
	var got map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&got)
	if ok, _ := got["ok"].(bool); !ok {
		t.Errorf("ok = %v, want true", got["ok"])
	}
	if line, _ := got["line"].(float64); int(line) != 42 {
		t.Errorf("line = %v, want 42", got["line"])
	}
}

func TestHandler_GetReload(t *testing.T) {
	dir := t.TempDir()
	file := writeMD(t, dir, "doc.md", "# Hello\n")
	s := newTestState(t, file)
	srv := httptest.NewServer(newHandler(s))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/reload")
	if err != nil {
		t.Fatalf("GET /reload: %v", err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var got map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&got)
	if v, ok := got["version"]; !ok {
		t.Errorf("missing 'version' field, got %v", got)
	} else if vf, _ := v.(float64); int(vf) != 1 {
		t.Errorf("version = %v, want 1", v)
	}
}

func TestHandler_GetTree_ListsWalkableFiles(t *testing.T) {
	dir := t.TempDir()
	file := writeMD(t, dir, "doc.md", "# Hello\n")
	writeMD(t, dir, "other.md", "# Other\n")
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeMD(t, filepath.Join(dir, "sub"), "nested.md", "# Nested\n")
	writeMD(t, dir, "skip.txt", "not a doc")
	s := newTestState(t, file)
	srv := httptest.NewServer(newHandler(s))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/tree")
	if err != nil {
		t.Fatalf("GET /tree: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got struct {
		Root  string   `json:"root"`
		Files []string `json:"files"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Root == "" {
		t.Errorf("root empty, want absolute path")
	}
	wantFiles := map[string]bool{"doc.md": true, "other.md": true, "sub/nested.md": true}
	for _, f := range got.Files {
		if !wantFiles[f] {
			t.Errorf("unexpected file in tree: %q", f)
		}
		delete(wantFiles, f)
	}
	for f := range wantFiles {
		t.Errorf("tree missing %q", f)
	}
}

func TestHandler_404(t *testing.T) {
	dir := t.TempDir()
	file := writeMD(t, dir, "doc.md", "# Hello\n")
	s := newTestState(t, file)
	srv := httptest.NewServer(newHandler(s))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/nope")
	if err != nil {
		t.Fatalf("GET /nope: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestStdin_RenderCommand(t *testing.T) {
	dir := t.TempDir()
	file := writeMD(t, dir, "doc.md", "# Hello\n")
	s := newTestState(t, file)
	if s.renderVersion != 1 {
		t.Fatalf("initial version = %d, want 1", s.renderVersion)
	}

	cmd := fmt.Sprintf(`{"type":"render","file":%q}`, file) + "\n"
	readStdin(s, strings.NewReader(cmd), func() {})

	if s.renderVersion != 2 {
		t.Errorf("after render: version = %d, want 2", s.renderVersion)
	}
}

func TestStdin_ScrollCommand(t *testing.T) {
	dir := t.TempDir()
	file := writeMD(t, dir, "doc.md", "# Hello\n")
	s := newTestState(t, file)
	versionBefore := s.renderVersion

	readStdin(s, strings.NewReader(`{"type":"scroll","line":7}`+"\n"), func() {})

	if s.renderVersion != versionBefore {
		t.Errorf("scroll changed version: got %d, want %d", s.renderVersion, versionBefore)
	}
}

func TestStdin_BlankLines(t *testing.T) {
	dir := t.TempDir()
	file := writeMD(t, dir, "doc.md", "# Hello\n")
	s := newTestState(t, file)
	versionBefore := s.renderVersion

	input := "\n   \nnot json\n{not:json}\n\n"
	readStdin(s, strings.NewReader(input), func() {})

	if s.renderVersion != versionBefore {
		t.Errorf("blank/invalid input changed version: got %d, want %d", s.renderVersion, versionBefore)
	}
}

func TestHandler_RejectsForeignOrigin(t *testing.T) {
	dir := t.TempDir()
	file := writeMD(t, dir, "doc.md", "# Hello\n")
	s := newTestState(t, file)
	srv := httptest.NewServer(newHandler(s))
	defer srv.Close()

	for _, path := range []string{"/", "/reload", "/render", "/scroll", "/ws", "/_img/pic.png"} {
		method := http.MethodGet
		if path == "/render" || path == "/scroll" {
			method = http.MethodPost
		}
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader("{}"))
		req.Header.Set("Origin", "http://evil.example.com")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s %s with foreign Origin → status = %d, want 403", method, path, resp.StatusCode)
		}
	}
}

func TestHandler_RejectsForeignHost(t *testing.T) {
	// Simulate DNS rebinding: same TCP socket, but the browser sends a
	// non-loopback Host header.
	dir := t.TempDir()
	file := writeMD(t, dir, "doc.md", "# Hello\n")
	s := newTestState(t, file)
	srv := httptest.NewServer(newHandler(s))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/", nil)
	req.Host = "evil.example.com"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("rebound Host → status = %d, want 403", resp.StatusCode)
	}
}

func TestHandler_GetImg_ServesFileInsideDir(t *testing.T) {
	dir := t.TempDir()
	file := writeMD(t, dir, "doc.md", "# Hello\n")
	imgBytes := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0, 0, 0}
	if err := os.WriteFile(filepath.Join(dir, "pic.png"), imgBytes, 0o644); err != nil {
		t.Fatalf("write pic.png: %v", err)
	}
	s := newTestState(t, file)
	srv := httptest.NewServer(newHandler(s))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/_img/pic.png")
	if err != nil {
		t.Fatalf("GET /_img/pic.png: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	got, _ := io.ReadAll(resp.Body)
	if !bytes.Equal(got, imgBytes) {
		t.Errorf("body = %x, want %x", got, imgBytes)
	}
}

func TestHandler_GetImg_404OnMissing(t *testing.T) {
	dir := t.TempDir()
	file := writeMD(t, dir, "doc.md", "# Hello\n")
	s := newTestState(t, file)
	srv := httptest.NewServer(newHandler(s))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/_img/nope.png")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestHandler_GetImg_404OnDirectory(t *testing.T) {
	dir := t.TempDir()
	file := writeMD(t, dir, "doc.md", "# Hello\n")
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	s := newTestState(t, file)
	srv := httptest.NewServer(newHandler(s))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/_img/sub")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

// Go's http.ServeMux cleans /_img/../etc to /etc before dispatch, so the
// only way an outside path reaches handleImg is via a literal segment
// that resolves out. Exercise that codepath directly.
func TestHandleImg_RejectsEscapeAttempt(t *testing.T) {
	outerDir := t.TempDir()
	innerDir := filepath.Join(outerDir, "served")
	if err := os.Mkdir(innerDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	file := writeMD(t, innerDir, "doc.md", "# Hello\n")
	target := filepath.Join(outerDir, "secret.txt")
	if err := os.WriteFile(target, []byte("nope"), 0o644); err != nil {
		t.Fatalf("write secret: %v", err)
	}
	s := newTestState(t, file)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/_img/../secret.txt", nil)
	s.handleImg(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rr.Code)
	}
}

// Sparse file via Truncate keeps the test cheap on disk while
// info.Size() still trips the maxImgBytes cap.
func TestHandler_GetImg_RejectsOversizedFile(t *testing.T) {
	dir := t.TempDir()
	file := writeMD(t, dir, "doc.md", "# Hello\n")
	big, err := os.Create(filepath.Join(dir, "big.png"))
	if err != nil {
		t.Fatalf("create big: %v", err)
	}
	if err := big.Truncate(int64(maxImgBytes) + 1); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	big.Close()
	s := newTestState(t, file)
	srv := httptest.NewServer(newHandler(s))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/_img/big.png")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", resp.StatusCode)
	}
}

func TestHandler_GetImg_URLEncodedTraversal(t *testing.T) {
	dir := t.TempDir()
	file := writeMD(t, dir, "doc.md", "# Hello\n")
	if err := os.WriteFile(filepath.Join(filepath.Dir(dir), "secret.txt"), []byte("nope"), 0o644); err != nil {
		t.Fatalf("write secret: %v", err)
	}
	s := newTestState(t, file)
	srv := httptest.NewServer(newHandler(s))
	defer srv.Close()

	// %2e%2e decodes to ".." before the mux routes; the cleaned path
	// either redirects out of /_img/ or 404s, but must never serve the
	// sibling secret.
	resp, err := http.Get(srv.URL + "/_img/%2e%2e/secret.txt")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusOK && bytes.Contains(body, []byte("nope")) {
		t.Errorf("URL-encoded traversal leaked secret content: %s", body)
	}
}

// Regression for the macOS /var/folders → /private/var/folders class
// of bug: when the served dir itself traverses a symlink, EvalSymlinks
// on a legitimate image inside it returns the resolved-tree path,
// which must still pass the post-resolve confinement check.
func TestHandler_GetImg_AcceptsServedDirThroughSymlink(t *testing.T) {
	real := t.TempDir()
	if err := os.WriteFile(filepath.Join(real, "pic.png"), []byte("png"), 0o644); err != nil {
		t.Fatalf("write pic: %v", err)
	}
	if err := os.WriteFile(filepath.Join(real, "doc.md"), []byte("# Hello\n"), 0o644); err != nil {
		t.Fatalf("write doc: %v", err)
	}

	parent := t.TempDir()
	linked := filepath.Join(parent, "served")
	if err := os.Symlink(real, linked); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}

	s := newTestState(t, filepath.Join(linked, "doc.md"))
	srv := httptest.NewServer(newHandler(s))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/_img/pic.png")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 for image under symlinked served dir", resp.StatusCode)
	}
}

func TestHandler_GetImg_RejectsNonGET(t *testing.T) {
	dir := t.TempDir()
	file := writeMD(t, dir, "doc.md", "# Hello\n")
	if err := os.WriteFile(filepath.Join(dir, "pic.png"), []byte("png"), 0o644); err != nil {
		t.Fatalf("write pic: %v", err)
	}
	s := newTestState(t, file)
	srv := httptest.NewServer(newHandler(s))
	defer srv.Close()

	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		req, _ := http.NewRequest(m, srv.URL+"/_img/pic.png", strings.NewReader(""))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s: %v", m, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("%s status = %d, want 405", m, resp.StatusCode)
		}
	}
}

// Symlink inside the served dir that points outside must not exfiltrate
// the target. http.ServeFile follows symlinks, so handleImg re-runs
// pathInsideDir on the resolved path.
func TestHandler_GetImg_RejectsSymlinkEscape(t *testing.T) {
	outer := t.TempDir()
	inner := filepath.Join(outer, "served")
	if err := os.Mkdir(inner, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	file := writeMD(t, inner, "doc.md", "# Hello\n")
	target := filepath.Join(outer, "secret.txt")
	if err := os.WriteFile(target, []byte("nope"), 0o644); err != nil {
		t.Fatalf("write secret: %v", err)
	}
	if err := os.Symlink(target, filepath.Join(inner, "evil.png")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	s := newTestState(t, file)
	srv := httptest.NewServer(newHandler(s))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/_img/evil.png")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusOK && bytes.Contains(body, []byte("nope")) {
		t.Errorf("symlink to outside dir leaked secret; status=%d body=%s", resp.StatusCode, body)
	}
}

func TestHandler_GetImg_SetsCacheControlNoStore(t *testing.T) {
	dir := t.TempDir()
	file := writeMD(t, dir, "doc.md", "# Hello\n")
	if err := os.WriteFile(filepath.Join(dir, "pic.png"), []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := newTestState(t, file)
	srv := httptest.NewServer(newHandler(s))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/_img/pic.png")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
}

// Every /render error path must return application/json with a parseable
// {"error": "..."} body so the WS-client click handler can toast it.
func TestHandler_PostRender_ErrorsAreJSON(t *testing.T) {
	dir := t.TempDir()
	file := writeMD(t, dir, "doc.md", "# Hello\n")
	weird := filepath.Join(dir, "binary.bin")
	if err := os.WriteFile(weird, []byte("not renderable"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := newTestState(t, file)
	srv := httptest.NewServer(newHandler(s))
	defer srv.Close()

	cases := []struct {
		name      string
		file      string
		wantCodes []int
	}{
		{"out of tree", "/etc/passwd", []int{http.StatusForbidden}},
		{"missing", filepath.Join(dir, "nope.md"), []int{http.StatusNotFound}},
		{"unsupported extension", weird, []int{http.StatusUnsupportedMediaType}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, _ := json.Marshal(map[string]string{"file": tc.file})
			resp, err := http.Post(srv.URL+"/render", "application/json", bytes.NewReader(body))
			if err != nil {
				t.Fatalf("POST: %v", err)
			}
			defer resp.Body.Close()
			matched := false
			for _, c := range tc.wantCodes {
				if resp.StatusCode == c {
					matched = true
					break
				}
			}
			if !matched {
				t.Errorf("status = %d, want one of %v", resp.StatusCode, tc.wantCodes)
			}
			if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", ct)
			}
			var got map[string]string
			if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			if got["error"] == "" {
				t.Errorf("body missing 'error' field: %v", got)
			}
		})
	}
}

func TestHandler_GetHTML_LeavesOutOfTreeImgSrcUntouched(t *testing.T) {
	dir := t.TempDir()
	file := writeMD(t, dir, "doc.md", "![out](/etc/passwd)\n")
	s := newTestState(t, file)
	srv := httptest.NewServer(newHandler(s))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `src="/etc/passwd"`) {
		t.Errorf("expected original out-of-tree src preserved; body=%s", body)
	}
	if strings.Contains(string(body), `/_img/etc/passwd`) {
		t.Errorf("out-of-tree path leaked into /_img/ rewrite")
	}
}

func TestHandler_PostRender_RewritesImgsAgainstNewFileDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	root := writeMD(t, dir, "root.md", "# Root\n")
	sub := writeMD(t, filepath.Join(dir, "sub"), "page.md", "![](inner.png)\n")
	if err := os.WriteFile(filepath.Join(dir, "sub", "inner.png"), []byte("png"), 0o644); err != nil {
		t.Fatalf("write inner: %v", err)
	}
	s := newTestState(t, root)
	srv := httptest.NewServer(newHandler(s))
	defer srv.Close()

	body, _ := json.Marshal(map[string]string{"file": sub})
	resp, err := http.Post(srv.URL+"/render", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /render: %v", err)
	}
	resp.Body.Close()

	resp, err = http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()
	page, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(page), `src="/_img/sub/inner.png"`) {
		t.Errorf("expected /_img/sub/inner.png after switch; body=%s", page)
	}
}

func TestHandler_GetHTML_RewritesLocalImgSrcs(t *testing.T) {
	dir := t.TempDir()
	file := writeMD(t, dir, "doc.md", "![](pic.png)\n![remote](https://example.com/x.png)\n")
	if err := os.WriteFile(filepath.Join(dir, "pic.png"), []byte("png"), 0o644); err != nil {
		t.Fatalf("write pic: %v", err)
	}
	s := newTestState(t, file)
	srv := httptest.NewServer(newHandler(s))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `src="/_img/pic.png"`) {
		t.Errorf("body missing rewritten local src: %s", body)
	}
	if !strings.Contains(string(body), `src="https://example.com/x.png"`) {
		t.Errorf("body missing untouched remote src")
	}
}

func TestHandler_PostRender_NonexistentFile(t *testing.T) {
	dir := t.TempDir()
	file := writeMD(t, dir, "doc.md", "# Hello\n")
	s := newTestState(t, file)
	srv := httptest.NewServer(newHandler(s))
	defer srv.Close()

	missing := filepath.Join(dir, "no-such-file.md")
	body, _ := json.Marshal(map[string]string{"file": missing})
	resp, err := http.Post(srv.URL+"/render", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /render: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for missing file", resp.StatusCode)
	}
	var got map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if !strings.Contains(got["error"], "file not found") {
		t.Errorf(`error = %q, want "file not found..."`, got["error"])
	}
}

func TestHandler_PostRender_UnsupportedExtension(t *testing.T) {
	dir := t.TempDir()
	file := writeMD(t, dir, "doc.md", "# Hello\n")
	weird := filepath.Join(dir, "binary.bin")
	if err := os.WriteFile(weird, []byte("not renderable"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := newTestState(t, file)
	srv := httptest.NewServer(newHandler(s))
	defer srv.Close()

	body, _ := json.Marshal(map[string]string{"file": weird})
	resp, err := http.Post(srv.URL+"/render", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /render: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Errorf("status = %d, want 415 for unsupported extension", resp.StatusCode)
	}
	var got map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if !strings.Contains(got["error"], "unsupported format") {
		t.Errorf(`error = %q, want "unsupported format..."`, got["error"])
	}
}

func TestHandler_PostRender_AcceptsPandocExtension(t *testing.T) {
	dir := t.TempDir()
	file := writeMD(t, dir, "doc.md", "# Hello\n")
	tex := filepath.Join(dir, "sample.tex")
	if err := os.WriteFile(tex, []byte(`\section{X}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s := newTestState(t, file)
	srv := httptest.NewServer(newHandler(s))
	defer srv.Close()

	body, _ := json.Marshal(map[string]string{"file": tex})
	resp, err := http.Post(srv.URL+"/render", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /render: %v", err)
	}
	defer resp.Body.Close()
	// Either 200 (pandoc present) or some 5xx (pandoc missing). The
	// dispatch itself should NOT reject .tex with 415 even when the
	// render later fails, since the extension is in InputFormat.
	if resp.StatusCode == http.StatusUnsupportedMediaType {
		t.Errorf("got 415 for .tex; should pass the dispatch check")
	}
}

func TestHandler_PostRender_RejectsPathOutsideServedDir(t *testing.T) {
	dir := t.TempDir()
	file := writeMD(t, dir, "doc.md", "# Hello\n")
	s := newTestState(t, file)
	srv := httptest.NewServer(newHandler(s))
	defer srv.Close()

	body, _ := json.Marshal(map[string]string{"file": "/etc/passwd"})
	resp, err := http.Post(srv.URL+"/render", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /render: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403 for foreign path", resp.StatusCode)
	}
}

func TestHandler_PostRender_RejectsBodyTooLarge(t *testing.T) {
	dir := t.TempDir()
	file := writeMD(t, dir, "doc.md", "# Hello\n")
	s := newTestState(t, file)
	srv := httptest.NewServer(newHandler(s))
	defer srv.Close()

	huge := bytes.Repeat([]byte("a"), maxJSONBodyBytes+1)
	resp, err := http.Post(srv.URL+"/render", "application/json", bytes.NewReader(huge))
	if err != nil {
		t.Fatalf("POST /render: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413 for oversized body", resp.StatusCode)
	}
}

// Symlink inside the served dir that resolves to an out-of-tree file
// must be rejected by /render. The lexical pathInsideDir passes (the
// link sits inside fileDir) so the second check on the resolved path
// is what stops the contents from being broadcast over WS.
func TestHandler_PostRender_RejectsSymlinkEscape(t *testing.T) {
	outer := t.TempDir()
	inner := filepath.Join(outer, "served")
	if err := os.Mkdir(inner, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	file := writeMD(t, inner, "doc.md", "# Hello\n")
	target := filepath.Join(outer, "secret.md")
	if err := os.WriteFile(target, []byte("# Secret\nnope-classified\n"), 0o644); err != nil {
		t.Fatalf("write secret: %v", err)
	}
	link := filepath.Join(inner, "evil.md")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}
	s := newTestState(t, file)
	srv := httptest.NewServer(newHandler(s))
	defer srv.Close()

	body, _ := json.Marshal(map[string]string{"file": link})
	resp, err := http.Post(srv.URL+"/render", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /render: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403 for symlink escape", resp.StatusCode)
	}

	getResp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer getResp.Body.Close()
	page, _ := io.ReadAll(getResp.Body)
	if bytes.Contains(page, []byte("nope-classified")) {
		t.Errorf("rendered page leaked out-of-tree symlink target")
	}
}

func TestHandler_PostRender_EmitsNavigateLineOnFileSwitch(t *testing.T) {
	dir := t.TempDir()
	first := writeMD(t, dir, "first.md", "# First\n")
	second := writeMD(t, dir, "second.md", "# Second\n")
	s := newTestState(t, first)
	var buf bytes.Buffer
	s.eventLog = &buf
	srv := httptest.NewServer(newHandler(s))
	defer srv.Close()

	body, _ := json.Marshal(map[string]string{"file": second})
	resp, err := http.Post(srv.URL+"/render", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /render switch: %v", err)
	}
	resp.Body.Close()

	absSecond, _ := filepath.Abs(second)
	resolvedSecond, _ := filepath.EvalSymlinks(absSecond)
	wantLine := "[md-preview] navigate: " + resolvedSecond + "\n"
	if got := buf.String(); got != wantLine {
		t.Errorf("eventLog = %q, want %q", got, wantLine)
	}

	buf.Reset()
	resp, err = http.Post(srv.URL+"/render", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /render re-render: %v", err)
	}
	resp.Body.Close()
	if got := buf.String(); got != "" {
		t.Errorf("re-render of same file emitted navigate: %q", got)
	}
}

func TestReadStdin_RenderUpdatesFileDirAcrossTrees(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()
	fileA := writeMD(t, dirA, "a.md", "# A\n")
	fileB := writeMD(t, dirB, "b.md", "# B\n")
	s := newTestState(t, fileA)
	var buf bytes.Buffer
	s.eventLog = &buf

	cmd := fmt.Sprintf(`{"type":"render","file":%q}`, fileB) + "\n"
	readStdin(s, strings.NewReader(cmd), func() {})

	wantFile, _ := filepath.Abs(fileB)
	wantDir := filepath.Dir(wantFile)
	wantResolved, _ := filepath.EvalSymlinks(wantDir)

	s.mu.Lock()
	gotFile := s.file
	gotDir := s.fileDir
	gotResolved := s.fileDirResolved
	s.mu.Unlock()

	if gotFile != wantFile {
		t.Errorf("file = %q, want %q", gotFile, wantFile)
	}
	if gotDir != wantDir {
		t.Errorf("fileDir = %q, want %q", gotDir, wantDir)
	}
	if gotResolved != wantResolved {
		t.Errorf("fileDirResolved = %q, want %q", gotResolved, wantResolved)
	}
	wantLine := "[md-preview] navigate: " + wantFile + "\n"
	if got := buf.String(); got != wantLine {
		t.Errorf("navigate line = %q, want %q", got, wantLine)
	}
}

func TestHandler_GetIndex_IncludesExtraCSS(t *testing.T) {
	dir := t.TempDir()
	file := writeMD(t, dir, "doc.md", "# Hello\n")
	s := newTestState(t, file)
	marker := "/*mdp-extra-css-marker*/"
	s.extraCSS = marker
	srv := httptest.NewServer(newHandler(s))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if !bytes.Contains(body, []byte(marker)) {
		t.Errorf("page missing extraCSS marker %q", marker)
	}
}

func TestStdin_QuitCommand(t *testing.T) {
	dir := t.TempDir()
	file := writeMD(t, dir, "doc.md", "# Hello\n")
	s := newTestState(t, file)

	var (
		called bool
		mu     sync.Mutex
	)
	quit := func() {
		mu.Lock()
		called = true
		mu.Unlock()
	}

	done := make(chan struct{})
	go func() {
		readStdin(s, strings.NewReader(`{"type":"quit"}`+"\n"), quit)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("readStdin did not return after quit")
	}

	mu.Lock()
	defer mu.Unlock()
	if !called {
		t.Errorf("quit callback not invoked")
	}
}

type fakeConn struct {
	writeStarted chan struct{}
	writeDone    chan struct{}
	writes       [][]byte
	deadline     time.Time
	slow         bool
	closed       bool
	mu           sync.Mutex
}

func newFastConn() *fakeConn {
	return &fakeConn{writeStarted: make(chan struct{}, 1), writeDone: make(chan struct{}, 1)}
}

func newSlowConn() *fakeConn {
	return &fakeConn{writeStarted: make(chan struct{}, 1), writeDone: make(chan struct{}, 1), slow: true}
}

func (f *fakeConn) Read(b []byte) (int, error) { return 0, io.EOF }

func (f *fakeConn) Write(b []byte) (int, error) {
	select {
	case f.writeStarted <- struct{}{}:
	default:
	}
	if !f.slow {
		dup := make([]byte, len(b))
		copy(dup, b)
		f.mu.Lock()
		f.writes = append(f.writes, dup)
		f.mu.Unlock()
		select {
		case f.writeDone <- struct{}{}:
		default:
		}
		return len(b), nil
	}
	f.mu.Lock()
	deadline := f.deadline
	f.mu.Unlock()
	if !deadline.IsZero() {
		d := time.Until(deadline)
		if d <= 0 {
			return 0, errors.New("write deadline exceeded")
		}
		time.Sleep(d)
		return 0, errors.New("write deadline exceeded")
	}
	select {}
}

func (f *fakeConn) writeFrames() [][]byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([][]byte, len(f.writes))
	copy(out, f.writes)
	return out
}

func (f *fakeConn) Close() error {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
	return nil
}

func (f *fakeConn) isClosed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

func (f *fakeConn) LocalAddr() net.Addr                { return &net.TCPAddr{} }
func (f *fakeConn) RemoteAddr() net.Addr               { return &net.TCPAddr{} }
func (f *fakeConn) SetDeadline(t time.Time) error      { return f.SetWriteDeadline(t) }
func (f *fakeConn) SetReadDeadline(_ time.Time) error  { return nil }
func (f *fakeConn) SetWriteDeadline(t time.Time) error {
	f.mu.Lock()
	f.deadline = t
	f.mu.Unlock()
	return nil
}

func TestBroadcast_FanOut_SlowClientDoesNotStallOthers(t *testing.T) {
	dir := t.TempDir()
	file := writeMD(t, dir, "doc.md", "# Hello\n")
	s := newTestState(t, file)
	s.eventLog = io.Discard

	fast := newFastConn()
	slow := newSlowConn()
	s.addClient(fast)
	s.addClient(slow)

	done := make(chan struct{})
	go func() {
		s.broadcast("hello")
		close(done)
	}()

	select {
	case <-fast.writeDone:
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("fast client did not receive within 500ms; slow client stalled fan-out")
	}

	select {
	case <-done:
	case <-time.After(wsWriteTimeout + 500*time.Millisecond):
		t.Fatalf("broadcast did not return after slow client's deadline")
	}

	s.mu.Lock()
	_, slowStillRegistered := s.wsClients[slow]
	_, fastStillRegistered := s.wsClients[fast]
	s.mu.Unlock()
	if slowStillRegistered {
		t.Errorf("slow client was not evicted after write timeout")
	}
	if !fastStillRegistered {
		t.Errorf("fast client was evicted; should have stayed registered")
	}
	if !slow.isClosed() {
		t.Errorf("slow client conn was not closed on eviction")
	}
}

func TestBroadcastScroll_Coalesces(t *testing.T) {
	dir := t.TempDir()
	file := writeMD(t, dir, "doc.md", "# Hello\n")
	s := newTestState(t, file)
	s.eventLog = io.Discard

	client := newFastConn()
	s.addClient(client)

	for i := 1; i <= 10; i++ {
		s.broadcastScroll(i)
	}
	time.Sleep(scrollCoalesce * 4)

	frames := client.writeFrames()
	if len(frames) > 2 {
		t.Fatalf("got %d frames, want <= 2 (coalesce window dropped older lines)", len(frames))
	}
	if len(frames) == 0 {
		t.Fatalf("got 0 frames, want at least 1 (flush should fire once after window)")
	}
	last := string(frames[len(frames)-1])
	if !strings.Contains(last, `"line":10`) {
		t.Errorf("last frame = %q, want one carrying the newest line value (10)", last)
	}
}
