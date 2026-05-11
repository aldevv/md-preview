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
	"path/filepath"
)

//go:embed skill_ref.md
var skillRef []byte

const skillUsage = `Usage: mdp skill <command>

Commands:
  path    Print the path to a tempfile containing the bundled mdp skill
          reference. Skills load it via ` + "`cat \"$(mdp skill path)\"`" + ` to
          pick up canonical invocation modes, tempfile conventions, and
          spawn semantics for the installed binary.
`

// runSkill handles `mdp skill <subcmd>`. Currently only `path` is supported.
// `path` extracts the embedded reference to a stable tempfile and prints
// its absolute path. Re-runs overwrite the same file.
func runSkill(args []string, stdout, stderr io.Writer, env Environment) int {
	if len(args) == 0 {
		fmt.Fprint(stdout, skillUsage)
		return 0
	}
	switch args[0] {
	case "help", "-h", "--help":
		fmt.Fprint(stdout, skillUsage)
		return 0
	case "path":
		dest := filepath.Join(env.TempDir(), "mdp-skill.md")
		if err := writeTmpFile(dest, skillRef); err != nil {
			fmt.Fprintf(stderr, "mdp skill: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, dest)
		return 0
	default:
		fmt.Fprintf(stderr, "mdp skill: unknown command %q; see `mdp skill help`\n", args[0])
		return 1
	}
}
