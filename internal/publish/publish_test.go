package publish

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"sift/internal/cache"
	"sift/internal/config"
	"sift/internal/core"
	"sift/internal/index"
	"sift/internal/store"
)

// fixedTime is the publication time every test publishes with, so manifests
// are byte-identical across runs.
var fixedTime = time.Date(2024, 5, 17, 9, 30, 0, 0, time.UTC)

// testConfig returns a configuration rooted in a fresh temporary directory,
// with segments small enough that several of them are written.
func testConfig(t *testing.T) core.Config {
	t.Helper()
	cfg := config.Default(t.TempDir())
	cfg.SegmentDocs = 2
	return cfg
}

// testIndex builds an index whose documents share two terms and contribute
// one term of their own each, so counts are easy to predict.
func testIndex(ids ...string) *index.Index {
	ix := index.New()
	for i, id := range ids {
		doc := core.Document{
			ID:          core.DocID(id),
			Path:        id,
			Title:       "title of " + id,
			Kind:        "text",
			Fields:      map[string]string{"kind": "text"},
			ContentHash: fmt.Sprintf("%064d", i+1),
		}
		terms := []string{"alpha", "beta", "term" + strconv.Itoa(i)}
		tokens := make([]core.Token, 0, len(terms))
		for pos, term := range terms {
			tokens = append(tokens, core.Token{Term: term, Position: pos})
		}
		ix.Add(doc, tokens)
	}
	return ix
}

// testCache returns a cache store holding one entry per id.
func testCache(ids ...string) *cache.Store {
	s := cache.New()
	for i, id := range ids {
		s.Put(core.DocID(id), cache.Entry{ContentHash: fmt.Sprintf("%064d", i+1)})
	}
	return s
}

// mustPublish publishes with the fixed clock and fails the test on error.
func mustPublish(t *testing.T, cfg core.Config, ix *index.Index, cs *cache.Store) core.Manifest {
	t.Helper()
	m, err := Publish(cfg, ix, cs, core.FixedClock{T: fixedTime})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	return m
}

// mustRead reads a file below the output directory and fails on error.
func mustRead(t *testing.T, out, rel string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(out, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return data
}

// assertAbsent fails when rel exists below the output directory.
func assertAbsent(t *testing.T, out, rel string) {
	t.Helper()
	if _, err := os.Lstat(filepath.Join(out, filepath.FromSlash(rel))); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("%s: want it gone, got err %v", rel, err)
	}
}

func TestPublishWritesCompleteGeneration(t *testing.T) {
	cfg := testConfig(t)
	out := OutputPath(cfg)
	m := mustPublish(t, cfg, testIndex("a.txt", "b.txt", "c.txt"), testCache("a.txt", "b.txt"))

	if m.Generation != 1 {
		t.Errorf("Generation = %d, want 1", m.Generation)
	}
	if m.DocCount != 3 || m.TermCount != 5 {
		t.Errorf("DocCount, TermCount = %d, %d, want 3, 5", m.DocCount, m.TermCount)
	}
	if !m.CreatedAt.Equal(fixedTime) {
		t.Errorf("CreatedAt = %v, want %v", m.CreatedAt, fixedTime)
	}
	if m.ConfigHash != config.Hash(cfg) {
		t.Errorf("ConfigHash = %q, want %q", m.ConfigHash, config.Hash(cfg))
	}
	if len(m.Segments) != 2 {
		t.Fatalf("len(Segments) = %d, want 2", len(m.Segments))
	}
	wantFiles := []string{"gen-0001/seg-0001.json", "gen-0001/seg-0002.json"}
	wantDocs := []int{2, 1}
	for i, ref := range m.Segments {
		if ref.File != wantFiles[i] {
			t.Errorf("Segments[%d].File = %q, want %q", i, ref.File, wantFiles[i])
		}
		if ref.DocCount != wantDocs[i] {
			t.Errorf("Segments[%d].DocCount = %d, want %d", i, ref.DocCount, wantDocs[i])
		}
		if ref.Digest == "" {
			t.Errorf("Segments[%d].Digest is empty", i)
		}
	}
	for _, rel := range append(wantFiles, core.ManifestFile, core.CacheFile) {
		if _, err := os.Stat(filepath.Join(out, filepath.FromSlash(rel))); err != nil {
			t.Errorf("published file %s: %v", rel, err)
		}
	}
	assertAbsent(t, out, StagingDir)
	// The generation directory holds segments and nothing else: the cache
	// belongs to the output directory, where the manifest expects it.
	assertAbsent(t, out, "gen-0001/"+core.CacheFile)

	digest, err := store.SHA256File(filepath.Join(out, core.CacheFile))
	if err != nil {
		t.Fatalf("hash cache: %v", err)
	}
	if digest != m.CacheDigest {
		t.Errorf("CacheDigest = %q, want the digest of the published cache %q", m.CacheDigest, digest)
	}
}

func TestPublishNextGenerationReplacesTheSegmentsOfTheOldOne(t *testing.T) {
	cfg := testConfig(t)
	out := OutputPath(cfg)
	ix := testIndex("a.txt", "b.txt", "c.txt")
	first := mustPublish(t, cfg, ix, testCache("a.txt"))
	if err := store.EnsureDir(filepath.Join(out, "notes")); err != nil {
		t.Fatalf("create unrelated directory: %v", err)
	}

	second := mustPublish(t, cfg, ix, testCache("a.txt"))
	if second.Generation != 2 {
		t.Fatalf("Generation = %d, want 2", second.Generation)
	}
	if got, want := second.Segments[0].File, "gen-0002/seg-0001.json"; got != want {
		t.Errorf("Segments[0].File = %q, want %q", got, want)
	}
	if second.Segments[0].Digest != first.Segments[0].Digest {
		t.Errorf("same index published twice gave digests %q and %q, want them equal",
			first.Segments[0].Digest, second.Segments[0].Digest)
	}
	assertAbsent(t, out, GenerationDir(1))
	if _, err := os.Stat(filepath.Join(out, GenerationDir(2), "seg-0002.json")); err != nil {
		t.Errorf("generation 2 segment: %v", err)
	}
	if _, err := os.Stat(filepath.Join(out, "notes")); err != nil {
		t.Errorf("pruning removed an unrelated directory: %v", err)
	}
}

func TestLoadReturnsThePublishedIndex(t *testing.T) {
	cfg := testConfig(t)
	published := mustPublish(t, cfg, testIndex("a.txt", "b.txt", "c.txt"), testCache("a.txt"))

	loaded, m, err := Load(cfg)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.DocCount() != 3 || loaded.TermCount() != 5 {
		t.Errorf("DocCount, TermCount = %d, %d, want 3, 5", loaded.DocCount(), loaded.TermCount())
	}
	if got := loaded.AvgDocLength(); got != 3 {
		t.Errorf("AvgDocLength = %v, want 3", got)
	}
	docs := loaded.Docs()
	wantIDs := []core.DocID{"a.txt", "b.txt", "c.txt"}
	if len(docs) != len(wantIDs) {
		t.Fatalf("len(Docs) = %d, want %d", len(docs), len(wantIDs))
	}
	for i, want := range wantIDs {
		if docs[i].ID != want {
			t.Errorf("Docs[%d].ID = %q, want %q", i, docs[i].ID, want)
		}
	}
	if got, want := docs[0].Title, "title of a.txt"; got != want {
		t.Errorf("Docs[0].Title = %q, want %q", got, want)
	}
	if got, want := docs[0].Fields["kind"], "text"; got != want {
		t.Errorf("Docs[0].Fields[kind] = %q, want %q", got, want)
	}
	postings := loaded.Postings("alpha")
	if len(postings) != 3 {
		t.Fatalf("len(Postings(alpha)) = %d, want 3", len(postings))
	}
	for i, p := range postings {
		if p.DocID != wantIDs[i] || p.Freq != 1 {
			t.Errorf("Postings(alpha)[%d] = %q freq %d, want %q freq 1", i, p.DocID, p.Freq, wantIDs[i])
		}
	}
	if m.Generation != published.Generation || len(m.Segments) != len(published.Segments) {
		t.Errorf("loaded manifest = generation %d with %d segments, want %d with %d",
			m.Generation, len(m.Segments), published.Generation, len(published.Segments))
	}
}

func TestPublishAnEmptyIndex(t *testing.T) {
	cfg := testConfig(t)
	out := OutputPath(cfg)
	m := mustPublish(t, cfg, nil, nil)

	if m.Generation != 1 || m.DocCount != 0 || len(m.Segments) != 0 {
		t.Errorf("manifest = generation %d, %d docs, %d segments, want 1, 0, 0",
			m.Generation, m.DocCount, len(m.Segments))
	}
	if _, err := os.Stat(filepath.Join(out, core.CacheFile)); err != nil {
		t.Errorf("cache file: %v", err)
	}
	loaded, _, err := Load(cfg)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.DocCount() != 0 {
		t.Errorf("DocCount = %d, want 0", loaded.DocCount())
	}
}

func TestPublishLeavesThePreviousGenerationOnFailure(t *testing.T) {
	cfg := testConfig(t)
	out := OutputPath(cfg)
	first := mustPublish(t, cfg, testIndex("a.txt", "b.txt", "c.txt"), testCache("a.txt"))
	manifestBefore := mustRead(t, out, core.ManifestFile)
	cacheBefore := mustRead(t, out, core.CacheFile)
	segmentBefore := mustRead(t, out, first.Segments[0].File)

	// A file where the next generation directory has to go stops the swap.
	blocker := filepath.Join(out, GenerationDir(2))
	if err := store.WriteFileAtomic(blocker, []byte("not a directory\n"), 0o644); err != nil {
		t.Fatalf("write blocker: %v", err)
	}

	if _, err := Publish(cfg, testIndex("d.txt"), testCache("d.txt"), core.FixedClock{T: fixedTime}); err == nil {
		t.Fatal("Publish succeeded, want a failure")
	} else if !errors.Is(err, core.ErrIntegrity) {
		t.Errorf("error = %v, want it to match core.ErrIntegrity", err)
	} else {
		var damaged *core.IntegrityError
		if !errors.As(err, &damaged) {
			t.Fatalf("error = %v, want a *core.IntegrityError", err)
		}
		if damaged.Path != GenerationDir(2) || damaged.Reason != "not a directory" {
			t.Errorf("error = %+v, want path %q reason %q", damaged, GenerationDir(2), "not a directory")
		}
	}

	assertAbsent(t, out, StagingDir)
	if got := mustRead(t, out, core.ManifestFile); string(got) != string(manifestBefore) {
		t.Error("the manifest changed although publishing failed")
	}
	if got := mustRead(t, out, core.CacheFile); string(got) != string(cacheBefore) {
		t.Error("the extraction cache changed although publishing failed")
	}
	if got := mustRead(t, out, first.Segments[0].File); string(got) != string(segmentBefore) {
		t.Error("a published segment changed although publishing failed")
	}
	loaded, m, err := Load(cfg)
	if err != nil {
		t.Fatalf("Load after a failed publish: %v", err)
	}
	if m.Generation != 1 || loaded.DocCount() != 3 {
		t.Errorf("loaded generation %d with %d docs, want 1 with 3", m.Generation, loaded.DocCount())
	}
}

func TestPublishRemovesALeftoverStagingDirectory(t *testing.T) {
	cfg := testConfig(t)
	out := OutputPath(cfg)
	mustPublish(t, cfg, testIndex("a.txt"), testCache("a.txt"))

	leftover := filepath.Join(out, StagingDir, "seg-0009.json")
	if err := store.WriteFileAtomic(leftover, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write leftover: %v", err)
	}

	m := mustPublish(t, cfg, testIndex("a.txt", "b.txt"), testCache("a.txt", "b.txt"))
	if m.Generation != 2 {
		t.Errorf("Generation = %d, want 2", m.Generation)
	}
	assertAbsent(t, out, StagingDir)
	assertAbsent(t, out, "gen-0002/seg-0009.json")
}

func TestPublishRestoresTheCacheWhenTheManifestCannotBeWritten(t *testing.T) {
	cfg := testConfig(t)
	out := OutputPath(cfg)
	mustPublish(t, cfg, testIndex("a.txt"), testCache("a.txt"))
	cacheBefore := mustRead(t, out, core.CacheFile)

	// A directory where the manifest has to go fails the commit at the very
	// last step, after the new cache has already been installed.
	if err := os.Remove(filepath.Join(out, core.ManifestFile)); err != nil {
		t.Fatalf("remove manifest: %v", err)
	}
	if err := store.EnsureDir(filepath.Join(out, core.ManifestFile)); err != nil {
		t.Fatalf("create blocker: %v", err)
	}

	if _, err := Publish(cfg, testIndex("x.txt", "y.txt"), testCache("x.txt", "y.txt"), core.FixedClock{T: fixedTime}); err == nil {
		t.Fatal("Publish succeeded, want a failure")
	}
	if got := mustRead(t, out, core.CacheFile); string(got) != string(cacheBefore) {
		t.Error("the extraction cache was not restored after the commit failed")
	}
	assertAbsent(t, out, StagingDir)
	assertAbsent(t, out, GenerationDir(1))
}

func TestLoadReportsADamagedGeneration(t *testing.T) {
	cases := []struct {
		name    string
		damage  func(t *testing.T, out string, m core.Manifest)
		wantErr error
		wantPtr string
	}{
		{
			name:    "no manifest at all",
			damage:  func(t *testing.T, out string, m core.Manifest) {},
			wantErr: core.ErrNotFound,
		},
		{
			name: "segment contents changed",
			damage: func(t *testing.T, out string, m core.Manifest) {
				path := filepath.Join(out, filepath.FromSlash(m.Segments[0].File))
				if err := store.WriteFileAtomic(path, []byte("{}\n"), 0o644); err != nil {
					t.Fatalf("damage segment: %v", err)
				}
			},
			wantErr: core.ErrIntegrity,
			wantPtr: "gen-0001/seg-0001.json",
		},
		{
			name: "segment deleted",
			damage: func(t *testing.T, out string, m core.Manifest) {
				if err := os.Remove(filepath.Join(out, filepath.FromSlash(m.Segments[1].File))); err != nil {
					t.Fatalf("delete segment: %v", err)
				}
			},
			wantErr: core.ErrIntegrity,
			wantPtr: "gen-0001/seg-0002.json",
		},
		{
			name: "cache deleted",
			damage: func(t *testing.T, out string, m core.Manifest) {
				if err := os.Remove(filepath.Join(out, core.CacheFile)); err != nil {
					t.Fatalf("delete cache: %v", err)
				}
			},
			wantErr: core.ErrIntegrity,
			wantPtr: core.CacheFile,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testConfig(t)
			out := OutputPath(cfg)
			var m core.Manifest
			if tc.name != "no manifest at all" {
				m = mustPublish(t, cfg, testIndex("a.txt", "b.txt", "c.txt"), testCache("a.txt"))
			}
			tc.damage(t, out, m)

			idx, got, err := Load(cfg)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Load error = %v, want it to match %v", err, tc.wantErr)
			}
			if idx != nil {
				t.Errorf("Load returned an index of %d documents, want nil", idx.DocCount())
			}
			if got.Generation != 0 {
				t.Errorf("Load returned generation %d, want the zero manifest", got.Generation)
			}
			if tc.wantPtr == "" {
				return
			}
			var damaged *core.IntegrityError
			if !errors.As(err, &damaged) {
				t.Fatalf("error = %v, want a *core.IntegrityError", err)
			}
			if damaged.Path != tc.wantPtr {
				t.Errorf("IntegrityError.Path = %q, want %q", damaged.Path, tc.wantPtr)
			}
		})
	}
}

func TestGenerationDirNames(t *testing.T) {
	cases := []struct {
		gen  core.Generation
		want string
	}{
		{1, "gen-0001"},
		{9, "gen-0009"},
		{42, "gen-0042"},
		{1000, "gen-1000"},
		{12345, "gen-12345"},
	}
	for _, tc := range cases {
		if got := GenerationDir(tc.gen); got != tc.want {
			t.Errorf("GenerationDir(%d) = %q, want %q", tc.gen, got, tc.want)
		}
		if !isGenerationDir(tc.want) {
			t.Errorf("isGenerationDir(%q) = false, want true", tc.want)
		}
	}
	for _, name := range []string{"gen-", "gen-x", "gen-00a1", "generations", "seg-0001", ".staging", ""} {
		if isGenerationDir(name) {
			t.Errorf("isGenerationDir(%q) = true, want false", name)
		}
	}
}

func TestOutputPathAppliesTheDefault(t *testing.T) {
	root := filepath.Join("corpus", "root")
	cases := []struct {
		outputDir string
		want      string
	}{
		{"", filepath.Join(root, core.DefaultOutputDir)},
		{"sift-out", filepath.Join(root, "sift-out")},
		{"build/index", filepath.Join(root, "build", "index")},
	}
	for _, tc := range cases {
		cfg := core.Config{Root: root, OutputDir: tc.outputDir}
		if got := OutputPath(cfg); got != tc.want {
			t.Errorf("OutputPath(%q) = %q, want %q", tc.outputDir, got, tc.want)
		}
	}
}
