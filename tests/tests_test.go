package tests

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"sift/internal/app"
	"sift/internal/cli"
	"sift/internal/config"
	"sift/internal/core"
	"sift/internal/manifest"
	"sift/internal/publish"
	"sift/internal/server"
	"sift/internal/store"
	"sift/internal/workspace"
)

// published is the time every reproducible test stamps its manifests with.
var published = time.Date(2024, 5, 6, 7, 8, 9, 0, time.UTC)

// sample is the corpus the tests index: two Markdown documents, one source
// file, one text file and one file that must never be indexed.
var sample = map[string]string{
	"docs/alpha.md":  "# Alpha Guide\n\nalpha beta gamma alpha indexing\n",
	"docs/beta.md":   "# Beta Notes\n\nbeta gamma searching\n",
	"src/main.go":    "package main\n\nfunc main() { println(\"alpha\") }\n",
	"readme.txt":     "plain text alpha\n",
	"assets/pic.bin": "header\x00\x00\x01binary payload",
}

// corpus writes files under a fresh directory and returns its path.
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
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// fixed returns an App whose manifests are reproducible.
func fixed() app.App {
	return app.App{Clock: core.FixedClock{T: published}}
}

// outputDir returns the directory a corpus publishes into.
func outputDir(t *testing.T, root string) string {
	t.Helper()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	return publish.OutputPath(cfg)
}

// run executes one command line and returns its exit code and both streams.
func run(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errs bytes.Buffer
	code := cli.Run(args, &out, &errs)
	return code, out.String(), errs.String()
}

// mustRun executes one command line that has to succeed.
func mustRun(t *testing.T, args ...string) string {
	t.Helper()
	code, out, errs := run(t, args...)
	if code != cli.ExitOK {
		t.Fatalf("%v: exit %d, stderr %s", args, code, errs)
	}
	return out
}

func TestEndToEndIndexAndSearch(t *testing.T) {
	root := corpus(t, sample)
	a := fixed()

	m, err := a.Index(root)
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	if m.Generation != 1 {
		t.Errorf("Generation = %d, want 1", m.Generation)
	}
	if m.DocCount != 4 {
		t.Errorf("DocCount = %d, want 4 (the binary file must be skipped)", m.DocCount)
	}
	if m.TermCount <= 0 {
		t.Errorf("TermCount = %d, want a positive count", m.TermCount)
	}

	out := outputDir(t, root)
	for _, rel := range []string{core.ManifestFile, core.CacheFile, "gen-0001/seg-0001.json"} {
		if _, err := os.Stat(filepath.Join(out, filepath.FromSlash(rel))); err != nil {
			t.Errorf("published file %s missing: %v", rel, err)
		}
	}

	rep, err := a.Search(root, core.SearchOptions{Query: "alpha", Facets: []string{"kind"}})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if rep.Total != 3 {
		t.Fatalf("Total = %d, want 3", rep.Total)
	}
	if rep.Results[0].DocID != "docs/alpha.md" {
		t.Errorf("first result = %q, want docs/alpha.md", rep.Results[0].DocID)
	}
	if rep.Results[0].Freq != 3 {
		t.Errorf("freq = %d, want 3", rep.Results[0].Freq)
	}
	if rep.Results[0].Title != "Alpha Guide" {
		t.Errorf("title = %q, want %q", rep.Results[0].Title, "Alpha Guide")
	}
	for i := 1; i < len(rep.Results); i++ {
		if rep.Results[i-1].Score < rep.Results[i].Score {
			t.Errorf("results are not ordered by score: %v", rep.Results)
		}
	}
	if got := rep.Facets["kind"].Counts["markdown"]; got != 1 {
		t.Errorf("facet kind[markdown] = %d, want 1", got)
	}
	if rep.Results[0].Fields["dir"] != "docs" {
		t.Errorf("fields = %v, want dir=docs", rep.Results[0].Fields)
	}
}

func TestManifestDescribesWhatWasPublished(t *testing.T) {
	root := corpus(t, sample)
	a := fixed()
	m, err := a.Index(root)
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	out := outputDir(t, root)

	loaded, err := manifest.Load(out)
	if err != nil {
		t.Fatalf("manifest.Load: %v", err)
	}
	if loaded.Generation != m.Generation || loaded.DocCount != m.DocCount {
		t.Errorf("loaded manifest = %+v, want the published one %+v", loaded, m)
	}
	if !loaded.CreatedAt.Equal(published) {
		t.Errorf("CreatedAt = %v, want %v", loaded.CreatedAt, published)
	}
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if loaded.ConfigHash != config.Hash(cfg) {
		t.Errorf("ConfigHash = %q, want %q", loaded.ConfigHash, config.Hash(cfg))
	}

	digest, err := store.SHA256File(filepath.Join(out, core.CacheFile))
	if err != nil {
		t.Fatalf("hash cache: %v", err)
	}
	if loaded.CacheDigest != digest {
		t.Errorf("CacheDigest = %q, want the digest of the cache file %q", loaded.CacheDigest, digest)
	}
	for _, ref := range loaded.Segments {
		got, err := store.SHA256File(filepath.Join(out, filepath.FromSlash(ref.File)))
		if err != nil {
			t.Fatalf("hash segment: %v", err)
		}
		if got != ref.Digest {
			t.Errorf("segment %s digest = %q, want %q", ref.File, got, ref.Digest)
		}
	}
}

func TestRepublishAfterEdits(t *testing.T) {
	root := corpus(t, sample)
	a := fixed()
	if _, err := a.Index(root); err != nil {
		t.Fatalf("first Index: %v", err)
	}
	out := outputDir(t, root)

	// Edit one document, add another and remove a third.
	write(t, filepath.Join(root, "docs", "alpha.md"), "# Alpha Guide\n\nalpha epsilon\n")
	write(t, filepath.Join(root, "docs", "gamma.md"), "# Gamma\n\ngamma delta epsilon\n")
	if err := os.Remove(filepath.Join(root, "readme.txt")); err != nil {
		t.Fatalf("remove: %v", err)
	}

	m, err := a.Index(root)
	if err != nil {
		t.Fatalf("second Index: %v", err)
	}
	if m.Generation != 2 {
		t.Errorf("Generation = %d, want 2", m.Generation)
	}
	if m.DocCount != 4 {
		t.Errorf("DocCount = %d, want 4", m.DocCount)
	}
	if _, err := os.Stat(filepath.Join(out, "gen-0001")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("gen-0001 survived the republish: %v", err)
	}
	if _, err := os.Stat(filepath.Join(out, publish.StagingDir)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("staging directory survived the republish: %v", err)
	}

	cases := []struct {
		query string
		want  []core.DocID
	}{
		{query: "epsilon", want: []core.DocID{"docs/alpha.md", "docs/gamma.md"}},
		{query: "indexing", want: nil},
		{query: "plain", want: nil},
		{query: "delta", want: []core.DocID{"docs/gamma.md"}},
	}
	for _, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			rep, err := a.Search(root, core.SearchOptions{Query: tc.query})
			if err != nil {
				t.Fatalf("Search: %v", err)
			}
			if len(rep.Results) != len(tc.want) {
				t.Fatalf("results = %v, want %v", rep.Results, tc.want)
			}
			got := make([]core.DocID, len(rep.Results))
			for i, res := range rep.Results {
				got[i] = res.DocID
			}
			for i, id := range tc.want {
				if got[i] != id {
					t.Errorf("result %d = %q, want %q", i, got[i], id)
				}
			}
		})
	}

	f, err := a.Validate(root)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(f.Problems) != 0 {
		t.Errorf("problems = %v, want a clean index after republishing", f.Problems)
	}
	if f.Generation != 2 {
		t.Errorf("Generation = %d, want 2", f.Generation)
	}
}

func TestValidateDetectsDamage(t *testing.T) {
	cases := []struct {
		name   string
		damage func(t *testing.T, out string)
		path   string
		reason string
	}{
		{
			name: "segment rewritten",
			damage: func(t *testing.T, out string) {
				p := filepath.Join(out, "gen-0001", "seg-0001.json")
				data, err := os.ReadFile(p)
				if err != nil {
					t.Fatalf("read: %v", err)
				}
				write(t, p, string(data)+" ")
			},
			path:   "gen-0001/seg-0001.json",
			reason: "digest mismatch",
		},
		{
			name: "segment removed",
			damage: func(t *testing.T, out string) {
				if err := os.Remove(filepath.Join(out, "gen-0001", "seg-0001.json")); err != nil {
					t.Fatalf("remove: %v", err)
				}
			},
			path:   "gen-0001/seg-0001.json",
			reason: "missing",
		},
		{
			name: "cache rewritten",
			damage: func(t *testing.T, out string) {
				write(t, filepath.Join(out, core.CacheFile), "{}\n")
			},
			path:   core.CacheFile,
			reason: "digest mismatch",
		},
		{
			name: "cache removed",
			damage: func(t *testing.T, out string) {
				if err := os.Remove(filepath.Join(out, core.CacheFile)); err != nil {
					t.Fatalf("remove: %v", err)
				}
			},
			path:   core.CacheFile,
			reason: "missing",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := corpus(t, sample)
			a := fixed()
			if _, err := a.Index(root); err != nil {
				t.Fatalf("Index: %v", err)
			}
			tc.damage(t, outputDir(t, root))

			f, err := a.Validate(root)
			if err != nil {
				t.Fatalf("Validate: %v", err)
			}
			want := tc.path + ": " + tc.reason
			if len(f.Problems) == 0 || f.Problems[0] != want {
				t.Fatalf("problems = %v, want the first to be %q", f.Problems, want)
			}
			if _, err := a.Search(root, core.SearchOptions{Query: "alpha"}); !errors.Is(err, core.ErrIntegrity) {
				t.Errorf("Search error = %v, want core.ErrIntegrity", err)
			}
			code, out, errs := run(t, "validate", "-root", root)
			if code != cli.ExitError {
				t.Fatalf("validate exit = %d, want %d", code, cli.ExitError)
			}
			if !strings.Contains(out, want) {
				t.Errorf("stdout = %q, want it to name %q", out, want)
			}
			if !strings.Contains(errs, "sift validate:") {
				t.Errorf("stderr = %q, want the command named", errs)
			}
		})
	}
}

func TestIndexingIsReproducibleAcrossPaths(t *testing.T) {
	first := corpus(t, sample)
	second := corpus(t, sample)
	a := fixed()
	if _, err := a.Index(first); err != nil {
		t.Fatalf("Index: %v", err)
	}
	if _, err := a.Index(second); err != nil {
		t.Fatalf("Index: %v", err)
	}

	for _, rel := range []string{core.ManifestFile, core.CacheFile, "gen-0001/seg-0001.json"} {
		name := filepath.FromSlash(rel)
		left, err := os.ReadFile(filepath.Join(outputDir(t, first), name))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		right, err := os.ReadFile(filepath.Join(outputDir(t, second), name))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		if !bytes.Equal(left, right) {
			t.Errorf("%s differs between two corpora with identical contents", rel)
		}
		if bytes.Contains(left, []byte(first)) {
			t.Errorf("%s embeds the corpus path, so it cannot be reproducible", rel)
		}
	}
}

func TestCLIIndexAndSearch(t *testing.T) {
	root := corpus(t, sample)

	indexed := mustRun(t, "index", "-root", root)
	if !strings.HasPrefix(indexed, "generation: 1\n") || !strings.Contains(indexed, "documents: 4\n") {
		t.Fatalf("index output = %q", indexed)
	}

	text := mustRun(t, "search", "-root", root, "alpha")
	if !strings.Contains(text, "total: 3\n") {
		t.Errorf("search output = %q, want three matches", text)
	}
	if !strings.Contains(text, "1. docs/alpha.md\n") {
		t.Errorf("search output = %q, want the best match first", text)
	}
	if strings.Contains(text, "assets/pic.bin") {
		t.Errorf("search output = %q, want the binary file absent", text)
	}

	phrase := mustRun(t, "search", "-root", root, "\"beta gamma\"")
	if !strings.Contains(phrase, "total: 2\n") {
		t.Errorf("phrase search = %q, want two matches", phrase)
	}
	negated := mustRun(t, "search", "-root", root, "gamma", "-alpha")
	if !strings.Contains(negated, "total: 1\n") || !strings.Contains(negated, "docs/beta.md") {
		t.Errorf("negated search = %q, want only docs/beta.md", negated)
	}
}

func TestCLISearchFormats(t *testing.T) {
	root := corpus(t, sample)
	mustRun(t, "index", "-root", root)

	cases := []struct {
		format   string
		contains []string
	}{
		{format: "text", contains: []string{"query: alpha\n", "total: 3\n", "   score: "}},
		{format: "md", contains: []string{"# Search report\n", "| # | Score |", "`docs/alpha.md`"}},
		{format: "csv", contains: []string{"rank,doc_id,path,title,score,freq,fields,snippet\n", "1,docs/alpha.md,"}},
		{format: "json", contains: []string{"\"total\": 3", "\"doc_id\": \"docs/alpha.md\""}},
	}
	for _, tc := range cases {
		t.Run(tc.format, func(t *testing.T) {
			out := mustRun(t, "search", "-root", root, "-format", tc.format, "alpha")
			for _, want := range tc.contains {
				if !strings.Contains(out, want) {
					t.Errorf("output = %q, want it to contain %q", out, want)
				}
			}
			again := mustRun(t, "search", "-root", root, "-format", tc.format, "alpha")
			if again != out {
				t.Errorf("two identical searches produced different output")
			}
		})
	}
}

func TestCLIStatsAndConfig(t *testing.T) {
	root := corpus(t, sample)
	// The configuration file is corpus content like any other file: the
	// scanner only skips .git and the output directory, so a corpus that does
	// not want its own settings indexed excludes them.
	write(t, filepath.Join(root, config.FileName), "{\"exclude\": [\"*.txt\", \".sift.json\"], \"segment_docs\": 2}\n")
	mustRun(t, "index", "-root", root)

	stats := mustRun(t, "stats", "-root", root)
	for _, want := range []string{"documents: 3\n", "by kind:\n", "  markdown: 2\n", "  source: 1\n", "by language:\n", "  go: 1\n", "largest documents:\n"} {
		if !strings.Contains(stats, want) {
			t.Errorf("stats = %q, want it to contain %q", stats, want)
		}
	}

	cfg := mustRun(t, "config", "-root", root)
	for _, want := range []string{"exclude: *.txt, .sift.json\n", "include: (none)\n", "segment_docs: 2\n", "hash: "} {
		if !strings.Contains(cfg, want) {
			t.Errorf("config = %q, want it to contain %q", cfg, want)
		}
	}

	// Two documents per segment means the three indexed documents need two
	// segment files, which the manifest and the validation both report.
	if got := mustRun(t, "validate", "-root", root); !strings.Contains(got, "segments: 2\n") {
		t.Errorf("validate = %q, want two segments", got)
	}
}

func TestCLIValidateReportsConfigurationDrift(t *testing.T) {
	root := corpus(t, sample)
	mustRun(t, "index", "-root", root)
	if got := mustRun(t, "validate", "-root", root); !strings.Contains(got, "problems: none\n") {
		t.Fatalf("validate = %q, want a clean index", got)
	}

	write(t, filepath.Join(root, config.FileName), "{\"min_term_length\": 5}\n")
	code, out, _ := run(t, "validate", "-root", root)
	if code != cli.ExitError {
		t.Errorf("exit = %d, want %d after a configuration change", code, cli.ExitError)
	}
	if !strings.Contains(out, "configuration changed since generation 1 was published") {
		t.Errorf("validate = %q, want the drift reported", out)
	}

	// Re-indexing under the new configuration makes the index current again,
	// and the shorter terms are gone because the cache was not reused.
	mustRun(t, "index", "-root", root)
	if got := mustRun(t, "validate", "-root", root); !strings.Contains(got, "problems: none\n") {
		t.Errorf("validate = %q, want a clean index after re-indexing", got)
	}
	if got := mustRun(t, "search", "-root", root, "beta"); !strings.Contains(got, "total: 0\n") {
		t.Errorf("search = %q, want no match for a term below the new minimum length", got)
	}
}

func TestCLIWorkspaceRegistration(t *testing.T) {
	ws := t.TempDir()
	docs := corpus(t, sample)
	code := corpus(t, map[string]string{"lib/util.go": "package lib\n\nfunc Helper() { println(\"helper\") }\n"})

	mustRun(t, "workspace", "add", "-root", ws, "docs", docs)
	mustRun(t, "workspace", "add", "-root", ws, "code", code, "index-out")

	registry, err := workspace.Load(ws)
	if err != nil {
		t.Fatalf("workspace.Load: %v", err)
	}
	if registry.Active != "docs" {
		t.Errorf("Active = %q, want docs", registry.Active)
	}
	listed := registry.List()
	if len(listed) != 2 || listed[0].Name != "code" || listed[1].Name != "docs" {
		t.Fatalf("List = %+v, want code then docs", listed)
	}
	if got, want := workspace.InferOutputPath(listed[0]), filepath.Join(code, "index-out"); got != want {
		t.Errorf("output path = %q, want %q", got, want)
	}

	// Each registered corpus indexes and searches on its own.
	write(t, filepath.Join(code, config.FileName), "{\"output_dir\": \"index-out\"}\n")
	mustRun(t, "index", "-root", docs)
	mustRun(t, "index", "-root", code)
	if _, err := os.Stat(filepath.Join(code, "index-out", core.ManifestFile)); err != nil {
		t.Errorf("second corpus did not publish into its own output directory: %v", err)
	}
	if got := mustRun(t, "search", "-root", code, "helper"); !strings.Contains(got, "lib/util.go") {
		t.Errorf("search = %q, want the second corpus searched", got)
	}
	if got := mustRun(t, "search", "-root", docs, "helper"); !strings.Contains(got, "total: 0\n") {
		t.Errorf("search = %q, want the corpora kept apart", got)
	}

	list := mustRun(t, "workspace", "list", "-root", ws)
	if !strings.Contains(list, "* docs\t") || !strings.Contains(list, "  code\t") {
		t.Errorf("list = %q, want docs active and code listed", list)
	}
}

func TestCLIWatchReportsChanges(t *testing.T) {
	root := corpus(t, sample)
	state := filepath.Join(t.TempDir(), "scan.json")

	first := mustRun(t, "watch", "-root", root, "-state", state)
	if !strings.Contains(first, "files: 5\n") {
		t.Errorf("watch = %q, want five files including the binary one", first)
	}
	if !strings.Contains(first, "changes: 5\n") {
		t.Errorf("watch = %q, want every file reported as added", first)
	}

	write(t, filepath.Join(root, "docs", "beta.md"), "# Beta Notes\n\nbeta gamma delta\n")
	write(t, filepath.Join(root, "docs", "gamma.md"), "# Gamma\n")
	if err := os.Remove(filepath.Join(root, "src", "main.go")); err != nil {
		t.Fatalf("remove: %v", err)
	}

	second := mustRun(t, "watch", "-root", root, "-state", state)
	if !strings.Contains(second, "changes: 3\n") {
		t.Fatalf("watch = %q, want three changes", second)
	}
	for _, want := range []string{"  added     docs/gamma.md\n", "  modified  docs/beta.md\n", "  removed   src/main.go\n"} {
		if !strings.Contains(second, want) {
			t.Errorf("watch = %q, want it to contain %q", second, want)
		}
	}
	if quiet := mustRun(t, "watch", "-root", root, "-state", state); !strings.Contains(quiet, "changes: 0\n") {
		t.Errorf("watch = %q, want the state file to have been updated", quiet)
	}
}

func TestCLIVersionAndHelp(t *testing.T) {
	if got, want := mustRun(t, "version"), "sift "+cli.Version+"\n"; got != want {
		t.Errorf("version = %q, want %q", got, want)
	}
	help := mustRun(t, "help")
	for _, name := range []string{"index", "search", "stats", "validate", "config", "workspace", "serve", "watch", "version", "help"} {
		if !strings.Contains(help, name) {
			t.Errorf("help = %q, want it to list %q", help, name)
		}
	}
	if got := mustRun(t, "help", "workspace"); !strings.HasPrefix(got, "usage: sift workspace list") {
		t.Errorf("help workspace = %q", got)
	}
}

func TestCLIUsageAndRuntimeExitCodes(t *testing.T) {
	root := corpus(t, sample)
	cases := []struct {
		name string
		args []string
		code int
	}{
		{name: "no command", args: nil, code: cli.ExitUsage},
		{name: "unknown command", args: []string{"reindex"}, code: cli.ExitUsage},
		{name: "unknown flag", args: []string{"stats", "-verbose"}, code: cli.ExitUsage},
		{name: "unknown format", args: []string{"search", "-root", root, "-format", "yaml"}, code: cli.ExitUsage},
		{name: "malformed filter", args: []string{"search", "-root", root, "-filter", "kind"}, code: cli.ExitUsage},
		{name: "malformed query", args: []string{"search", "-root", root, "kind:"}, code: cli.ExitUsage},
		{name: "missing corpus", args: []string{"index", "-root", filepath.Join(root, "absent")}, code: cli.ExitError},
		{name: "unindexed corpus", args: []string{"search", "-root", root, "alpha"}, code: cli.ExitError},
		{name: "help", args: []string{"help"}, code: cli.ExitOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, out, errs := run(t, tc.args...)
			if code != tc.code {
				t.Fatalf("exit = %d, want %d (stdout %q, stderr %q)", code, tc.code, out, errs)
			}
			switch tc.code {
			case cli.ExitOK:
				if errs != "" {
					t.Errorf("stderr = %q, want empty on success", errs)
				}
			default:
				if !strings.HasPrefix(errs, "sift") {
					t.Errorf("stderr = %q, want it to name the command", errs)
				}
			}
		})
	}
}

func TestHTTPSearchEndpoint(t *testing.T) {
	root := corpus(t, sample)
	a := fixed()
	if _, err := a.Index(root); err != nil {
		t.Fatalf("Index: %v", err)
	}
	handler := server.Handler(a)

	t.Run("healthz", func(t *testing.T) {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/healthz", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", w.Code)
		}
		if strings.TrimSpace(w.Body.String()) != "ok" {
			t.Errorf("body = %q, want ok", w.Body.String())
		}
	})

	query := url.Values{"root": {root}, "q": {"alpha"}, "limit": {"2"}, "facet": {"kind"}}
	t.Run("search", func(t *testing.T) {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/search?"+query.Encode(), nil))
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, body %s", w.Code, w.Body.String())
		}
		var report core.SearchReport
		if err := json.Unmarshal(w.Body.Bytes(), &report); err != nil {
			t.Fatalf("decode: %v\n%s", err, w.Body.String())
		}
		if report.Total != 3 {
			t.Errorf("Total = %d, want 3", report.Total)
		}
		if len(report.Results) != 2 {
			t.Errorf("Results = %d, want the limit of 2", len(report.Results))
		}
		if report.Results[0].DocID != "docs/alpha.md" {
			t.Errorf("first result = %q, want docs/alpha.md", report.Results[0].DocID)
		}
		if len(report.Facets["kind"].Counts) != 3 {
			t.Errorf("kind facet = %v, want three values counted over every match", report.Facets["kind"].Counts)
		}
	})

	t.Run("errors carry a status", func(t *testing.T) {
		cases := []struct {
			name   string
			target string
			status int
		}{
			{name: "bad query", target: "/search?" + url.Values{"root": {root}, "q": {"\"open"}}.Encode(), status: http.StatusBadRequest},
			{name: "unknown corpus", target: "/search?" + url.Values{"root": {filepath.Join(root, "absent")}, "q": {"alpha"}}.Encode(), status: http.StatusNotFound},
			{name: "unknown path", target: "/nowhere", status: http.StatusNotFound},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, tc.target, nil))
				if w.Code != tc.status {
					t.Errorf("status = %d, want %d (body %s)", w.Code, tc.status, w.Body.String())
				}
			})
		}
	})
}

func TestIndexReusesTheCacheAcrossRuns(t *testing.T) {
	root := corpus(t, sample)
	a := fixed()
	if _, err := a.Index(root); err != nil {
		t.Fatalf("first Index: %v", err)
	}
	out := outputDir(t, root)
	before, err := os.ReadFile(filepath.Join(out, core.CacheFile))
	if err != nil {
		t.Fatalf("read cache: %v", err)
	}

	if _, err := a.Index(root); err != nil {
		t.Fatalf("second Index: %v", err)
	}
	after, err := os.ReadFile(filepath.Join(out, core.CacheFile))
	if err != nil {
		t.Fatalf("read cache: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("the cache changed although the corpus did not")
	}

	if err := os.Remove(filepath.Join(root, "docs", "beta.md")); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := a.Index(root); err != nil {
		t.Fatalf("third Index: %v", err)
	}
	pruned, err := os.ReadFile(filepath.Join(out, core.CacheFile))
	if err != nil {
		t.Fatalf("read cache: %v", err)
	}
	if bytes.Contains(pruned, []byte("docs/beta.md")) {
		t.Errorf("the cache still holds the removed document")
	}
	if len(pruned) >= len(after) {
		t.Errorf("cache grew from %d to %d bytes after a document was removed", len(after), len(pruned))
	}
}

func TestIndexHandlesAnEmptyCorpus(t *testing.T) {
	root := corpus(t, map[string]string{})
	a := fixed()

	m, err := a.Index(root)
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	if m.DocCount != 0 || len(m.Segments) != 0 {
		t.Errorf("manifest = %+v, want no documents and no segments", m)
	}
	f, err := a.Validate(root)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(f.Problems) != 0 {
		t.Errorf("problems = %v, want none", f.Problems)
	}
	rep, err := a.Search(root, core.SearchOptions{Query: "alpha"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if rep.Total != 0 || len(rep.Results) != 0 {
		t.Errorf("report = %+v, want no matches", rep)
	}
	if got := mustRun(t, "stats", "-root", root); !strings.Contains(got, "documents: 0\n") {
		t.Errorf("stats = %q, want an empty corpus", got)
	}
}
