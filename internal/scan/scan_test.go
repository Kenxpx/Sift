package scan

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sift/internal/core"
)

// writeTree creates files under root; keys are slash paths, values contents.
func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, body := range files {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", abs, err)
		}
		if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", abs, err)
		}
	}
}

// rels returns the Rel field of every ref.
func rels(refs []core.FileRef) []string {
	out := make([]string, 0, len(refs))
	for _, r := range refs {
		out = append(out, r.Rel)
	}
	return out
}

// sample is the tree most tests walk.
var sample = map[string]string{
	"README.md":          "# Readme\n",
	"docs/guide.md":      "guide\n",
	"docs/notes.txt":     "notes\n",
	"docs/deep/inner.md": "inner\n",
	"src/main.go":        "package main\n",
	"src/util.go":        "package main\n",
	"src/vendor/dep.go":  "package dep\n",
	"Makefile":           "all:\n",
	".git/config":        "[core]\n",
	".git/objects/ab/cd": "blob\n",
	"sift-out/seg.json":  "{}\n",
}

func TestWalkOrderAndContents(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, sample)
	cfg := core.Config{Root: root, OutputDir: core.DefaultOutputDir}

	refs, err := Walk(cfg)
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	want := []string{
		"Makefile",
		"README.md",
		"docs/deep/inner.md",
		"docs/guide.md",
		"docs/notes.txt",
		"src/main.go",
		"src/util.go",
		"src/vendor/dep.go",
	}
	got := rels(refs)
	if len(got) != len(want) {
		t.Fatalf("got %d files %v, want %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("file %d = %q, want %q", i, got[i], want[i])
		}
	}
	again, err := Walk(cfg)
	if err != nil {
		t.Fatalf("Walk again: %v", err)
	}
	for i, r := range again {
		if r.Rel != refs[i].Rel {
			t.Errorf("second walk differs at %d: %q vs %q", i, r.Rel, refs[i].Rel)
		}
	}
}

func TestWalkFileRefFields(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{"docs/Guide.MD": "hello world"})
	refs, err := Walk(core.Config{Root: root})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("got %d refs, want 1", len(refs))
	}
	ref := refs[0]
	if ref.Rel != "docs/Guide.MD" {
		t.Errorf("Rel = %q, want docs/Guide.MD", ref.Rel)
	}
	if ref.Ext != ".md" {
		t.Errorf("Ext = %q, want .md (lower-cased)", ref.Ext)
	}
	if ref.Size != 11 {
		t.Errorf("Size = %d, want 11", ref.Size)
	}
	if !filepath.IsAbs(ref.Abs) {
		t.Errorf("Abs = %q, want an absolute path", ref.Abs)
	}
	if filepath.Base(ref.Abs) != "Guide.MD" {
		t.Errorf("Abs base = %q, want Guide.MD", filepath.Base(ref.Abs))
	}
	if ref.ModTime.IsZero() {
		t.Error("ModTime is zero")
	}
}

func TestWalkSkipsGitAndOutputDir(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"a.md":                  "a\n",
		".git/config":           "x\n",
		"src/.git/hooks/pre":    "x\n",
		"build/manifest.json":   "{}\n",
		"build/nested/seg.json": "{}\n",
		"buildings/keep.md":     "keep\n",
		"sift-out/stray.json":   "{}\n",
	})
	refs, err := Walk(core.Config{Root: root, OutputDir: "build"})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	want := []string{"a.md", "buildings/keep.md", "sift-out/stray.json"}
	got := rels(refs)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestWalkIncludeExcludeAndSize(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, sample)
	cases := []struct {
		name string
		cfg  core.Config
		want []string
	}{
		{
			name: "include markdown by base name",
			cfg:  core.Config{Include: []string{"*.md"}},
			want: []string{"README.md", "docs/deep/inner.md", "docs/guide.md"},
		},
		{
			name: "include by full slash path",
			cfg:  core.Config{Include: []string{"src/*.go"}},
			want: []string{"src/main.go", "src/util.go"},
		},
		{
			name: "exclude beats include",
			cfg:  core.Config{Include: []string{"*.go"}, Exclude: []string{"src/vendor/*"}},
			want: []string{"src/main.go", "src/util.go"},
		},
		{
			name: "several include patterns",
			cfg:  core.Config{Include: []string{"*.txt", "Makefile"}},
			want: []string{"Makefile", "docs/notes.txt"},
		},
		{
			name: "exclude only",
			cfg:  core.Config{Exclude: []string{"*.go", "*.txt", "Makefile"}},
			want: []string{"README.md", "docs/deep/inner.md", "docs/guide.md"},
		},
		{
			name: "max file bytes drops the larger files",
			cfg:  core.Config{Include: []string{"*.go"}, MaxFileBytes: 12},
			want: []string{"src/vendor/dep.go"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := tc.cfg
			cfg.Root = root
			refs, err := Walk(cfg)
			if err != nil {
				t.Fatalf("Walk: %v", err)
			}
			got := rels(refs)
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestWalkRootErrors(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope")
	if _, err := Walk(core.Config{Root: missing}); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("missing root err = %v, want core.ErrNotFound", err)
	}
	file := filepath.Join(t.TempDir(), "a.md")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := Walk(core.Config{Root: file})
	if !errors.Is(err, core.ErrConfig) {
		t.Fatalf("file root err = %v, want core.ErrConfig", err)
	}
	var cfgErr *core.ConfigError
	if !errors.As(err, &cfgErr) || cfgErr.Field != "root" {
		t.Fatalf("err = %v, want *core.ConfigError on field root", err)
	}
}

func TestWalkRejectsBadPattern(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{"a.md": "a\n"})
	cases := []struct {
		name  string
		cfg   core.Config
		field string
	}{
		{"include", core.Config{Root: root, Include: []string{"[bad"}}, "include"},
		{"exclude", core.Config{Root: root, Exclude: []string{"a[b"}}, "exclude"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Walk(tc.cfg)
			if !errors.Is(err, core.ErrConfig) {
				t.Fatalf("err = %v, want core.ErrConfig", err)
			}
			var cfgErr *core.ConfigError
			if !errors.As(err, &cfgErr) {
				t.Fatalf("err = %v, want *core.ConfigError", err)
			}
			if cfgErr.Field != tc.field {
				t.Errorf("Field = %q, want %q", cfgErr.Field, tc.field)
			}
		})
	}
}

func TestWalkSkipsSymlinksAndDirectories(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{"real.md": "real\n", "sub/inner.md": "inner\n"})
	if err := os.Symlink(filepath.Join(root, "real.md"), filepath.Join(root, "link.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink(filepath.Join(root, "sub"), filepath.Join(root, "subline")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	refs, err := Walk(core.Config{Root: root})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	want := []string{"real.md", "sub/inner.md"}
	got := rels(refs)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestMatch(t *testing.T) {
	cases := []struct {
		name string
		cfg  core.Config
		rel  string
		size int64
		want bool
	}{
		{"plain file", core.Config{}, "docs/a.md", 10, true},
		{"git component", core.Config{}, ".git/config", 10, false},
		{"nested git component", core.Config{}, "src/.git/hooks/pre", 10, false},
		{"default output dir", core.Config{}, "sift-out/manifest.json", 10, false},
		{"configured output dir", core.Config{OutputDir: "build"}, "build/x/seg.json", 10, false},
		{"output dir prefix is not a match", core.Config{OutputDir: "build"}, "buildings/a.md", 10, true},
		{"escaping path", core.Config{}, "../outside.md", 10, false},
		{"empty path", core.Config{}, "", 10, false},
		{"dot path", core.Config{}, ".", 10, false},
		{"size over limit", core.Config{MaxFileBytes: 5}, "a.md", 6, false},
		{"size at limit", core.Config{MaxFileBytes: 5}, "a.md", 5, true},
		{"zero limit means unlimited", core.Config{MaxFileBytes: 0}, "a.md", 1 << 40, true},
		{"include miss", core.Config{Include: []string{"*.go"}}, "a.md", 1, false},
		{"include base name hit", core.Config{Include: []string{"*.go"}}, "src/deep/a.go", 1, true},
		{"include full path hit", core.Config{Include: []string{"src/*.go"}}, "src/a.go", 1, true},
		{"include full path miss on depth", core.Config{Include: []string{"src/*"}}, "src/deep/a.go", 1, false},
		{"exclude wins", core.Config{Include: []string{"*.md"}, Exclude: []string{"a.md"}}, "a.md", 1, false},
		{"bad pattern never matches", core.Config{Exclude: []string{"[bad"}}, "a.md", 1, true},
		{"windows separators normalized", core.Config{}, `docs\a.md`, 1, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Match(tc.cfg, tc.rel, tc.size); got != tc.want {
				t.Fatalf("Match(%q, %d) = %v, want %v", tc.rel, tc.size, got, tc.want)
			}
		})
	}
}

func TestWalkMatchesMatchPredicate(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, sample)
	cfg := core.Config{Root: root, Include: []string{"*.md", "*.go"}, Exclude: []string{"src/vendor/*"}}
	refs, err := Walk(cfg)
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if len(refs) != 5 {
		t.Fatalf("got %d refs %v, want 5", len(refs), rels(refs))
	}
	for _, r := range refs {
		if !Match(cfg, r.Rel, r.Size) {
			t.Errorf("Walk returned %q which Match rejects", r.Rel)
		}
	}
	for rel := range sample {
		if Match(cfg, rel, 32) {
			found := false
			for _, r := range refs {
				if r.Rel == rel {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("Match accepts %q but Walk omitted it", rel)
			}
		}
	}
}

func TestWalkEmptyRootAndDirsOnly(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "a", "b"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	refs, err := Walk(core.Config{Root: root})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if len(refs) != 0 {
		t.Fatalf("got %v, want no files", rels(refs))
	}
}
