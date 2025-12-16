package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"sift/internal/core"
)

// sampleEntry builds a deterministic entry for tests.
func sampleEntry(id core.DocID, hash string, terms ...string) Entry {
	tokens := make([]core.Token, 0, len(terms))
	for i, term := range terms {
		tokens = append(tokens, core.Token{Term: term, Position: i})
	}
	return Entry{
		ContentHash: hash,
		Doc: core.Document{
			ID:          id,
			Path:        string(id),
			Title:       "Title of " + string(id),
			Kind:        "markdown",
			Body:        "body of " + string(id),
			Fields:      map[string]string{"kind": "markdown", "dir": "docs"},
			Size:        int64(len(terms)),
			ContentHash: hash,
		},
		Tokens: tokens,
	}
}

// ids returns the sorted document IDs held by s.
func ids(s *Store) []string {
	out := make([]string, 0, len(s.Entries))
	for id := range s.Entries {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func TestGet(t *testing.T) {
	s := New()
	s.Put("docs/a.md", sampleEntry("docs/a.md", "hash-a", "alpha", "beta"))
	s.Put("docs/b.md", sampleEntry("docs/b.md", "hash-b", "gamma"))

	tests := []struct {
		name     string
		store    *Store
		id       core.DocID
		hash     string
		wantOK   bool
		wantLen  int
		wantTerm string
	}{
		{name: "hit", store: s, id: "docs/a.md", hash: "hash-a", wantOK: true, wantLen: 2, wantTerm: "alpha"},
		{name: "hit other doc", store: s, id: "docs/b.md", hash: "hash-b", wantOK: true, wantLen: 1, wantTerm: "gamma"},
		{name: "stale hash", store: s, id: "docs/a.md", hash: "hash-b", wantOK: false},
		{name: "empty hash", store: s, id: "docs/a.md", hash: "", wantOK: false},
		{name: "unknown id", store: s, id: "docs/missing.md", hash: "hash-a", wantOK: false},
		{name: "empty store", store: New(), id: "docs/a.md", hash: "hash-a", wantOK: false},
		{name: "nil map", store: &Store{}, id: "docs/a.md", hash: "hash-a", wantOK: false},
		{name: "nil store", store: nil, id: "docs/a.md", hash: "hash-a", wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := tt.store.Get(tt.id, tt.hash)
			if ok != tt.wantOK {
				t.Fatalf("Get(%q, %q) ok = %v, want %v", tt.id, tt.hash, ok, tt.wantOK)
			}
			if !tt.wantOK {
				if got.ContentHash != "" || got.Doc.ID != "" || got.Tokens != nil {
					t.Fatalf("miss returned %+v, want zero Entry", got)
				}
				return
			}
			if got.Doc.ID != tt.id {
				t.Errorf("Doc.ID = %q, want %q", got.Doc.ID, tt.id)
			}
			if got.ContentHash != tt.hash {
				t.Errorf("ContentHash = %q, want %q", got.ContentHash, tt.hash)
			}
			if len(got.Tokens) != tt.wantLen {
				t.Fatalf("len(Tokens) = %d, want %d", len(got.Tokens), tt.wantLen)
			}
			if got.Tokens[0].Term != tt.wantTerm || got.Tokens[0].Position != 0 {
				t.Errorf("Tokens[0] = %+v, want {%q 0}", got.Tokens[0], tt.wantTerm)
			}
		})
	}
}

func TestPutReplacesAndCopies(t *testing.T) {
	s := New()
	e := sampleEntry("docs/a.md", "hash-1", "alpha", "beta")
	s.Put("docs/a.md", e)

	// Mutating the caller's copy must not reach the cache.
	e.Tokens[0].Term = "mutated"
	e.Doc.Fields["kind"] = "mutated"
	got, ok := s.Get("docs/a.md", "hash-1")
	if !ok {
		t.Fatal("Get after Put: miss, want hit")
	}
	if got.Tokens[0].Term != "alpha" {
		t.Errorf("Tokens[0].Term = %q, want %q", got.Tokens[0].Term, "alpha")
	}
	if got.Doc.Fields["kind"] != "markdown" {
		t.Errorf("Fields[kind] = %q, want %q", got.Doc.Fields["kind"], "markdown")
	}

	// Mutating a returned entry must not reach the cache either.
	got.Tokens[1].Term = "mutated"
	got.Doc.Fields["dir"] = "mutated"
	again, _ := s.Get("docs/a.md", "hash-1")
	if again.Tokens[1].Term != "beta" || again.Doc.Fields["dir"] != "docs" {
		t.Errorf("cache changed through returned entry: %+v", again)
	}

	// A second Put replaces the entry rather than adding one.
	s.Put("docs/a.md", sampleEntry("docs/a.md", "hash-2", "gamma"))
	if len(s.Entries) != 1 {
		t.Fatalf("len(Entries) = %d, want 1", len(s.Entries))
	}
	if _, ok := s.Get("docs/a.md", "hash-1"); ok {
		t.Error("old hash still hits after replacement")
	}
	replaced, ok := s.Get("docs/a.md", "hash-2")
	if !ok || len(replaced.Tokens) != 1 || replaced.Tokens[0].Term != "gamma" {
		t.Errorf("replacement entry = %+v, ok = %v", replaced, ok)
	}

	// Put allocates the map of a zero store.
	zero := &Store{}
	zero.Put("docs/c.md", sampleEntry("docs/c.md", "hash-c", "delta"))
	if len(zero.Entries) != 1 {
		t.Errorf("zero store len(Entries) = %d, want 1", len(zero.Entries))
	}
}

func TestPrune(t *testing.T) {
	tests := []struct {
		name        string
		keep        map[core.DocID]bool
		wantRemoved int
		wantIDs     []string
	}{
		{
			name:        "keep all",
			keep:        map[core.DocID]bool{"a.md": true, "b.md": true, "c.md": true},
			wantRemoved: 0,
			wantIDs:     []string{"a.md", "b.md", "c.md"},
		},
		{
			name:        "keep subset",
			keep:        map[core.DocID]bool{"a.md": true, "c.md": true},
			wantRemoved: 1,
			wantIDs:     []string{"a.md", "c.md"},
		},
		{
			name:        "false counts as drop",
			keep:        map[core.DocID]bool{"a.md": true, "b.md": false, "c.md": false},
			wantRemoved: 2,
			wantIDs:     []string{"a.md"},
		},
		{
			name:        "unknown ids ignored",
			keep:        map[core.DocID]bool{"z.md": true},
			wantRemoved: 3,
			wantIDs:     nil,
		},
		{
			name:        "nil keep empties",
			keep:        nil,
			wantRemoved: 3,
			wantIDs:     nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := New()
			for _, id := range []core.DocID{"a.md", "b.md", "c.md"} {
				s.Put(id, sampleEntry(id, "hash-"+string(id), "alpha"))
			}
			if got := s.Prune(tt.keep); got != tt.wantRemoved {
				t.Fatalf("Prune removed %d, want %d", got, tt.wantRemoved)
			}
			got := ids(s)
			if strings.Join(got, ",") != strings.Join(tt.wantIDs, ",") {
				t.Fatalf("remaining %v, want %v", got, tt.wantIDs)
			}
			// Pruning again with the same keep set removes nothing more.
			if again := s.Prune(tt.keep); again != 0 {
				t.Errorf("second Prune removed %d, want 0", again)
			}
		})
	}

	var nilStore *Store
	if got := nilStore.Prune(nil); got != 0 {
		t.Errorf("nil store Prune = %d, want 0", got)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), core.CacheFile)
	s := New()
	s.Put("docs/a.md", sampleEntry("docs/a.md", "hash-a", "alpha", "beta", "alpha"))
	s.Put("src/b.go", sampleEntry("src/b.go", "hash-b", "gamma"))

	if err := Save(path, s); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded.Entries) != 2 {
		t.Fatalf("len(Entries) = %d, want 2", len(loaded.Entries))
	}
	got, ok := loaded.Get("docs/a.md", "hash-a")
	if !ok {
		t.Fatal("Get after Load: miss, want hit")
	}
	if got.Doc.Title != "Title of docs/a.md" || got.Doc.Kind != "markdown" {
		t.Errorf("Doc = %+v, want title %q kind %q", got.Doc, "Title of docs/a.md", "markdown")
	}
	if got.Doc.Fields["dir"] != "docs" {
		t.Errorf("Fields[dir] = %q, want %q", got.Doc.Fields["dir"], "docs")
	}
	if len(got.Tokens) != 3 || got.Tokens[2].Term != "alpha" || got.Tokens[2].Position != 2 {
		t.Errorf("Tokens = %+v, want 3 tokens ending {alpha 2}", got.Tokens)
	}
	if Digest(loaded) != Digest(s) {
		t.Errorf("Digest after round trip = %s, want %s", Digest(loaded), Digest(s))
	}

	// The saved bytes are canonical, and their SHA-256 is what Digest reports.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if len(data) == 0 || data[len(data)-1] != '\n' {
		t.Error("cache file does not end with a newline")
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != Digest(s) {
		t.Errorf("file digest = %s, want Digest = %s", hex.EncodeToString(sum[:]), Digest(s))
	}

	// Saving again over the same path replaces the file.
	s.Prune(map[core.DocID]bool{"src/b.go": true})
	if err := Save(path, s); err != nil {
		t.Fatalf("second Save: %v", err)
	}
	reloaded, err := Load(path)
	if err != nil {
		t.Fatalf("second Load: %v", err)
	}
	if len(reloaded.Entries) != 1 {
		t.Fatalf("len(Entries) after replace = %d, want 1", len(reloaded.Entries))
	}
	if _, ok := reloaded.Get("docs/a.md", "hash-a"); ok {
		t.Error("pruned document survived the rewrite")
	}
}

func TestLoadBadInputYieldsEmptyStore(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile %s: %v", name, err)
		}
		return p
	}
	tests := []struct {
		name string
		path string
	}{
		{name: "missing file", path: filepath.Join(dir, "absent.json")},
		{name: "directory", path: dir},
		{name: "empty file", path: write("empty.json", "")},
		{name: "garbage", path: write("garbage.json", "not json at all")},
		{name: "truncated", path: write("truncated.json", "{\"Entries\":{\"a.md\":{\"ContentHash\":\"h\"")},
		{name: "trailing junk", path: write("trailing.json", "{\"Entries\":{}} oops")},
		{name: "wrong shape", path: write("array.json", "[\"a.md\",\"b.md\"]")},
		{name: "wrong entry type", path: write("types.json", "{\"Entries\":{\"a.md\":\"nope\"}}")},
		{name: "null entries", path: write("null.json", "{\"Entries\":null}")},
		{name: "empty object", path: write("object.json", "{}")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, err := Load(tt.path)
			if err != nil {
				t.Fatalf("Load(%s) error = %v, want nil", tt.name, err)
			}
			if s == nil {
				t.Fatal("Load returned a nil store")
			}
			if len(s.Entries) != 0 {
				t.Fatalf("len(Entries) = %d, want 0", len(s.Entries))
			}
			if Digest(s) != Digest(New()) {
				t.Errorf("Digest = %s, want empty-cache digest %s", Digest(s), Digest(New()))
			}
			// The recovered store must be usable for a rebuild.
			s.Put("docs/a.md", sampleEntry("docs/a.md", "hash-a", "alpha"))
			if _, ok := s.Get("docs/a.md", "hash-a"); !ok {
				t.Error("recovered store rejected a Put")
			}
		})
	}
}

func TestSaveError(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	path := filepath.Join(blocker, core.CacheFile)

	err := Save(path, New())
	if err == nil {
		t.Fatal("Save below a plain file: nil error, want failure")
	}
	if !strings.Contains(err.Error(), "cache:") || !strings.Contains(err.Error(), path) {
		t.Errorf("error = %q, want it to name the package and %q", err.Error(), path)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Errorf("Stat(%s) = %v, want not-exist", path, statErr)
	}
	// The failed write left the existing file alone.
	kept, readErr := os.ReadFile(blocker)
	if readErr != nil {
		t.Fatalf("ReadFile: %v", readErr)
	}
	if string(kept) != "not a directory" {
		t.Errorf("blocker contents = %q, want them unchanged", string(kept))
	}
}

func TestDigest(t *testing.T) {
	empty := Digest(New())
	if len(empty) != 64 {
		t.Fatalf("len(Digest(New())) = %d, want 64", len(empty))
	}
	if empty != Digest(nil) || empty != Digest(&Store{}) {
		t.Errorf("nil and zero stores digest differently: %s %s %s", empty, Digest(nil), Digest(&Store{}))
	}

	// Insertion order must not change the digest.
	a := New()
	a.Put("docs/a.md", sampleEntry("docs/a.md", "hash-a", "alpha", "beta"))
	a.Put("src/b.go", sampleEntry("src/b.go", "hash-b", "gamma"))
	b := New()
	b.Put("src/b.go", sampleEntry("src/b.go", "hash-b", "gamma"))
	b.Put("docs/a.md", sampleEntry("docs/a.md", "hash-a", "alpha", "beta"))
	if Digest(a) != Digest(b) {
		t.Errorf("digest depends on insertion order: %s vs %s", Digest(a), Digest(b))
	}
	if Digest(a) == empty {
		t.Error("populated cache digests as an empty one")
	}

	// Any change to an entry changes the digest.
	c := New()
	c.Put("docs/a.md", sampleEntry("docs/a.md", "hash-a", "alpha", "beta"))
	c.Put("src/b.go", sampleEntry("src/b.go", "hash-b", "delta"))
	if Digest(c) == Digest(a) {
		t.Error("changed token did not change the digest")
	}

	// Pruning back to the same content restores the same digest.
	d := New()
	d.Put("docs/a.md", sampleEntry("docs/a.md", "hash-a", "alpha", "beta"))
	d.Put("src/b.go", sampleEntry("src/b.go", "hash-b", "gamma"))
	d.Put("tmp/c.txt", sampleEntry("tmp/c.txt", "hash-c", "epsilon"))
	if removed := d.Prune(map[core.DocID]bool{"docs/a.md": true, "src/b.go": true}); removed != 1 {
		t.Fatalf("Prune removed %d, want 1", removed)
	}
	if Digest(d) != Digest(a) {
		t.Errorf("digest after prune = %s, want %s", Digest(d), Digest(a))
	}
}

func TestEncodeIsCanonical(t *testing.T) {
	s := New()
	s.Put("b.md", sampleEntry("b.md", "hash-b", "gamma"))
	s.Put("a.md", sampleEntry("a.md", "hash-a", "alpha"))

	data, err := encode(s)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if !json.Valid(data) {
		t.Fatal("encode produced invalid JSON")
	}
	text := string(data)
	if want := "{\n  \"Entries\": {\n    \"a.md\": {\n"; !strings.HasPrefix(text, want) {
		t.Errorf("encode does not start with %q", want)
	}
	if !strings.HasSuffix(text, "\n") {
		t.Error("encode output does not end with a newline")
	}
	if strings.Contains(text, "\r") {
		t.Error("encode output contains a carriage return")
	}
	if strings.Index(text, "\"a.md\"") > strings.Index(text, "\"b.md\"") {
		t.Error("encode does not sort entries by document ID")
	}
	// Encoding is stable across calls.
	again, err := encode(s)
	if err != nil {
		t.Fatalf("encode again: %v", err)
	}
	if string(again) != text {
		t.Error("encode is not stable across calls")
	}
	// A nil store and an empty store encode identically.
	nilData, err := encode(nil)
	if err != nil {
		t.Fatalf("encode(nil): %v", err)
	}
	if string(nilData) != "{\n  \"Entries\": {}\n}\n" {
		t.Errorf("encode(nil) = %q, want empty cache JSON", string(nilData))
	}
}
