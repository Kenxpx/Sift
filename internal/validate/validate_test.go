package validate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"sift/internal/cache"
	"sift/internal/config"
	"sift/internal/core"
	"sift/internal/index"
	"sift/internal/manifest"
	"sift/internal/publish"
	"sift/internal/store"
)

// fixedTime keeps published manifests identical from run to run.
var fixedTime = time.Date(2024, 5, 17, 9, 30, 0, 0, time.UTC)

// publishTestIndex publishes one generation of a small corpus and returns the
// configuration it was published with and its output directory. The documents
// share two terms and add one of their own each, so three documents make five
// distinct terms across two segments.
func publishTestIndex(t *testing.T, ids ...string) (core.Config, string) {
	t.Helper()
	cfg := config.Default(t.TempDir())
	cfg.SegmentDocs = 2
	ix := index.New()
	cs := cache.New()
	for i, id := range ids {
		hash := fmt.Sprintf("%064d", i+1)
		doc := core.Document{
			ID:          core.DocID(id),
			Path:        id,
			Title:       "title of " + id,
			Kind:        "text",
			Fields:      map[string]string{"kind": "text"},
			ContentHash: hash,
		}
		terms := []string{"alpha", "beta", "term" + strconv.Itoa(i)}
		tokens := make([]core.Token, 0, len(terms))
		for pos, term := range terms {
			tokens = append(tokens, core.Token{Term: term, Position: pos})
		}
		ix.Add(doc, tokens)
		cs.Put(doc.ID, cache.Entry{ContentHash: hash, Doc: doc, Tokens: tokens})
	}
	if _, err := publish.Publish(cfg, ix, cs, core.FixedClock{T: fixedTime}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	return cfg, publish.OutputPath(cfg)
}

// rewriteManifest loads the published manifest, hands it to edit and saves it
// again, so tests can describe a manifest that no longer tells the truth.
func rewriteManifest(t *testing.T, out string, edit func(m *core.Manifest)) {
	t.Helper()
	m, err := manifest.Load(out)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	edit(&m)
	if err := manifest.Save(out, m); err != nil {
		t.Fatalf("save manifest: %v", err)
	}
}

// mustReport reports on cfg and fails the test when reporting itself fails.
func mustReport(t *testing.T, cfg core.Config) Findings {
	t.Helper()
	f, err := Report(cfg)
	if err != nil {
		t.Fatalf("Report: %v", err)
	}
	return f
}

func TestIndexAndReportAcceptAFreshPublish(t *testing.T) {
	cfg, _ := publishTestIndex(t, "a.txt", "b.txt", "c.txt")

	if err := Index(cfg); err != nil {
		t.Fatalf("Index of a freshly published corpus: %v", err)
	}
	f := mustReport(t, cfg)
	if f.Generation != 1 {
		t.Errorf("Generation = %d, want 1", f.Generation)
	}
	if f.Segments != 2 {
		t.Errorf("Segments = %d, want 2", f.Segments)
	}
	if f.Documents != 3 {
		t.Errorf("Documents = %d, want 3", f.Documents)
	}
	if f.Terms != 5 {
		t.Errorf("Terms = %d, want 5", f.Terms)
	}
	if len(f.Problems) != 0 {
		t.Errorf("Problems = %v, want none", f.Problems)
	}
}

func TestMissingManifestIsNotFound(t *testing.T) {
	cfg := config.Default(t.TempDir())

	if err := Index(cfg); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("Index error = %v, want it to match core.ErrNotFound", err)
	}
	f, err := Report(cfg)
	if !errors.Is(err, core.ErrNotFound) {
		t.Errorf("Report error = %v, want it to match core.ErrNotFound", err)
	}
	if f.Generation != 0 || f.Segments != 0 || len(f.Problems) != 0 {
		t.Errorf("Findings = %+v, want the zero value", f)
	}
}

func TestIndexReportsDamage(t *testing.T) {
	// wantReason is matched as a substring, because a neighbouring package
	// words the reasons for the files it owns.
	cases := []struct {
		name       string
		damage     func(t *testing.T, out string)
		wantPath   string
		wantReason string
	}{
		{
			name: "segment deleted",
			damage: func(t *testing.T, out string) {
				remove(t, out, "gen-0001/seg-0001.json")
			},
			wantPath:   "gen-0001/seg-0001.json",
			wantReason: "missing",
		},
		{
			name: "segment contents changed",
			damage: func(t *testing.T, out string) {
				overwrite(t, out, "gen-0001/seg-0002.json", "{}\n")
			},
			wantPath:   "gen-0001/seg-0002.json",
			wantReason: "digest",
		},
		{
			name: "cache deleted",
			damage: func(t *testing.T, out string) {
				remove(t, out, core.CacheFile)
			},
			wantPath:   core.CacheFile,
			wantReason: "missing",
		},
		{
			name: "cache contents changed",
			damage: func(t *testing.T, out string) {
				overwrite(t, out, core.CacheFile, "{\"Entries\":{}}\n")
			},
			wantPath:   core.CacheFile,
			wantReason: "digest mismatch",
		},
		{
			name: "manifest claims more documents than the segments hold",
			damage: func(t *testing.T, out string) {
				rewriteManifest(t, out, func(m *core.Manifest) { m.DocCount = 99 })
			},
			wantPath:   core.ManifestFile,
			wantReason: "doc count 99 does not match the 3 documents in the segments",
		},
		{
			name: "manifest claims more terms than the segments hold",
			damage: func(t *testing.T, out string) {
				rewriteManifest(t, out, func(m *core.Manifest) { m.TermCount = 7 })
			},
			wantPath:   core.ManifestFile,
			wantReason: "term count 7 does not match the 5 terms in the segments",
		},
		{
			name: "the same segment is listed twice",
			damage: func(t *testing.T, out string) {
				rewriteManifest(t, out, func(m *core.Manifest) {
					m.Segments = []core.SegmentRef{m.Segments[0], m.Segments[0], m.Segments[1]}
				})
			},
			wantPath:   core.ManifestFile,
			wantReason: "segment seg-0001 is listed twice",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, out := publishTestIndex(t, "a.txt", "b.txt", "c.txt")
			tc.damage(t, out)

			err := Index(cfg)
			if !errors.Is(err, core.ErrIntegrity) {
				t.Fatalf("Index error = %v, want it to match core.ErrIntegrity", err)
			}
			var damaged *core.IntegrityError
			if !errors.As(err, &damaged) {
				t.Fatalf("Index error = %v, want a *core.IntegrityError", err)
			}
			if damaged.Path != tc.wantPath {
				t.Errorf("IntegrityError.Path = %q, want %q", damaged.Path, tc.wantPath)
			}
			if !strings.Contains(damaged.Reason, tc.wantReason) {
				t.Errorf("IntegrityError.Reason = %q, want it to contain %q", damaged.Reason, tc.wantReason)
			}

			f := mustReport(t, cfg)
			if len(f.Problems) == 0 {
				t.Fatal("Report listed no problems, want at least one")
			}
			if got, want := f.Problems[0], damaged.Path+": "+damaged.Reason; got != want {
				t.Errorf("first problem = %q, want the error Index returned, %q", got, want)
			}
		})
	}
}

// remove deletes a file below the output directory.
func remove(t *testing.T, out, rel string) {
	t.Helper()
	if err := os.Remove(filepath.Join(out, filepath.FromSlash(rel))); err != nil {
		t.Fatalf("remove %s: %v", rel, err)
	}
}

// overwrite replaces a file below the output directory.
func overwrite(t *testing.T, out, rel, contents string) {
	t.Helper()
	path := filepath.Join(out, filepath.FromSlash(rel))
	if err := store.WriteFileAtomic(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("overwrite %s: %v", rel, err)
	}
}

func TestReportListsEveryProblem(t *testing.T) {
	cfg, out := publishTestIndex(t, "a.txt", "b.txt", "c.txt")
	overwrite(t, out, "gen-0001/seg-0001.json", "{}\n")
	remove(t, out, core.CacheFile)

	f := mustReport(t, cfg)
	if len(f.Problems) != 2 {
		t.Fatalf("Problems = %v, want two of them", f.Problems)
	}
	if !strings.HasPrefix(f.Problems[0], "gen-0001/seg-0001.json: ") {
		t.Errorf("Problems[0] = %q, want it to be about gen-0001/seg-0001.json", f.Problems[0])
	}
	if got, want := f.Problems[1], core.CacheFile+": missing"; got != want {
		t.Errorf("Problems[1] = %q, want %q", got, want)
	}
	// The counts still describe what the manifest claims, damaged or not.
	if f.Generation != 1 || f.Segments != 2 || f.Documents != 3 || f.Terms != 5 {
		t.Errorf("Findings = %+v, want generation 1 with 2 segments, 3 documents, 5 terms", f)
	}
}

func TestReportFlagsConfigurationDrift(t *testing.T) {
	cfg, _ := publishTestIndex(t, "a.txt", "b.txt", "c.txt")
	drifted := cfg
	drifted.MinTermLength = cfg.MinTermLength + 1

	f := mustReport(t, drifted)
	want := core.ManifestFile + ": configuration changed since generation 1 was published"
	if len(f.Problems) != 1 || f.Problems[0] != want {
		t.Fatalf("Problems = %v, want exactly [%q]", f.Problems, want)
	}
	// Drift is not damage: the published generation is still usable.
	if err := Index(drifted); err != nil {
		t.Errorf("Index of a drifted configuration = %v, want nil", err)
	}
	if f := mustReport(t, cfg); len(f.Problems) != 0 {
		t.Errorf("Problems with the original configuration = %v, want none", f.Problems)
	}
}

func TestReportFlagsALeftoverStagingDirectory(t *testing.T) {
	cfg, out := publishTestIndex(t, "a.txt", "b.txt")
	staging := filepath.Join(out, publish.StagingDir)
	if err := store.EnsureDir(staging); err != nil {
		t.Fatalf("create staging: %v", err)
	}

	f := mustReport(t, cfg)
	want := publish.StagingDir + ": left behind by an interrupted publish"
	if len(f.Problems) != 1 || f.Problems[0] != want {
		t.Fatalf("Problems = %v, want exactly [%q]", f.Problems, want)
	}
	if err := Index(cfg); err != nil {
		t.Errorf("Index with a leftover staging directory = %v, want nil", err)
	}

	// A file in the way of staging is worse: the next publish cannot run.
	if err := os.Remove(staging); err != nil {
		t.Fatalf("remove staging: %v", err)
	}
	overwrite(t, out, publish.StagingDir, "in the way\n")
	f = mustReport(t, cfg)
	want = publish.StagingDir + ": not a directory, so the next publish will fail"
	if len(f.Problems) != 1 || f.Problems[0] != want {
		t.Errorf("Problems = %v, want exactly [%q]", f.Problems, want)
	}
}

func TestReportFlagsSegmentsOutOfOrder(t *testing.T) {
	cfg, out := publishTestIndex(t, "a.txt", "b.txt", "c.txt")
	rewriteManifest(t, out, func(m *core.Manifest) {
		m.Segments = []core.SegmentRef{m.Segments[1], m.Segments[0]}
	})

	f := mustReport(t, cfg)
	want := core.ManifestFile + ": segments are not in ascending order"
	if len(f.Problems) != 1 || f.Problems[0] != want {
		t.Fatalf("Problems = %v, want exactly [%q]", f.Problems, want)
	}
	// Out of order is untidy, not unusable: every file is still intact.
	if err := Index(cfg); err != nil {
		t.Errorf("Index = %v, want nil", err)
	}
}
