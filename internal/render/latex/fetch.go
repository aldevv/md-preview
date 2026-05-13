package latex

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// PandocVersion pins the upstream release auto-fetched into the user
// cache. Bumping this invalidates the cache (new versioned dir) but
// leaves the old install in place; users can `rm -rf ~/.cache/mdp`
// to reclaim disk.
const PandocVersion = "3.9.0.2"

// EnsurePandoc returns the path to a usable pandoc binary. Probes
// $PATH and the per-version cache dir; if neither has one, downloads
// the upstream static release into the cache. Progress messages go
// to stderr. Idempotent across calls in the same process.
func EnsurePandoc(ctx context.Context, stderr io.Writer) (string, error) {
	if PandocAvailable() {
		pandocOnceMu.Lock()
		defer pandocOnceMu.Unlock()
		return pandocBin, nil
	}
	pandocOnceMu.Lock()
	defer pandocOnceMu.Unlock()
	p, err := downloadPandoc(ctx, stderr)
	if err != nil {
		return "", err
	}
	pandocBin = p
	return p, nil
}

func cachedPandocPath() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "mdp", "pandoc-"+PandocVersion, "pandoc"), nil
}

func findCachedPandoc() (string, bool) {
	p, err := cachedPandocPath()
	if err != nil {
		return "", false
	}
	info, err := os.Stat(p)
	if err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
		return "", false
	}
	return p, true
}

type pandocSource struct {
	url        string
	isZip      bool
	innerMatch string // suffix the pandoc binary's path inside the archive should end with
}

func pandocSourceFor(goos, goarch string) (pandocSource, error) {
	base := "https://github.com/jgm/pandoc/releases/download/" + PandocVersion
	switch {
	case goos == "linux" && goarch == "amd64":
		return pandocSource{url: base + "/pandoc-" + PandocVersion + "-linux-amd64.tar.gz", innerMatch: "/bin/pandoc"}, nil
	case goos == "linux" && goarch == "arm64":
		return pandocSource{url: base + "/pandoc-" + PandocVersion + "-linux-arm64.tar.gz", innerMatch: "/bin/pandoc"}, nil
	case goos == "darwin" && goarch == "arm64":
		return pandocSource{url: base + "/pandoc-" + PandocVersion + "-arm64-macOS.zip", isZip: true, innerMatch: "/bin/pandoc"}, nil
	case goos == "darwin" && goarch == "amd64":
		return pandocSource{url: base + "/pandoc-" + PandocVersion + "-x86_64-macOS.zip", isZip: true, innerMatch: "/bin/pandoc"}, nil
	}
	return pandocSource{}, fmt.Errorf("no auto-fetch for %s/%s; install pandoc manually", goos, goarch)
}

func downloadPandoc(ctx context.Context, stderr io.Writer) (string, error) {
	src, err := pandocSourceFor(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return "", err
	}
	dst, err := cachedPandocPath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", err
	}
	fmt.Fprintf(stderr, "mdp: pandoc not found, fetching %s (~30 MB)\n", src.url)
	bin, err := fetchAndExtract(ctx, src)
	if err != nil {
		return "", err
	}
	tmp := dst + ".partial"
	if err := os.WriteFile(tmp, bin, 0o755); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	fmt.Fprintf(stderr, "mdp: installed pandoc to %s\n", dst)
	return dst, nil
}

func fetchAndExtract(ctx context.Context, src pandocSource) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src.url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download pandoc: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download pandoc: status %s", resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("download pandoc: %w", err)
	}
	if src.isZip {
		return extractFromZip(body, src.innerMatch)
	}
	return extractFromTarGz(body, src.innerMatch)
}

func extractFromTarGz(archive []byte, suffix string) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if hdr.Typeflag == tar.TypeReg && strings.HasSuffix(hdr.Name, suffix) {
			return io.ReadAll(tr)
		}
	}
	return nil, fmt.Errorf("pandoc binary (%s) not found in archive", suffix)
}

func extractFromZip(archive []byte, suffix string) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return nil, err
	}
	for _, f := range zr.File {
		if !strings.HasSuffix(f.Name, suffix) {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		return io.ReadAll(rc)
	}
	return nil, fmt.Errorf("pandoc binary (%s) not found in archive", suffix)
}
