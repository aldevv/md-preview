package main

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
)

const (
	updateAPI             = "https://api.github.com/repos/aldevv/md-preview/releases/latest"
	updateTarballURLFmt   = "https://github.com/aldevv/md-preview/releases/download/%s/mdp_%s_%s.tar.gz"
	updateChecksumsURLFmt = "https://github.com/aldevv/md-preview/releases/download/%s/checksums.txt"
	updateGoModule        = "github.com/aldevv/md-preview/cmd/mdp"
	updateMaxTarballBytes = 100 << 20
	updateHTTPTimeout     = 30 * time.Second
)

// Validates both the GitHub API response and the --version flag.
// Defends against tag-name injection into the tarball URL and into the
// `go install module@<tag>` arg vector.
var tagPattern = regexp.MustCompile(`^v[0-9A-Za-z.+\-]+$`)

const updateUsage = `Usage: mdp update [--check] [--force] [--version vX.Y.Z]`

func runUpdate(args []string, stdout, stderr io.Writer, env Environment) int {
	fset := flag.NewFlagSet("mdp update", flag.ContinueOnError)
	fset.SetOutput(stderr)
	fset.Usage = func() { fmt.Fprintln(stdout, updateUsage) }
	checkFlag := fset.Bool("check", false, "")
	forceFlag := fset.Bool("force", false, "")
	versionFlag := fset.String("version", "", "")
	if err := fset.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		fmt.Fprintln(stderr, updateUsage)
		return 1
	}
	if fset.NArg() > 0 {
		fmt.Fprintf(stderr, "mdp update: unexpected arg %q\n", fset.Arg(0))
		fmt.Fprintln(stderr, updateUsage)
		return 1
	}

	target := *versionFlag
	if target == "" {
		latest, err := fetchLatestTag(env)
		if err != nil {
			fmt.Fprintf(stderr, "mdp update: resolving latest release: %v\n", err)
			return 1
		}
		target = latest
	} else if !tagPattern.MatchString(target) {
		fmt.Fprintf(stderr, "mdp update: invalid --version %q (expected vX.Y.Z)\n", target)
		return 1
	}

	current := buildVersion()
	if current == target {
		fmt.Fprintf(stdout, "mdp update: already at %s\n", current)
		return 0
	}
	if *checkFlag {
		fmt.Fprintf(stdout, "mdp update: %s -> %s available\n", current, target)
		return 0
	}
	if !*forceFlag && (current == "(devel)" || current == "(unknown)") {
		fmt.Fprintf(stderr, "mdp update: current binary reports %s (not a release build); refusing to overwrite without --force\n", current)
		return 1
	}

	dest, err := env.Executable()
	if err != nil {
		fmt.Fprintf(stderr, "mdp update: resolving executable path: %v\n", err)
		return 1
	}

	if _, err := env.LookPath("go"); err == nil {
		gobin := filepath.Dir(dest)
		if err := checkDirWritable(gobin); err != nil {
			if errors.Is(err, os.ErrPermission) {
				fmt.Fprintf(stderr, "mdp update: cannot write to %s (permission denied). Re-run with sudo, or reinstall to a user-writable directory.\n", gobin)
			} else {
				fmt.Fprintf(stderr, "mdp update: cannot use %s: %v\n", gobin, err)
			}
			return 1
		}
		fmt.Fprintf(stdout, "mdp update: %s -> %s (go install)\n", current, target)
		environ := append(os.Environ(), "GOBIN="+gobin)
		if err := env.RunCmd("go", []string{"install", updateGoModule + "@" + target}, environ); err != nil {
			fmt.Fprintf(stderr, "mdp update: go install %s@%s failed: %v\n", updateGoModule, target, err)
			return 1
		}
		fmt.Fprintf(stdout, "mdp update: installed %s (%s)\n", target, filepath.Join(gobin, "mdp"))
		return 0
	}

	fmt.Fprintf(stdout, "mdp update: %s -> %s (downloading release tarball)\n", current, target)
	if err := downloadAndReplace(env, target, dest); err != nil {
		if errors.Is(err, os.ErrPermission) {
			fmt.Fprintf(stderr, "mdp update: cannot write to %s (permission denied). Re-run with sudo, or reinstall to a user-writable directory.\n", filepath.Dir(dest))
		} else {
			fmt.Fprintf(stderr, "mdp update: %v\n", err)
		}
		return 1
	}
	fmt.Fprintf(stdout, "mdp update: installed %s (%s)\n", target, dest)
	return 0
}

func fetchLatestTag(env Environment) (string, error) {
	body, err := env.HTTPGet(updateAPI)
	if err != nil {
		return "", err
	}
	defer body.Close()
	data, err := io.ReadAll(io.LimitReader(body, 1<<20))
	if err != nil {
		return "", err
	}
	var rel struct {
		TagName string `json:"tag_name"`
	}
	if err := json.Unmarshal(data, &rel); err != nil {
		return "", fmt.Errorf("parsing release JSON: %w", err)
	}
	if rel.TagName == "" {
		return "", fmt.Errorf("no tag_name in response from %s", updateAPI)
	}
	if !tagPattern.MatchString(rel.TagName) {
		return "", fmt.Errorf("untrusted tag %q from release API", rel.TagName)
	}
	return rel.TagName, nil
}

func downloadAndReplace(env Environment, tag, dest string) error {
	osTag := env.GOOS
	if osTag != "linux" && osTag != "darwin" {
		return fmt.Errorf("unsupported os: %s", osTag)
	}
	arch := env.GOARCH
	if arch != "amd64" && arch != "arm64" {
		return fmt.Errorf("unsupported arch: %s", arch)
	}

	tarballName := fmt.Sprintf("mdp_%s_%s.tar.gz", osTag, arch)
	expected, err := fetchExpectedChecksum(env, tag, tarballName)
	if err != nil {
		return fmt.Errorf("fetching checksums: %w", err)
	}

	tarballURL := fmt.Sprintf(updateTarballURLFmt, tag, osTag, arch)
	body, err := env.HTTPGet(tarballURL)
	if err != nil {
		return fmt.Errorf("downloading %s: %w", tarballURL, err)
	}
	defer body.Close()

	// teed sits between the size-capped body and gzip so the hasher sees
	// the same gzipped bytes the server sent (matching goreleaser's
	// checksums.txt, which hashes the .tar.gz as served).
	limited := io.LimitReader(body, updateMaxTarballBytes)
	hasher := sha256.New()
	teed := io.TeeReader(limited, hasher)

	gz, err := gzip.NewReader(teed)
	if err != nil {
		return fmt.Errorf("gunzip: %w", err)
	}
	defer gz.Close()

	tmp := dest + ".new"
	if err := extractMDPBinary(gz, tmp, dest); err != nil {
		return err
	}

	// Drain remaining gzip output so the hasher covers the full archive,
	// not just the bytes consumed by the mdp entry. Otherwise checksum
	// verification would only validate the prefix that fed the extraction.
	if _, err := io.Copy(io.Discard, gz); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("draining body: %w", err)
	}

	got := hex.EncodeToString(hasher.Sum(nil))
	if !strings.EqualFold(got, expected) {
		os.Remove(tmp)
		return fmt.Errorf("checksum mismatch for %s: got %s, want %s", tarballName, got, expected)
	}

	if err := os.Rename(tmp, dest); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

func fetchExpectedChecksum(env Environment, tag, filename string) (string, error) {
	url := fmt.Sprintf(updateChecksumsURLFmt, tag)
	body, err := env.HTTPGet(url)
	if err != nil {
		return "", err
	}
	defer body.Close()
	scanner := bufio.NewScanner(io.LimitReader(body, 1<<20))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 2 && fields[1] == filename {
			return fields[0], nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("no entry for %s in checksums.txt", filename)
}

func extractMDPBinary(r io.Reader, tmp, dest string) error {
	// Preserve the running binary's mode bits across the swap; setuid/
	// setgid/sticky/group bits would otherwise vanish.
	mode := os.FileMode(0o755)
	if fi, err := os.Stat(dest); err == nil {
		mode = fi.Mode().Perm()
	}
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return fmt.Errorf("mdp binary not found in tarball")
		}
		if err != nil {
			return fmt.Errorf("tar: %w", err)
		}
		// Reject non-regular entries with name "mdp"; a symlink/dir entry
		// would otherwise yield a zero-byte successor binary on swap.
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		if filepath.Base(hdr.Name) != "mdp" {
			continue
		}
		return writeBinaryTo(tr, tmp, mode)
	}
}

// O_NOFOLLOW refuses to write through a foreign-planted symlink at tmp
// (same defense as writeTmpFile in main.go). LimitReader caps a hostile
// gzip's decompressed output.
func writeBinaryTo(r io.Reader, tmp string, mode os.FileMode) error {
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC|syscall.O_NOFOLLOW, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, io.LimitReader(r, updateMaxTarballBytes)); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// Probes writability by creating-then-removing a tempfile, so we can
// surface a permission error before shelling out to `go install` (whose
// exit-status error won't match os.ErrPermission via errors.Is).
func checkDirWritable(dir string) error {
	f, err := os.CreateTemp(dir, ".mdp-write-probe-*")
	if err != nil {
		return err
	}
	name := f.Name()
	f.Close()
	os.Remove(name)
	return nil
}

// Finite timeout (so a hung TCP connection can't wedge update forever)
// plus an allowlist on redirect hosts: GitHub releases redirect through
// *.githubusercontent.com, anything else is rejected.
var updateHTTPClient = &http.Client{
	Timeout: updateHTTPTimeout,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		if !updateAllowedHost(req.URL.Hostname()) {
			return fmt.Errorf("redirect to disallowed host %q", req.URL.Hostname())
		}
		return nil
	},
}

func updateAllowedHost(host string) bool {
	if host == "github.com" || host == "api.github.com" {
		return true
	}
	return strings.HasSuffix(host, ".githubusercontent.com")
}

func httpGet(url string) (io.ReadCloser, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "mdp-updater")
	resp, err := updateHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		resp.Body.Close()
		return nil, fmt.Errorf("HTTP %s for %s", resp.Status, url)
	}
	return resp.Body, nil
}

func runCmdInherit(name string, args, environ []string) error {
	cmd := exec.Command(name, args...)
	if len(environ) > 0 {
		cmd.Env = environ
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
