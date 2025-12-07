package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"sift/internal/core"
)

// write puts a .sift.json holding body in a fresh corpus root.
func write(t *testing.T, body string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, FileName), []byte(body), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return root
}

func TestDefault(t *testing.T) {
	cfg := Default("/corpus")
	if cfg.Root != "/corpus" {
		t.Errorf("Root = %q, want %q", cfg.Root, "/corpus")
	}
	if cfg.OutputDir != core.DefaultOutputDir {
		t.Errorf("OutputDir = %q, want %q", cfg.OutputDir, core.DefaultOutputDir)
	}
	if cfg.MinTermLength != 2 {
		t.Errorf("MinTermLength = %d, want 2", cfg.MinTermLength)
	}
	if cfg.SegmentDocs != 256 {
		t.Errorf("SegmentDocs = %d, want 256", cfg.SegmentDocs)
	}
	if cfg.MaxFileBytes != 1<<20 {
		t.Errorf("MaxFileBytes = %d, want %d", cfg.MaxFileBytes, 1<<20)
	}
	if len(cfg.Include) != 0 || len(cfg.Exclude) != 0 || len(cfg.Stopwords) != 0 {
		t.Errorf("lists must default to empty, got %+v", cfg)
	}
	if err := Validate(cfg); err != nil {
		t.Errorf("Validate(Default) = %v, want nil", err)
	}
}

func TestLoadWithoutFile(t *testing.T) {
	root := t.TempDir()
	cfg, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := Default(root)
	if cfg.OutputDir != want.OutputDir || cfg.SegmentDocs != want.SegmentDocs ||
		cfg.MinTermLength != want.MinTermLength || cfg.MaxFileBytes != want.MaxFileBytes {
		t.Errorf("Load = %+v, want %+v", cfg, want)
	}
	if cfg.Root != root {
		t.Errorf("Root = %q, want %q", cfg.Root, root)
	}
}

func TestLoadOverlaysOnlyPresentKeys(t *testing.T) {
	body := `{
  "output_dir": "index",
  "include": ["*.md", "*.go"],
  "min_term_length": 3
}`
	root := write(t, body)
	cfg, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.OutputDir != "index" {
		t.Errorf("OutputDir = %q, want %q", cfg.OutputDir, "index")
	}
	if len(cfg.Include) != 2 || cfg.Include[0] != "*.md" || cfg.Include[1] != "*.go" {
		t.Errorf("Include = %v, want [*.md *.go]", cfg.Include)
	}
	if cfg.MinTermLength != 3 {
		t.Errorf("MinTermLength = %d, want 3", cfg.MinTermLength)
	}
	// Untouched keys keep their defaults.
	if cfg.SegmentDocs != DefaultSegmentDocs {
		t.Errorf("SegmentDocs = %d, want %d", cfg.SegmentDocs, DefaultSegmentDocs)
	}
	if cfg.MaxFileBytes != DefaultMaxFileBytes {
		t.Errorf("MaxFileBytes = %d, want %d", cfg.MaxFileBytes, DefaultMaxFileBytes)
	}
	if cfg.Root != root {
		t.Errorf("Root = %q, want %q", cfg.Root, root)
	}
}

func TestLoadKeySpellingsAndNulls(t *testing.T) {
	tests := []struct {
		name string
		body string
		want int
	}{
		{"snake", `{"segment_docs": 8}`, 8},
		{"camel", `{"segmentDocs": 9}`, 9},
		{"pascal", `{"SegmentDocs": 10}`, 10},
		{"kebab", `{"segment-docs": 11}`, 11},
		{"null keeps default", `{"segment_docs": null}`, DefaultSegmentDocs},
		{"empty object", `{}`, DefaultSegmentDocs},
		{"blank file", "   \n", DefaultSegmentDocs},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Load(write(t, tt.body))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.SegmentDocs != tt.want {
				t.Errorf("SegmentDocs = %d, want %d", cfg.SegmentDocs, tt.want)
			}
		})
	}
}

func TestLoadRejects(t *testing.T) {
	tests := []struct {
		name  string
		body  string
		field string
	}{
		{"unknown setting", `{"segment_size": 4}`, "segment_size"},
		{"malformed json", `{"segment_docs":`, FileName},
		{"not an object", `["a"]`, FileName},
		{"wrong type for string", `{"output_dir": 4}`, "output_dir"},
		{"wrong type for list", `{"include": "*.md"}`, "include"},
		{"wrong type for number", `{"min_term_length": "two"}`, "min_term_length"},
		{"fractional number", `{"segment_docs": 2.5}`, "segment_docs"},
		{"invalid value", `{"min_term_length": 0}`, "MinTermLength"},
		{"absolute output dir", `{"output_dir": "/var/index"}`, "OutputDir"},
		{"escaping output dir", `{"output_dir": "../index"}`, "OutputDir"},
		{"bad glob", `{"exclude": ["["]}`, "Exclude[0]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(write(t, tt.body))
			if err == nil {
				t.Fatal("got nil error")
			}
			if !errors.Is(err, core.ErrConfig) {
				t.Fatalf("error %v does not match core.ErrConfig", err)
			}
			var cerr *core.ConfigError
			if !errors.As(err, &cerr) {
				t.Fatalf("error %v is not a *core.ConfigError", err)
			}
			if cerr.Field != tt.field {
				t.Errorf("Field = %q, want %q", cerr.Field, tt.field)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	base := Default("/corpus")
	tests := []struct {
		name   string
		mutate func(*core.Config)
		field  string
	}{
		{"ok", func(c *core.Config) {}, ""},
		{"empty output dir", func(c *core.Config) { c.OutputDir = "" }, "OutputDir"},
		{"blank output dir", func(c *core.Config) { c.OutputDir = "  " }, "OutputDir"},
		{"windows absolute", func(c *core.Config) { c.OutputDir = `C:\out` }, "OutputDir"},
		{"unix absolute", func(c *core.Config) { c.OutputDir = "/out" }, "OutputDir"},
		{"root itself", func(c *core.Config) { c.OutputDir = "." }, "OutputDir"},
		{"escape", func(c *core.Config) { c.OutputDir = "a/../.." }, "OutputDir"},
		{"nested is fine", func(c *core.Config) { c.OutputDir = "build/index" }, ""},
		{"min term length zero", func(c *core.Config) { c.MinTermLength = 0 }, "MinTermLength"},
		{"negative min term length", func(c *core.Config) { c.MinTermLength = -1 }, "MinTermLength"},
		{"segment docs zero", func(c *core.Config) { c.SegmentDocs = 0 }, "SegmentDocs"},
		{"negative max bytes", func(c *core.Config) { c.MaxFileBytes = -1 }, "MaxFileBytes"},
		{"zero max bytes means unlimited", func(c *core.Config) { c.MaxFileBytes = 0 }, ""},
		{"bad include", func(c *core.Config) { c.Include = []string{"*.md", "[a-"} }, "Include[1]"},
		{"empty exclude", func(c *core.Config) { c.Exclude = []string{""} }, "Exclude[0]"},
		{"empty root is fine", func(c *core.Config) { c.Root = "" }, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := base
			tt.mutate(&cfg)
			err := Validate(cfg)
			if tt.field == "" {
				if err != nil {
					t.Fatalf("Validate = %v, want nil", err)
				}
				return
			}
			var cerr *core.ConfigError
			if !errors.As(err, &cerr) {
				t.Fatalf("Validate = %v, want a *core.ConfigError", err)
			}
			if cerr.Field != tt.field {
				t.Errorf("Field = %q, want %q", cerr.Field, tt.field)
			}
			if !errors.Is(err, core.ErrConfig) {
				t.Errorf("error %v does not match core.ErrConfig", err)
			}
		})
	}
}

func TestHashIgnoresRootAndListOrder(t *testing.T) {
	a := Default("/one")
	a.Include = []string{"*.md", "*.go"}
	a.Stopwords = []string{"the", "and"}

	b := Default("/two/elsewhere")
	b.Include = []string{"*.go", "*.md"}
	b.Stopwords = []string{"and", "the"}

	ha, hb := Hash(a), Hash(b)
	if ha != hb {
		t.Errorf("hash depends on root or list order:\n%s\n%s", ha, hb)
	}
	if len(ha) != 64 {
		t.Errorf("hash length = %d, want 64 hex characters", len(ha))
	}
	for _, r := range ha {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			t.Fatalf("hash %q is not lower-case hex", ha)
		}
	}
	// Hashing must not reorder the caller's slices.
	if a.Include[0] != "*.md" || a.Stopwords[0] != "the" {
		t.Errorf("Hash mutated its input: %v %v", a.Include, a.Stopwords)
	}
}

func TestHashDistinguishesSettings(t *testing.T) {
	base := Default("/corpus")
	seen := map[string]string{Hash(base): "base"}

	variants := []struct {
		name   string
		mutate func(*core.Config)
	}{
		{"output dir", func(c *core.Config) { c.OutputDir = "index" }},
		{"include", func(c *core.Config) { c.Include = []string{"*.md"} }},
		{"exclude", func(c *core.Config) { c.Exclude = []string{"*.md"} }},
		{"max file bytes", func(c *core.Config) { c.MaxFileBytes = 2048 }},
		{"stopwords", func(c *core.Config) { c.Stopwords = []string{"the"} }},
		{"min term length", func(c *core.Config) { c.MinTermLength = 3 }},
		{"segment docs", func(c *core.Config) { c.SegmentDocs = 64 }},
		// Length prefixes must keep neighbouring values from running together.
		{"split stopwords", func(c *core.Config) { c.Stopwords = []string{"ab", "c"} }},
		{"joined stopwords", func(c *core.Config) { c.Stopwords = []string{"a", "bc"} }},
	}
	for _, v := range variants {
		cfg := base
		v.mutate(&cfg)
		h := Hash(cfg)
		if prev, ok := seen[h]; ok {
			t.Errorf("%q hashes the same as %q", v.name, prev)
			continue
		}
		seen[h] = v.name
	}

	// The same settings always hash the same, and an equivalent spelling of
	// the output directory does too.
	again := base
	again.OutputDir = core.DefaultOutputDir + "/"
	if Hash(again) != Hash(base) {
		t.Errorf("trailing separator changed the hash")
	}
}

func TestOutputPath(t *testing.T) {
	cfg := Default(filepath.Join("corpus", "docs"))
	want := filepath.Join("corpus", "docs", core.DefaultOutputDir)
	if got := OutputPath(cfg); got != want {
		t.Errorf("OutputPath = %q, want %q", got, want)
	}

	cfg.OutputDir = filepath.Join("build", "index")
	want = filepath.Join("corpus", "docs", "build", "index")
	if got := OutputPath(cfg); got != want {
		t.Errorf("OutputPath = %q, want %q", got, want)
	}
}
