package manifest

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"sift/internal/core"
	"sift/internal/index"
	"sift/internal/store"
)

var fixed = time.Date(2024, 5, 17, 12, 30, 0, 0, time.UTC)

func testClock() core.Clock { return core.FixedClock{T: fixed} }

// writeFile writes body into dir and returns the digest the manifest should
// record for it.
func writeFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return store.SHA256Bytes([]byte(body))
}

// tokensFor turns terms into positioned tokens.
func tokensFor(terms ...string) []core.Token {
	out := make([]core.Token, len(terms))
	for i, term := range terms {
		out[i] = core.Token{Term: term, Position: i}
	}
	return out
}

// testIndex holds two documents over the three distinct terms alpha, beta and
// gamma.
func testIndex() *index.Index {
	idx := index.New()
	idx.Add(core.Document{
		ID:          "a.md",
		Path:        "a.md",
		Title:       "A",
		Kind:        "markdown",
		Body:        "alpha beta alpha",
		Fields:      map[string]string{"kind": "markdown"},
		ContentHash: "aa",
	}, tokensFor("alpha", "beta", "alpha"))
	idx.Add(core.Document{
		ID:          "b.md",
		Path:        "b.md",
		Title:       "B",
		Kind:        "markdown",
		Body:        "beta gamma",
		Fields:      map[string]string{"kind": "markdown"},
		ContentHash: "bb",
	}, tokensFor("beta", "gamma"))
	return idx
}

// generation writes a complete, valid generation into a fresh directory and
// returns the directory together with its manifest.
func generation(t *testing.T) (string, core.Manifest) {
	t.Helper()
	dir := t.TempDir()
	d1 := writeFile(t, dir, "seg-0001.json", `{"seg":1}`)
	d2 := writeFile(t, dir, "seg-0002.json", `{"seg":2}`)
	cd := writeFile(t, dir, core.CacheFile, `{"Entries":{}}`)
	m := core.Manifest{
		Generation: 4,
		Segments: []core.SegmentRef{
			{ID: "seg-0001", File: "seg-0001.json", Digest: d1, DocCount: 2, TermCount: 3},
			{ID: "seg-0002", File: "seg-0002.json", Digest: d2, DocCount: 1, TermCount: 2},
		},
		DocCount:    3,
		TermCount:   4,
		ConfigHash:  "cfg",
		CacheDigest: cd,
		CreatedAt:   fixed,
	}
	return dir, m
}

func TestBuildOrdersSegmentsAndCountsFromIndex(t *testing.T) {
	refs := []core.SegmentRef{
		{ID: "seg-0002", File: "seg-0002.json", Digest: "d2", DocCount: 1, TermCount: 2},
		{ID: "seg-0001", File: "seg-0001.json", Digest: "d1", DocCount: 2, TermCount: 3},
	}
	m := Build(9, refs, "cfg-hash", "cache-digest", testClock(), testIndex())

	if got := len(m.Segments); got != 2 {
		t.Fatalf("segments = %d, want 2", got)
	}
	got := []core.SegmentID{m.Segments[0].ID, m.Segments[1].ID}
	if !reflect.DeepEqual(got, []core.SegmentID{"seg-0001", "seg-0002"}) {
		t.Errorf("segment order = %v, want [seg-0001 seg-0002]", got)
	}
	if m.Generation != 9 {
		t.Errorf("generation = %d, want 9", m.Generation)
	}
	// Documents are partitioned across segments but terms are shared, so both
	// counts come from the index and are never summed over the references.
	if m.DocCount != 2 {
		t.Errorf("doc count = %d, want 2", m.DocCount)
	}
	if m.TermCount != 3 {
		t.Errorf("term count = %d, want 3", m.TermCount)
	}
	if m.ConfigHash != "cfg-hash" || m.CacheDigest != "cache-digest" {
		t.Errorf("hashes = %q/%q, want cfg-hash/cache-digest", m.ConfigHash, m.CacheDigest)
	}
	if !m.CreatedAt.Equal(fixed) {
		t.Errorf("created at = %v, want %v", m.CreatedAt, fixed)
	}
	if refs[0].ID != "seg-0002" {
		t.Errorf("Build reordered the slice of the caller: refs[0] = %s", refs[0].ID)
	}
}

func TestBuildIsRepeatableAndHandlesNilInputs(t *testing.T) {
	refs := []core.SegmentRef{
		{ID: "seg-0002", File: "seg-0002.json", Digest: "d2", DocCount: 3, TermCount: 5},
		{ID: "seg-0001", File: "seg-0001.json", Digest: "d1", DocCount: 2, TermCount: 4},
	}
	first := Build(1, refs, "cfg", "cache", testClock(), testIndex())
	second := Build(1, refs, "cfg", "cache", testClock(), testIndex())
	if !reflect.DeepEqual(first, second) {
		t.Errorf("Build is not repeatable:\n%+v\n%+v", first, second)
	}

	// Without an index the counts fall back to the segment totals.
	noIdx := Build(1, refs, "cfg", "cache", testClock(), nil)
	if noIdx.DocCount != 5 {
		t.Errorf("doc count = %d, want 5", noIdx.DocCount)
	}
	if noIdx.TermCount != 9 {
		t.Errorf("term count = %d, want 9", noIdx.TermCount)
	}

	// A nil clock falls back to wall-clock time instead of panicking.
	if got := Build(1, nil, "cfg", "cache", nil, nil); got.CreatedAt.IsZero() {
		t.Error("created at is zero with a nil clock")
	}
	if got := Build(1, nil, "cfg", "cache", testClock(), nil); len(got.Segments) != 0 {
		t.Errorf("segments = %d, want 0", len(got.Segments))
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "out")
	want := Build(7, []core.SegmentRef{
		{ID: "seg-0001", File: "seg-0001.json", Digest: "d1", DocCount: 2, TermCount: 3},
	}, "cfg", "cache", testClock(), testIndex())

	if err := Save(dir, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, core.ManifestFile))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if !json.Valid(raw) {
		t.Fatalf("manifest is not valid JSON: %s", raw)
	}
	if !strings.HasSuffix(string(raw), "\n") {
		t.Error("manifest does not end with a newline")
	}

	got, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Generation != 7 || got.DocCount != 2 || got.TermCount != 3 {
		t.Errorf("got gen=%d docs=%d terms=%d, want 7/2/3", got.Generation, got.DocCount, got.TermCount)
	}
	if got.ConfigHash != "cfg" || got.CacheDigest != "cache" {
		t.Errorf("got hashes %q/%q, want cfg/cache", got.ConfigHash, got.CacheDigest)
	}
	if !reflect.DeepEqual(got.Segments, want.Segments) {
		t.Errorf("segments = %+v, want %+v", got.Segments, want.Segments)
	}
	if !got.CreatedAt.Equal(fixed) {
		t.Errorf("created at = %v, want %v", got.CreatedAt, fixed)
	}

	// Saving again over an existing manifest replaces it in place.
	want.Generation = 8
	if err := Save(dir, want); err != nil {
		t.Fatalf("second Save: %v", err)
	}
	if got, err = Load(dir); err != nil || got.Generation != 8 {
		t.Fatalf("after resave: gen=%d err=%v, want 8/nil", got.Generation, err)
	}
}

func TestLoadMissingAndMalformed(t *testing.T) {
	dir := t.TempDir()
	if _, err := Load(dir); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("missing manifest: err = %v, want core.ErrNotFound", err)
	}
	if gen, ok := Current(dir); ok || gen != 0 {
		t.Errorf("Current on empty dir = (%d, %v), want (0, false)", gen, ok)
	}

	if err := os.WriteFile(filepath.Join(dir, core.ManifestFile), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := Load(dir); err == nil {
		t.Error("malformed manifest: err = nil, want an error")
	}
	if gen, ok := Current(dir); ok || gen != 0 {
		t.Errorf("Current on malformed manifest = (%d, %v), want (0, false)", gen, ok)
	}
}

func TestCurrentReportsSavedGeneration(t *testing.T) {
	dir := t.TempDir()
	for _, gen := range []core.Generation{1, 2, 12} {
		if err := Save(dir, Build(gen, nil, "cfg", "cache", testClock(), nil)); err != nil {
			t.Fatalf("Save gen %d: %v", gen, err)
		}
		got, ok := Current(dir)
		if !ok || got != gen {
			t.Errorf("Current = (%d, %v), want (%d, true)", got, ok, gen)
		}
	}
}

func TestValidateAcceptsCompleteGeneration(t *testing.T) {
	dir, m := generation(t)
	if err := Validate(dir, m); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	// An empty generation is valid as long as the cache is intact.
	empty := t.TempDir()
	cd := writeFile(t, empty, core.CacheFile, `{"Entries":{}}`)
	if err := Validate(empty, core.Manifest{Generation: 1, CacheDigest: cd}); err != nil {
		t.Fatalf("Validate empty generation: %v", err)
	}

	// Unreferenced extra files in the directory are ignored.
	writeFile(t, dir, "seg-0009.json", `{"seg":9}`)
	if err := Validate(dir, m); err != nil {
		t.Fatalf("Validate with extra file: %v", err)
	}
}

func TestValidateNamesFirstOffendingPath(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(t *testing.T, dir string, m *core.Manifest)
		wantPath   string
		wantReason string
	}{
		{
			name: "missing segment file",
			mutate: func(t *testing.T, dir string, m *core.Manifest) {
				if err := os.Remove(filepath.Join(dir, "seg-0002.json")); err != nil {
					t.Fatalf("remove: %v", err)
				}
			},
			wantPath:   "seg-0002.json",
			wantReason: "missing",
		},
		{
			name: "segment digest mismatch",
			mutate: func(t *testing.T, dir string, m *core.Manifest) {
				writeFile(t, dir, "seg-0002.json", `{"seg":"tampered"}`)
			},
			wantPath:   "seg-0002.json",
			wantReason: "digest mismatch",
		},
		{
			name: "earliest broken segment wins",
			mutate: func(t *testing.T, dir string, m *core.Manifest) {
				writeFile(t, dir, "seg-0001.json", `{"seg":"tampered"}`)
				if err := os.Remove(filepath.Join(dir, "seg-0002.json")); err != nil {
					t.Fatalf("remove: %v", err)
				}
			},
			wantPath:   "seg-0001.json",
			wantReason: "digest mismatch",
		},
		{
			name: "segment without a recorded digest",
			mutate: func(t *testing.T, dir string, m *core.Manifest) {
				m.Segments[0].Digest = ""
			},
			wantPath:   "seg-0001.json",
			wantReason: "no digest recorded",
		},
		{
			name: "segment file name empty",
			mutate: func(t *testing.T, dir string, m *core.Manifest) {
				m.Segments[0].File = ""
			},
			wantPath:   core.ManifestFile,
			wantReason: "empty file name",
		},
		{
			name: "segment file escapes the output directory",
			mutate: func(t *testing.T, dir string, m *core.Manifest) {
				m.Segments[0].File = "../seg-0001.json"
			},
			wantPath:   "../seg-0001.json",
			wantReason: "escapes the output directory",
		},
		{
			name: "segment id empty",
			mutate: func(t *testing.T, dir string, m *core.Manifest) {
				m.Segments[1].ID = ""
			},
			wantPath:   "seg-0002.json",
			wantReason: "empty segment id",
		},
		{
			name: "duplicate segment id",
			mutate: func(t *testing.T, dir string, m *core.Manifest) {
				m.Segments[1].ID = m.Segments[0].ID
			},
			wantPath:   "seg-0002.json",
			wantReason: "duplicate segment id seg-0001",
		},
		{
			name: "segment path is a directory",
			mutate: func(t *testing.T, dir string, m *core.Manifest) {
				if err := os.Remove(filepath.Join(dir, "seg-0001.json")); err != nil {
					t.Fatalf("remove: %v", err)
				}
				if err := os.Mkdir(filepath.Join(dir, "seg-0001.json"), 0o755); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
			},
			wantPath:   "seg-0001.json",
			wantReason: "not a regular file",
		},
		{
			name: "cache missing",
			mutate: func(t *testing.T, dir string, m *core.Manifest) {
				if err := os.Remove(filepath.Join(dir, core.CacheFile)); err != nil {
					t.Fatalf("remove: %v", err)
				}
			},
			wantPath:   core.CacheFile,
			wantReason: "missing",
		},
		{
			name: "cache digest mismatch",
			mutate: func(t *testing.T, dir string, m *core.Manifest) {
				writeFile(t, dir, core.CacheFile, `{"Entries":{"a.md":{}}}`)
			},
			wantPath:   core.CacheFile,
			wantReason: "digest mismatch",
		},
		{
			name: "cache digest not recorded",
			mutate: func(t *testing.T, dir string, m *core.Manifest) {
				m.CacheDigest = ""
			},
			wantPath:   core.CacheFile,
			wantReason: "no digest recorded",
		},
		{
			name: "doc count disagrees with the segments",
			mutate: func(t *testing.T, dir string, m *core.Manifest) {
				m.DocCount = 5
			},
			wantPath:   core.ManifestFile,
			wantReason: "doc count 5 but segments hold 3",
		},
		{
			name: "term count below the largest segment",
			mutate: func(t *testing.T, dir string, m *core.Manifest) {
				m.TermCount = 1
			},
			wantPath:   core.ManifestFile,
			wantReason: "term count 1 outside segment range 3..5",
		},
		{
			name: "term count above the segment total",
			mutate: func(t *testing.T, dir string, m *core.Manifest) {
				m.TermCount = 6
			},
			wantPath:   core.ManifestFile,
			wantReason: "term count 6 outside segment range 3..5",
		},
		{
			name: "broken segment is reported before the counts",
			mutate: func(t *testing.T, dir string, m *core.Manifest) {
				writeFile(t, dir, "seg-0001.json", `{"seg":"tampered"}`)
				m.DocCount = 5
			},
			wantPath:   "seg-0001.json",
			wantReason: "digest mismatch",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir, m := generation(t)
			tt.mutate(t, dir, &m)

			err := Validate(dir, m)
			if err == nil {
				t.Fatal("Validate: err = nil, want an integrity error")
			}
			if !errors.Is(err, core.ErrIntegrity) {
				t.Errorf("errors.Is(err, core.ErrIntegrity) = false for %v", err)
			}
			var ie *core.IntegrityError
			if !errors.As(err, &ie) {
				t.Fatalf("err = %v (%T), want *core.IntegrityError", err, err)
			}
			if ie.Path != tt.wantPath {
				t.Errorf("path = %q, want %q", ie.Path, tt.wantPath)
			}
			if ie.Reason != tt.wantReason {
				t.Errorf("reason = %q, want %q", ie.Reason, tt.wantReason)
			}
			if !strings.Contains(ie.Error(), tt.wantPath) {
				t.Errorf("message %q does not name the path %q", ie.Error(), tt.wantPath)
			}
		})
	}
}

func TestValidateAfterSaveLoadCycle(t *testing.T) {
	dir, m := generation(t)
	if err := Save(dir, m); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := Validate(dir, loaded); err != nil {
		t.Fatalf("Validate after round trip: %v", err)
	}
	gen, ok := Current(dir)
	if !ok || gen != 4 {
		t.Fatalf("Current = (%d, %v), want (4, true)", gen, ok)
	}

	// Truncating one segment invalidates the generation that was just read.
	writeFile(t, dir, "seg-0001.json", "")
	if err := Validate(dir, loaded); !errors.Is(err, core.ErrIntegrity) {
		t.Errorf("Validate after truncation: err = %v, want core.ErrIntegrity", err)
	}
}
