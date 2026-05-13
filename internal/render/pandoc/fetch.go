package pandoc

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
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Version pins the upstream pandoc release auto-fetched into the user
// cache. Bumping this invalidates the cache (new versioned dir) but
// leaves the old install in place; users can `rm -rf ~/.cache/mdp`
// to reclaim disk.
const Version = "3.9.0.2"

// ErrFormatUnsupported is returned when neither the host pandoc nor
// the pinned auto-fetch version can read the requested input format.
var ErrFormatUnsupported = errors.New("pandoc: format not supported")

// downloadPandocFn lets tests replace the network-touching download
// step without exposing flag soup. Defaults to downloadPandoc.
var downloadPandocFn = downloadPandoc

// Ensure returns the path to a pandoc binary that handles format
// (a `pandoc --from` name like "latex" or "djot"). Probe order:
//
//  1. cache (~/.cache/mdp/pandoc-<Version>/pandoc) — always wins
//  2. $PATH pandoc that lists format in --list-input-formats
//  3. download the pinned release if its embedded format list
//     includes format
//
// If format is "" the format-support check is skipped; any pandoc is
// acceptable. Progress messages go to stderr.
func Ensure(ctx context.Context, format string, stderr io.Writer) (string, error) {
	probeMu.Lock()
	defer probeMu.Unlock()
	if p, ok := findCachedPandoc(); ok {
		binPath = p
		return p, nil
	}
	if p, err := exec.LookPath("pandoc"); err == nil {
		if format == "" || hostSupports(p, format) {
			binPath = p
			return p, nil
		}
		fmt.Fprintf(stderr, "mdp: system pandoc at %s does not support %q; auto-fetching pinned version %s\n", p, format, Version)
	}
	if format != "" && !supportedByPinned[format] {
		return "", fmt.Errorf("%w: %q not supported by pinned pandoc %s either; upgrade your system pandoc", ErrFormatUnsupported, format, Version)
	}
	p, err := downloadPandocFn(ctx, stderr)
	if err != nil {
		return "", err
	}
	binPath = p
	return p, nil
}

func cachedPandocPath() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "mdp", "pandoc-"+Version, "pandoc"), nil
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
	base := "https://github.com/jgm/pandoc/releases/download/" + Version
	switch {
	case goos == "linux" && goarch == "amd64":
		return pandocSource{url: base + "/pandoc-" + Version + "-linux-amd64.tar.gz", innerMatch: "/bin/pandoc"}, nil
	case goos == "linux" && goarch == "arm64":
		return pandocSource{url: base + "/pandoc-" + Version + "-linux-arm64.tar.gz", innerMatch: "/bin/pandoc"}, nil
	case goos == "darwin" && goarch == "arm64":
		return pandocSource{url: base + "/pandoc-" + Version + "-arm64-macOS.zip", isZip: true, innerMatch: "/bin/pandoc"}, nil
	case goos == "darwin" && goarch == "amd64":
		return pandocSource{url: base + "/pandoc-" + Version + "-x86_64-macOS.zip", isZip: true, innerMatch: "/bin/pandoc"}, nil
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
