package app

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"sift/internal/cache"
	"sift/internal/config"
	"sift/internal/core"
	"sift/internal/publish"
)

// fixedTime stamps every manifest a test publishes, so two runs over the same
// corpus produce identical bytes.
var fixedTime = time.Date(2024, 3, 4, 5, 6, 7, 0, time.UTC)

// testApp returns an App whose manifests are reproducible.
func testApp() App {
	return App{Clock: core.FixedClock{T: fixedTime}}
}

// corpus writes files under a fresh directory and returns its path. Keys are
// slash-separated paths relative to the corpus root.
func corpus(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, body := range files {
		write(t, filepath.Join(root, filepath.FromSlash(rel)), body)
	}
	return root
}

// write creates one file and every directory above it.
func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// sample is the corpus most tests index: two Markdown files, one source file
// and one text file.
func sample() map[string]string {
	return map[string]string{
		"docs/alpha.md": "# Alpha Guide\n\nalpha beta gamma alpha\n",
		"docs/beta.md":  "# Beta Notes\n\nbeta gamma\n",
		"src/main.go":   "package main\n\nfunc main() { println(\"alpha\") }\n",
		"readme.txt":    "plain text alpha\n",
	}
}

func TestIndexPublishesGeneration(t *testing.T) {
	root := corpus(t, sample())
	a := testApp()

	m, err := a.Index(root)
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	if m.Generation != 1 {
		t.Errorf("Generation = %d, want 1", m.Generation)
	}
	if m.DocCount != 4 {
		t.Errorf("DocCount = %d, want 4", m.DocCount)
	}
	if len(m.Segments) != 1 {
		t.Fatalf("Segments = %d, want 1", len(m.Segments))
	}
	if got, want := m.Segments[0].File, "gen-0001/seg-0001.json"; got != want {
		t.Errorf("segment file = %q, want %q", got, want)
	}
	if !m.CreatedAt.Equal(fixedTime) {
		t.Errorf("CreatedAt = %v, want %v", m.CreatedAt, fixedTime)
	}
	cfg, err := a.Config(root)
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	if m.ConfigHash != config.Hash(cfg) {
		t.Errorf("ConfigHash = %q, want %q", m.ConfigHash, config.Hash(cfg))
	}
	out := publish.OutputPath(cfg)
	for _, rel := range []string{core.ManifestFile, core.CacheFile, "gen-0001/seg-0001.json"} {
		if _, err := os.Stat(filepath.Join(out, filepath.FromSlash(rel))); err != nil {
			t.Errorf("missing published file %s: %v", rel, err)
		}
	}
}

func TestIndexSkipsBinaryFiles(t *testing.T) {
	files := sample()
	root := corpus(t, files)
	write(t, filepath.Join(root, "assets", "logo.bin"), "text\x00\x00binary")
	a := testApp()

	m, err := a.Index(root)
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	if m.DocCount != 4 {
		t.Fatalf("DocCount = %d, want 4 (the binary file must be skipped)", m.DocCount)
	}
	rep, err := a.Search(root, core.SearchOptions{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	for _, res := range rep.Results {
		if res.DocID == "assets/logo.bin" {
			t.Fatalf("binary file was indexed: %v", res.DocID)
		}
	}
}

func TestIndexReusesUnchangedDocumentsFromCache(t *testing.T) {
	root := corpus(t, sample())
	a := testApp()
	if _, err := a.Index(root); err != nil {
		t.Fatalf("first Index: %v", err)
	}
	// A cached document that could never be produced by extraction proves the
	// second run reused the cache instead of extracting the file again.
	poison(t, a, root, "docs/alpha.md", "CACHED TITLE")

	if _, err := a.Index(root); err != nil {
		t.Fatalf("second Index: %v", err)
	}
	if got := titleOf(t, a, root, "docs/alpha.md"); got != "CACHED TITLE" {
		t.Errorf("title = %q, want the cached title: the cache was not reused", got)
	}
}

func TestIndexDropsCacheWhenConfigurationChanges(t *testing.T) {
	root := corpus(t, sample())
	a := testApp()
	if _, err := a.Index(root); err != nil {
		t.Fatalf("first Index: %v", err)
	}
	poison(t, a, root, "docs/alpha.md", "CACHED TITLE")
	// Tokenization depends on the configuration, so a cache published under a
	// different configuration must not be reused.
	write(t, filepath.Join(root, config.FileName), "{\"min_term_length\": 4}\n")

	if _, err := a.Index(root); err != nil {
		t.Fatalf("second Index: %v", err)
	}
	if got := titleOf(t, a, root, "docs/alpha.md"); got != "Alpha Guide" {
		t.Errorf("title = %q, want %q: the stale cache was reused", got, "Alpha Guide")
	}
}

func TestIndexPrunesRemovedDocumentsFromCache(t *testing.T) {
	root := corpus(t, sample())
	a := testApp()
	if _, err := a.Index(root); err != nil {
		t.Fatalf("first Index: %v", err)
	}
	if err := os.Remove(filepath.Join(root, "docs", "beta.md")); err != nil {
		t.Fatalf("remove: %v", err)
	}

	m, err := a.Index(root)
	if err != nil {
		t.Fatalf("second Index: %v", err)
	}
	if m.DocCount != 3 {
		t.Errorf("DocCount = %d, want 3", m.DocCount)
	}
	entries := loadCacheFile(t, a, root)
	if len(entries.Entries) != 3 {
		t.Errorf("cache holds %d entries, want 3", len(entries.Entries))
	}
	if _, ok := entries.Entries["docs/beta.md"]; ok {
		t.Error("cache still holds the removed document")
	}
}

func TestIndexRepublishesAndPrunesOldGenerations(t *testing.T) {
	root := corpus(t, sample())
	a := testApp()
	if _, err := a.Index(root); err != nil {
		t.Fatalf("first Index: %v", err)
	}
	write(t, filepath.Join(root, "docs", "gamma.md"), "# Gamma\n\ngamma delta\n")

	m, err := a.Index(root)
	if err != nil {
		t.Fatalf("second Index: %v", err)
	}
	if m.Generation != 2 {
		t.Errorf("Generation = %d, want 2", m.Generation)
	}
	if m.DocCount != 5 {
		t.Errorf("DocCount = %d, want 5", m.DocCount)
	}
	cfg, err := a.Config(root)
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	out := publish.OutputPath(cfg)
	if _, err := os.Stat(filepath.Join(out, "gen-0001")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("gen-0001 still present after republish: %v", err)
	}
	if _, err := os.Stat(filepath.Join(out, "gen-0002")); err != nil {
		t.Errorf("gen-0002 missing: %v", err)
	}
}

func TestSearchOrdersAndCountsMatches(t *testing.T) {
	root := corpus(t, sample())
	a := testApp()
	if _, err := a.Index(root); err != nil {
		t.Fatalf("Index: %v", err)
	}

	cases := []struct {
		name  string
		opts  core.SearchOptions
		total int
		first core.DocID
	}{
		{name: "term", opts: core.SearchOptions{Query: "alpha"}, total: 3, first: "docs/alpha.md"},
		{name: "empty matches all", opts: core.SearchOptions{}, total: 4, first: "docs/alpha.md"},
		{name: "field clause", opts: core.SearchOptions{Query: "kind:markdown"}, total: 2, first: "docs/alpha.md"},
		{name: "negation", opts: core.SearchOptions{Query: "gamma -alpha"}, total: 1, first: "docs/beta.md"},
		{name: "filter", opts: core.SearchOptions{Query: "alpha", Filters: map[string]string{"kind": "text"}}, total: 1, first: "readme.txt"},
		{name: "limit keeps the head", opts: core.SearchOptions{Query: "alpha", Limit: 1}, total: 3, first: "docs/alpha.md"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rep, err := a.Search(root, tc.opts)
			if err != nil {
				t.Fatalf("Search: %v", err)
			}
			if rep.Total != tc.total {
				t.Errorf("Total = %d, want %d", rep.Total, tc.total)
			}
			if len(rep.Results) == 0 {
				t.Fatal("no results")
			}
			if rep.Results[0].DocID != tc.first {
				t.Errorf("first result = %q, want %q", rep.Results[0].DocID, tc.first)
			}
			if tc.opts.Limit > 0 && len(rep.Results) > tc.opts.Limit {
				t.Errorf("returned %d results, want at most %d", len(rep.Results), tc.opts.Limit)
			}
		})
	}
}

func TestSearchCountsFacetsOverEveryMatch(t *testing.T) {
	root := corpus(t, sample())
	a := testApp()
	if _, err := a.Index(root); err != nil {
		t.Fatalf("Index: %v", err)
	}

	rep, err := a.Search(root, core.SearchOptions{Query: "alpha", Limit: 1, Facets: []string{"kind"}})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(rep.Results) != 1 {
		t.Fatalf("Results = %d, want 1", len(rep.Results))
	}
	kind, ok := rep.Facets["kind"]
	if !ok {
		t.Fatal("no kind facet")
	}
	want := map[string]int{"markdown": 1, "source": 1, "text": 1}
	for value, count := range want {
		if kind.Counts[value] != count {
			t.Errorf("facet kind[%q] = %d, want %d", value, kind.Counts[value], count)
		}
	}
}

func TestSearchAndStatsReportMissingAndDamagedIndexes(t *testing.T) {
	a := testApp()

	t.Run("never indexed", func(t *testing.T) {
		root := corpus(t, sample())
		if _, err := a.Search(root, core.SearchOptions{}); !errors.Is(err, core.ErrNotFound) {
			t.Errorf("Search error = %v, want core.ErrNotFound", err)
		}
		if _, err := a.Stats(root); !errors.Is(err, core.ErrNotFound) {
			t.Errorf("Stats error = %v, want core.ErrNotFound", err)
		}
	})

	t.Run("damaged segment", func(t *testing.T) {
		root := corpus(t, sample())
		if _, err := a.Index(root); err != nil {
			t.Fatalf("Index: %v", err)
		}
		damage(t, a, root)
		_, err := a.Search(root, core.SearchOptions{Query: "alpha"})
		if !errors.Is(err, core.ErrIntegrity) {
			t.Fatalf("Search error = %v, want core.ErrIntegrity", err)
		}
		var damaged *core.IntegrityError
		if !errors.As(err, &damaged) {
			t.Fatalf("error is not a *core.IntegrityError: %v", err)
		}
		if damaged.Path != "gen-0001/seg-0001.json" {
			t.Errorf("Path = %q, want %q", damaged.Path, "gen-0001/seg-0001.json")
		}
	})

	t.Run("bad query", func(t *testing.T) {
		root := corpus(t, sample())
		if _, err := a.Index(root); err != nil {
			t.Fatalf("Index: %v", err)
		}
		if _, err := a.Search(root, core.SearchOptions{Query: "\"open"}); !errors.Is(err, core.ErrQuery) {
			t.Errorf("Search error = %v, want core.ErrQuery", err)
		}
	})
}

func TestStatsSummarizesCorpus(t *testing.T) {
	root := corpus(t, sample())
	a := testApp()
	if _, err := a.Index(root); err != nil {
		t.Fatalf("Index: %v", err)
	}

	c, err := a.Stats(root)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if c.Documents != 4 {
		t.Errorf("Documents = %d, want 4", c.Documents)
	}
	if c.ByKind["markdown"] != 2 {
		t.Errorf("ByKind[markdown] = %d, want 2", c.ByKind["markdown"])
	}
	if c.ByLanguage["go"] != 1 {
		t.Errorf("ByLanguage[go] = %d, want 1", c.ByLanguage["go"])
	}
	if len(c.LargestDocs) != 4 {
		t.Fatalf("LargestDocs = %d, want 4", len(c.LargestDocs))
	}
	if c.LargestDocs[0].ID != "docs/alpha.md" {
		t.Errorf("largest document = %q, want %q", c.LargestDocs[0].ID, "docs/alpha.md")
	}
	if c.Tokens <= 0 {
		t.Errorf("Tokens = %d, want a positive count", c.Tokens)
	}
}

func TestValidateReportsCleanAndDamagedIndexes(t *testing.T) {
	root := corpus(t, sample())
	a := testApp()
	if _, err := a.Index(root); err != nil {
		t.Fatalf("Index: %v", err)
	}

	f, err := a.Validate(root)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(f.Problems) != 0 {
		t.Errorf("Problems = %v, want none", f.Problems)
	}
	if f.Generation != 1 || f.Documents != 4 || f.Segments != 1 {
		t.Errorf("findings = %+v, want generation 1, 4 documents, 1 segment", f)
	}

	damage(t, a, root)
	f, err = a.Validate(root)
	if err != nil {
		t.Fatalf("Validate after damage: %v", err)
	}
	if len(f.Problems) != 1 {
		t.Fatalf("Problems = %v, want exactly one", f.Problems)
	}
	if want := "gen-0001/seg-0001.json: digest mismatch"; f.Problems[0] != want {
		t.Errorf("problem = %q, want %q", f.Problems[0], want)
	}
}

func TestWatchReportsChangesSincePreviousScan(t *testing.T) {
	root := corpus(t, sample())
	a := testApp()

	first, changes, err := a.Watch(root, nil)
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	if len(first) != 4 {
		t.Fatalf("scan = %d files, want 4", len(first))
	}
	if len(changes) != 4 {
		t.Fatalf("changes = %d, want 4 additions", len(changes))
	}
	for _, c := range changes {
		if c.Kind != "added" {
			t.Errorf("change %s = %q, want added", c.Rel, c.Kind)
		}
	}

	if _, _, err := a.Watch(root, first); err != nil {
		t.Fatalf("Watch: %v", err)
	}
	_, unchanged, err := a.Watch(root, first)
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	if len(unchanged) != 0 {
		t.Errorf("changes = %v, want none", unchanged)
	}

	if err := os.Remove(filepath.Join(root, "readme.txt")); err != nil {
		t.Fatalf("remove: %v", err)
	}
	write(t, filepath.Join(root, "docs", "gamma.md"), "# Gamma\n")
	_, changes, err = a.Watch(root, first)
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	want := map[string]string{"docs/gamma.md": "added", "readme.txt": "removed"}
	if len(changes) != len(want) {
		t.Fatalf("changes = %v, want %v", changes, want)
	}
	for _, c := range changes {
		if want[c.Rel] != c.Kind {
			t.Errorf("change %s = %q, want %q", c.Rel, c.Kind, want[c.Rel])
		}
	}
}

func TestConfigAppliesFileAndRejectsBadSettings(t *testing.T) {
	a := testApp()

	t.Run("defaults", func(t *testing.T) {
		root := corpus(t, sample())
		cfg, err := a.Config(root)
		if err != nil {
			t.Fatalf("Config: %v", err)
		}
		if cfg.OutputDir != core.DefaultOutputDir {
			t.Errorf("OutputDir = %q, want %q", cfg.OutputDir, core.DefaultOutputDir)
		}
		if cfg.MinTermLength != config.DefaultMinTermLength {
			t.Errorf("MinTermLength = %d, want %d", cfg.MinTermLength, config.DefaultMinTermLength)
		}
	})

	t.Run("file applies", func(t *testing.T) {
		root := corpus(t, sample())
		write(t, filepath.Join(root, config.FileName), "{\"output_dir\": \"idx\", \"include\": [\"*.md\"]}\n")
		cfg, err := a.Config(root)
		if err != nil {
			t.Fatalf("Config: %v", err)
		}
		if cfg.OutputDir != "idx" {
			t.Errorf("OutputDir = %q, want %q", cfg.OutputDir, "idx")
		}
		m, err := a.Index(root)
		if err != nil {
			t.Fatalf("Index: %v", err)
		}
		if m.DocCount != 2 {
			t.Errorf("DocCount = %d, want 2 Markdown files", m.DocCount)
		}
		if _, err := os.Stat(filepath.Join(root, "idx", core.ManifestFile)); err != nil {
			t.Errorf("manifest not published under idx: %v", err)
		}
	})

	t.Run("rejected", func(t *testing.T) {
		root := corpus(t, sample())
		write(t, filepath.Join(root, config.FileName), "{\"min_term_length\": 0}\n")
		if _, err := a.Config(root); !errors.Is(err, core.ErrConfig) {
			t.Errorf("Config error = %v, want core.ErrConfig", err)
		}
		if _, err := a.Index(root); !errors.Is(err, core.ErrConfig) {
			t.Errorf("Index error = %v, want core.ErrConfig", err)
		}
	})
}

// poison rewrites the title of one cached document to a value extraction could
// never produce, keeping the content hash intact so the entry stays reusable.
func poison(t *testing.T, a App, root, id, title string) {
	t.Helper()
	entries := loadCacheFile(t, a, root)
	e, ok := entries.Entries[id]
	if !ok {
		t.Fatalf("cache holds no entry for %s", id)
	}
	e.Doc.Title = title
	entries.Entries[id] = e
	if err := cache.Save(cachePath(t, a, root), entries); err != nil {
		t.Fatalf("save cache: %v", err)
	}
}

// loadCacheFile reads the extraction cache published for a corpus.
func loadCacheFile(t *testing.T, a App, root string) *cache.Store {
	t.Helper()
	entries, err := cache.Load(cachePath(t, a, root))
	if err != nil {
		t.Fatalf("load cache: %v", err)
	}
	return entries
}

// cachePath returns the path of the published extraction cache.
func cachePath(t *testing.T, a App, root string) string {
	t.Helper()
	cfg, err := a.Config(root)
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	return filepath.Join(publish.OutputPath(cfg), core.CacheFile)
}

// titleOf returns the title the published index holds for one document.
func titleOf(t *testing.T, a App, root, id string) string {
	t.Helper()
	rep, err := a.Search(root, core.SearchOptions{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	for _, res := range rep.Results {
		if string(res.DocID) == id {
			return res.Title
		}
	}
	t.Fatalf("document %s is not indexed", id)
	return ""
}

// damage appends bytes to the published segment so its digest no longer
// matches the manifest.
func damage(t *testing.T, a App, root string) {
	t.Helper()
	cfg, err := a.Config(root)
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	path := filepath.Join(publish.OutputPath(cfg), "gen-0001", "seg-0001.json")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open segment: %v", err)
	}
	defer f.Close()
	if _, err := f.WriteString(" "); err != nil {
		t.Fatalf("damage segment: %v", err)
	}
}
