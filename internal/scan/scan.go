// Package scan discovers the source files of a corpus.
//
// Walking is deterministic: the same tree and the same configuration always
// produce the same files in the same ascending Rel order. Everything that is
// not an ordinary, readable, non-symlinked file is skipped rather than
// reported as an error, so a single unreadable file never fails an index run.
package scan

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"sift/internal/core"
)

// gitDir is the version control directory a walk never descends into.
const gitDir = ".git"

// Walk returns every file of the corpus that Match accepts, sorted by Rel in
// ascending order.
//
// The walk never follows symlinks, never descends into ".git" or into the
// configured output directory, and skips entries it cannot read: those are
// omitted from the result instead of failing the walk. A missing root wraps
// core.ErrNotFound, a root that is not a directory and an unusable glob
// pattern both return a *core.ConfigError.
func Walk(cfg core.Config) ([]core.FileRef, error) {
	if err := checkPatterns(cfg); err != nil {
		return nil, err
	}
	root := cfg.Root
	if root == "" {
		root = "."
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("scan: root %s: %w", root, err)
	}
	info, err := os.Stat(absRoot)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("scan: root %s: %w", root, core.ErrNotFound)
		}
		return nil, fmt.Errorf("scan: root %s: %w", root, err)
	}
	if !info.IsDir() {
		return nil, &core.ConfigError{Field: "root", Reason: "not a directory: " + root}
	}
	outAbs := outputAbs(cfg, absRoot)

	var refs []core.FileRef
	walkErr := filepath.WalkDir(absRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == absRoot {
				return err
			}
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			// An unreadable entry is skipped, not fatal.
			return nil
		}
		if p == absRoot {
			return nil
		}
		if d.IsDir() {
			if d.Name() == gitDir || samePath(p, outAbs) {
				return fs.SkipDir
			}
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 || !d.Type().IsRegular() {
			return nil
		}
		rel, relErr := filepath.Rel(absRoot, p)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		fi, infoErr := d.Info()
		if infoErr != nil {
			return nil
		}
		if !Match(cfg, rel, fi.Size()) {
			return nil
		}
		if !readable(p) {
			return nil
		}
		refs = append(refs, core.FileRef{
			Rel:     rel,
			Abs:     p,
			Size:    fi.Size(),
			ModTime: fi.ModTime(),
			Ext:     strings.ToLower(path.Ext(rel)),
		})
		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("scan: walk %s: %w", root, walkErr)
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].Rel < refs[j].Rel })
	return refs, nil
}

// Match reports whether the corpus-relative path rel, whose file is size
// bytes long, belongs in the index.
//
// A path is rejected when it escapes the corpus, has a ".git" component, lies
// inside the output directory, exceeds Config.MaxFileBytes, fails to match any
// Include pattern, or matches an Exclude pattern. Patterns are matched with
// path.Match against the whole slash-separated path and against the base name,
// so "*.md" selects Markdown files at any depth. Include takes effect only
// when it is non-empty; Exclude always wins over Include. Patterns that do not
// compile never match.
func Match(cfg core.Config, rel string, size int64) bool {
	rel = filepath.ToSlash(rel)
	if rel == "" {
		return false
	}
	rel = strings.TrimPrefix(path.Clean(rel), "./")
	if rel == "." || rel == ".." || path.IsAbs(rel) || strings.HasPrefix(rel, "../") {
		return false
	}
	if hasComponent(rel, gitDir) {
		return false
	}
	if out := outputRel(cfg); out != "" && (rel == out || strings.HasPrefix(rel, out+"/")) {
		return false
	}
	if cfg.MaxFileBytes > 0 && size > cfg.MaxFileBytes {
		return false
	}
	if len(cfg.Include) > 0 && !matchAny(cfg.Include, rel) {
		return false
	}
	return !matchAny(cfg.Exclude, rel)
}

// matchAny reports whether any pattern matches the whole path or its base
// name. Invalid patterns are ignored so Match stays a total function.
func matchAny(patterns []string, rel string) bool {
	base := path.Base(rel)
	for _, pat := range patterns {
		if ok, err := path.Match(pat, rel); err == nil && ok {
			return true
		}
		if ok, err := path.Match(pat, base); err == nil && ok {
			return true
		}
	}
	return false
}

// checkPatterns rejects glob patterns that path.Match cannot compile.
func checkPatterns(cfg core.Config) error {
	for _, group := range []struct {
		field    string
		patterns []string
	}{
		{"include", cfg.Include},
		{"exclude", cfg.Exclude},
	} {
		for _, pat := range group.patterns {
			if !validPattern(pat) {
				return &core.ConfigError{Field: group.field, Reason: "invalid glob pattern: " + pat}
			}
		}
	}
	return nil
}

// validPattern reports whether path.Match accepts pat.
func validPattern(pat string) bool {
	for _, probe := range []string{"a", "a/b"} {
		if _, err := path.Match(pat, probe); err != nil {
			return false
		}
	}
	return true
}

// hasComponent reports whether the slash path rel contains the given path
// component.
func hasComponent(rel, name string) bool {
	for _, part := range strings.Split(rel, "/") {
		if part == name {
			return true
		}
	}
	return false
}

// outputRel returns the output directory as a corpus-relative slash path, or
// "" when it is absolute and therefore not expressible relative to the root.
func outputRel(cfg core.Config) string {
	dir := cfg.OutputDir
	if dir == "" {
		dir = core.DefaultOutputDir
	}
	if filepath.IsAbs(dir) {
		return ""
	}
	out := path.Clean(filepath.ToSlash(dir))
	if out == "." || out == ".." || strings.HasPrefix(out, "../") {
		return ""
	}
	return out
}

// outputAbs returns the absolute output directory for the corpus.
func outputAbs(cfg core.Config, absRoot string) string {
	dir := cfg.OutputDir
	if dir == "" {
		dir = core.DefaultOutputDir
	}
	if filepath.IsAbs(dir) {
		return filepath.Clean(dir)
	}
	return filepath.Join(absRoot, filepath.FromSlash(dir))
}

// samePath compares two cleaned absolute paths, ignoring case where the
// platform does.
func samePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if a == b {
		return true
	}
	if filepath.Separator != '/' {
		return strings.EqualFold(a, b)
	}
	return false
}

// readable reports whether the file can be opened for reading.
func readable(p string) bool {
	f, err := os.Open(p)
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}
