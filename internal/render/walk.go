package render

import (
	"io/fs"
	"path/filepath"
	"sort"
)

const WalkableFilesMax = 2000

// noiseDirs are skipped during the tree walk. Names are checked at any
// depth (e.g. nested `node_modules`).
var noiseDirs = map[string]bool{
	".git":          true,
	".hg":           true,
	".svn":          true,
	"node_modules":  true,
	"vendor":        true,
	"venv":          true,
	".venv":         true,
	"__pycache__":   true,
	".idea":         true,
	".vscode":       true,
	"dist":          true,
	"build":         true,
	".next":         true,
	".nuxt":         true,
	"target":        true,
	".cache":        true,
	"coverage":      true,
	".nyc_output":   true,
	".tox":          true,
	".pytest_cache": true,
	".mypy_cache":   true,
	".ruff_cache":   true,
}

// WalkableFiles returns sorted forward-slash relative paths rooted at
// root for every file IsWalkableExt accepts, skipping the noise dirs
// above. The result is capped at maxFiles (defaults to WalkableFilesMax
// when <= 0); the cap is silent (the tree UI just shows what fit).
func WalkableFiles(root string, maxFiles int) ([]string, error) {
	if maxFiles <= 0 {
		maxFiles = WalkableFilesMax
	}
	var out []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if path != root && noiseDirs[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		if !IsWalkableExt(path) {
			return nil
		}
		if len(out) >= maxFiles {
			return fs.SkipAll
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}
