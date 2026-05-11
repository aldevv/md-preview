// mdp skill subcommand: exposes a bundled markdown reference for
// automation (Claude Code skills, scripts) that drive `mdp`. The reference
// content is versioned with the binary, so callers can `cat "$(mdp skill
// path)"` to load the canonical invocation guide without hard-coding it
// in skill prose.
package main

import (
	_ "embed"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

//go:embed skill_ref.md
var skillRef []byte

// runSkill handles `mdp skill <subcmd>`. Currently only `path` is supported.
// `path` extracts the embedded reference to a stable tempfile and prints
// its absolute path. Re-runs overwrite the same file.
func runSkill(args []string, stdout, stderr io.Writer, env Environment) int {
	if len(args) == 0 || args[0] != "path" {
		fmt.Fprintln(stderr, "Usage: mdp skill path")
		return 1
	}
	dest := filepath.Join(env.TempDir(), "mdp-skill.md")
	if err := os.WriteFile(dest, skillRef, 0o600); err != nil {
		fmt.Fprintf(stderr, "mdp skill: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, dest)
	return 0
}
