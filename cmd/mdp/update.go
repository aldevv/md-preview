package main

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
)

const (
	updateAPI           = "https://api.github.com/repos/aldevv/md-preview/releases/latest"
	updateTarballURLFmt = "https://github.com/aldevv/md-preview/releases/download/%s/mdp_%s_%s.tar.gz"
	updateGoModule      = "github.com/aldevv/md-preview/cmd/mdp"
)

func runUpdate(args []string, stdout, stderr io.Writer, env Environment) int {
	fs := flag.NewFlagSet("mdp update", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stdout, "Usage: mdp update [--check]")
	}
	checkLong := fs.Bool("check", false, "")
	checkShort := fs.Bool("c", false, "")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 1
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "mdp update: unexpected arg %q\n", fs.Arg(0))
		return 1
	}
	checkOnly := *checkLong || *checkShort

	latest, err := fetchLatestTag(env)
	if err != nil {
		fmt.Fprintf(stderr, "mdp update: resolving latest release: %v\n", err)
		return 1
	}
	current := buildVersion()
	if current == latest {
		fmt.Fprintf(stdout, "mdp update: already at %s\n", current)
		return 0
	}
	if checkOnly {
		fmt.Fprintf(stdout, "mdp update: %s -> %s available\n", current, latest)
		return 0
	}

	dest, err := env.Executable()
	if err != nil {
		fmt.Fprintf(stderr, "mdp update: resolving executable path: %v\n", err)
		return 1
	}
	if resolved, err := filepath.EvalSymlinks(dest); err == nil {
		dest = resolved
	}

	if _, err := env.LookPath("go"); err == nil {
		fmt.Fprintf(stdout, "mdp update: %s -> %s (go install)\n", current, latest)
		gobin := filepath.Dir(dest)
		environ := append(os.Environ(), "GOBIN="+gobin)
		if err := env.RunCmd("go", []string{"install", updateGoModule + "@" + latest}, environ); err != nil {
			fmt.Fprintf(stderr, "mdp update: go install failed: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "mdp update: installed %s (%s)\n", latest, dest)
		return 0
	}

	fmt.Fprintf(stdout, "mdp update: %s -> %s (release tarball)\n", current, latest)
	if err := downloadAndReplace(env, latest, dest); err != nil {
		fmt.Fprintf(stderr, "mdp update: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "mdp update: installed %s (%s)\n", latest, dest)
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
		return "", fmt.Errorf("no tag_name in response")
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

	url := fmt.Sprintf(updateTarballURLFmt, tag, osTag, arch)
	body, err := env.HTTPGet(url)
	if err != nil {
		return fmt.Errorf("downloading %s: %w", url, err)
	}
	defer body.Close()

	gz, err := gzip.NewReader(body)
	if err != nil {
		return fmt.Errorf("gunzip: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return fmt.Errorf("mdp binary not found in tarball")
		}
		if err != nil {
			return fmt.Errorf("tar: %w", err)
		}
		if filepath.Base(hdr.Name) != "mdp" {
			continue
		}
		return writeAndSwap(tr, dest)
	}
}

// Atomic swap via sibling tempfile + rename. Linux/macOS allow rename
// over a running binary; the open fd keeps the live process intact.
func writeAndSwap(r io.Reader, dest string) error {
	tmp := dest + ".new"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, r); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dest); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

func httpGet(url string) (io.ReadCloser, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "mdp-updater")
	resp, err := http.DefaultClient.Do(req)
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
