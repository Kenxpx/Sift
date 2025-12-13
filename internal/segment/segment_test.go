package segment

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"sift/internal/core"
	"sift/internal/index"
)

// sampleDoc describes one document to feed into a test index: its identity
// and the exact token stream it contributes, so every expected count in the
// tests below can be worked out by hand.
type sampleDoc struct {
	id    core.DocID
	title string
	kind  string
	lang  string
	terms []string
}

// corpus is the five-document fixture the tests share.
//
//	a.md alpha beta alpha
//	b.md beta gamma
//	c.md alpha delta
//	d.md gamma
//	e.md epsilon epsilon beta
//
// Globally that is alpha 2/3, beta 3/3, delta 1/1, epsilon 1/2, gamma 2/2
// (DocFreq/TotalFreq) over 11 tokens.
func corpus() []sampleDoc {
	return []sampleDoc{
		{id: "a.md", title: "Alpha", kind: "markdown", terms: []string{"alpha", "beta", "alpha"}},
		{id: "b.md", title: "Bravo", kind: "markdown", terms: []string{"beta", "gamma"}},
		{id: "c.md", title: "Charlie", kind: "source", lang: "go", terms: []string{"alpha", "delta"}},
		{id: "d.md", title: "Delta", kind: "source", lang: "go", terms: []string{"gamma"}},
		{id: "e.md", title: "Echo", kind: "text", terms: []string{"epsilon", "epsilon", "beta"}},
	}
}

// buildIndex indexes samples in the order given.
func buildIndex(t *testing.T, samples ...sampleDoc) *index.Index {
	t.Helper()
	idx := index.New()
	for _, s := range samples {
		fields := map[string]string{"kind": s.kind, "dir": ".", "ext": ".md"}
		if s.lang != "" {
			fields["language"] = s.lang
		}
		sum := sha256.Sum256([]byte(strings.Join(s.terms, " ")))
		doc := core.Document{
			ID:          s.id,
			Path:        string(s.id),
			Title:       s.title,
			Kind:        s.kind,
			Language:    s.lang,
			Body:        strings.Join(s.terms, " "),
			Fields:      fields,
			Size:        int64(len(s.terms)),
			ContentHash: hex.EncodeToString(sum[:]),
		}
		tokens := make([]core.Token, len(s.terms))
		for i, term := range s.terms {
			tokens[i] = core.Token{Term: term, Position: i}
		}
		idx.Add(doc, tokens)
	}
	return idx
}

// docIDs lists the documents of a segment.
func docIDs(seg *Segment) []core.DocID {
	out := make([]core.DocID, len(seg.Docs))
	for i, d := range seg.Docs {
		out[i] = d.ID
	}
	return out
}

func TestSplitAssignsDocumentsInOrder(t *testing.T) {
	idx := buildIndex(t, corpus()...)
	segs := Split(idx, 2)
	if len(segs) != 3 {
		t.Fatalf("segment count = %d, want 3", len(segs))
	}
	wantIDs := []core.SegmentID{"seg-0001", "seg-0002", "seg-0003"}
	wantDocs := [][]core.DocID{{"a.md", "b.md"}, {"c.md", "d.md"}, {"e.md"}}
	wantTerms := []int{3, 3, 2}
	for i, seg := range segs {
		if seg.Ref.ID != wantIDs[i] {
			t.Errorf("segment %d id = %q, want %q", i, seg.Ref.ID, wantIDs[i])
		}
		if want := string(wantIDs[i]) + ".json"; seg.Ref.File != want {
			t.Errorf("segment %d file = %q, want %q", i, seg.Ref.File, want)
		}
		if seg.Ref.Digest != "" {
			t.Errorf("segment %d digest = %q, want empty before Write", i, seg.Ref.Digest)
		}
		if got := docIDs(seg); !reflect.DeepEqual(got, wantDocs[i]) {
			t.Errorf("segment %d docs = %v, want %v", i, got, wantDocs[i])
		}
		if seg.Ref.DocCount != len(wantDocs[i]) {
			t.Errorf("segment %d DocCount = %d, want %d", i, seg.Ref.DocCount, len(wantDocs[i]))
		}
		if seg.Ref.TermCount != wantTerms[i] || len(seg.Terms) != wantTerms[i] {
			t.Errorf("segment %d TermCount = %d (%d terms), want %d", i, seg.Ref.TermCount, len(seg.Terms), wantTerms[i])
		}
	}
}

func TestSplitRecomputesSegmentLocalTermStats(t *testing.T) {
	idx := buildIndex(t, corpus()...)
	segs := Split(idx, 2)
	want := [][]core.TermStats{
		{{Term: "alpha", DocFreq: 1, TotalFreq: 2}, {Term: "beta", DocFreq: 2, TotalFreq: 2}, {Term: "gamma", DocFreq: 1, TotalFreq: 1}},
		{{Term: "alpha", DocFreq: 1, TotalFreq: 1}, {Term: "delta", DocFreq: 1, TotalFreq: 1}, {Term: "gamma", DocFreq: 1, TotalFreq: 1}},
		{{Term: "beta", DocFreq: 1, TotalFreq: 1}, {Term: "epsilon", DocFreq: 1, TotalFreq: 2}},
	}
	for i, seg := range segs {
		if !reflect.DeepEqual(seg.Terms, want[i]) {
			t.Errorf("segment %d terms = %+v, want %+v", i, seg.Terms, want[i])
		}
	}
	// The global statistics must not have leaked into any segment: alpha is
	// in two documents overall but in one document per segment.
	for _, ts := range idx.Terms() {
		if ts.Term == "alpha" && (ts.DocFreq != 2 || ts.TotalFreq != 3) {
			t.Fatalf("index alpha = %+v, want DocFreq 2 TotalFreq 3", ts)
		}
	}
}

func TestSplitCopiesPostingsAndFields(t *testing.T) {
	idx := buildIndex(t, corpus()...)
	segs := Split(idx, 100)
	if len(segs) != 1 {
		t.Fatalf("segment count = %d, want 1", len(segs))
	}
	seg := segs[0]
	want := []core.Posting{
		{DocID: "a.md", Freq: 2, Positions: []int{0, 2}},
		{DocID: "c.md", Freq: 1, Positions: []int{0}},
	}
	if got := seg.Postings["alpha"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("alpha postings = %+v, want %+v", got, want)
	}
	seg.Postings["alpha"][0].Positions[0] = 99
	seg.Docs[0].Fields["kind"] = "mutated"
	if got := idx.Postings("alpha"); got[0].Positions[0] != 0 {
		t.Errorf("index posting positions = %v, want the segment copy to be independent", got[0].Positions)
	}
	info, ok := idx.Doc("a.md")
	if !ok {
		t.Fatal("index lost a.md")
	}
	if info.Fields["kind"] != "markdown" {
		t.Errorf("index kind = %q, want %q: segment shares the field map", info.Fields["kind"], "markdown")
	}
}

func TestSplitBoundaries(t *testing.T) {
	full := buildIndex(t, corpus()...)
	tests := []struct {
		name     string
		idx      *index.Index
		maxDocs  int
		wantSegs []int // documents per segment
	}{
		{name: "nil index", idx: nil, maxDocs: 2},
		{name: "empty index", idx: index.New(), maxDocs: 2},
		{name: "zero max is one segment", idx: full, maxDocs: 0, wantSegs: []int{5}},
		{name: "negative max is one segment", idx: full, maxDocs: -7, wantSegs: []int{5}},
		{name: "one doc per segment", idx: full, maxDocs: 1, wantSegs: []int{1, 1, 1, 1, 1}},
		{name: "exact multiple", idx: full, maxDocs: 5, wantSegs: []int{5}},
		{name: "max above corpus", idx: full, maxDocs: 9, wantSegs: []int{5}},
		{name: "uneven tail", idx: full, maxDocs: 3, wantSegs: []int{3, 2}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			segs := Split(tc.idx, tc.maxDocs)
			if len(segs) != len(tc.wantSegs) {
				t.Fatalf("segment count = %d, want %d", len(segs), len(tc.wantSegs))
			}
			seen := 0
			for i, seg := range segs {
				if len(seg.Docs) != tc.wantSegs[i] {
					t.Errorf("segment %d holds %d docs, want %d", i, len(seg.Docs), tc.wantSegs[i])
				}
				seen += len(seg.Docs)
			}
			if len(tc.wantSegs) > 0 && seen != 5 {
				t.Errorf("documents across segments = %d, want 5", seen)
			}
		})
	}
}

func TestWriteReadRoundTrip(t *testing.T) {
	idx := buildIndex(t, corpus()...)
	seg := Split(idx, 2)[0]
	dir := filepath.Join(t.TempDir(), "gen")
	ref, err := Write(seg, dir)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if ref.ID != "seg-0001" || ref.File != "seg-0001.json" || ref.DocCount != 2 || ref.TermCount != 3 {
		t.Fatalf("ref = %+v, want seg-0001/seg-0001.json with 2 docs and 3 terms", ref)
	}
	if seg.Ref != ref {
		t.Errorf("seg.Ref = %+v, want the returned ref %+v", seg.Ref, ref)
	}
	data, err := os.ReadFile(filepath.Join(dir, "seg-0001.json"))
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}
	sum := sha256.Sum256(data)
	if want := hex.EncodeToString(sum[:]); ref.Digest != want {
		t.Errorf("digest = %q, want the sha256 of the file %q", ref.Digest, want)
	}
	if !strings.HasSuffix(string(data), "}\n") {
		t.Errorf("file does not end with a closing brace and newline: %q", tailOf(string(data)))
	}
	if !strings.Contains(string(data), "\n  \"Ref\": {") {
		t.Errorf("file is not indented with two spaces:\n%s", firstLines(string(data), 3))
	}
	if !strings.Contains(string(data), "\"Digest\": \"\"") {
		t.Errorf("stored bytes must carry an empty digest, got:\n%s", firstLines(string(data), 8))
	}
	got, err := Read(dir, ref)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !reflect.DeepEqual(got, seg) {
		t.Errorf("round trip mismatch\n got %+v\nwant %+v", got, seg)
	}
}

func TestWriteIsReproducible(t *testing.T) {
	first := Split(buildIndex(t, corpus()...), 2)[1]
	second := Split(buildIndex(t, corpus()...), 2)[1]
	dirA, dirB := t.TempDir(), t.TempDir()
	refA, err := Write(first, dirA)
	if err != nil {
		t.Fatalf("Write A: %v", err)
	}
	refB, err := Write(second, dirB)
	if err != nil {
		t.Fatalf("Write B: %v", err)
	}
	if refA != refB {
		t.Fatalf("refs differ across runs:\n a %+v\n b %+v", refA, refB)
	}
	bytesA, err := os.ReadFile(filepath.Join(dirA, refA.File))
	if err != nil {
		t.Fatalf("read A: %v", err)
	}
	bytesB, err := os.ReadFile(filepath.Join(dirB, refB.File))
	if err != nil {
		t.Fatalf("read B: %v", err)
	}
	if !reflect.DeepEqual(bytesA, bytesB) {
		t.Errorf("segment bytes differ across runs")
	}
	// Rewriting a segment that already carries a digest must not fold that
	// digest into the file.
	again, err := Write(first, dirA)
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if again.Digest != refA.Digest {
		t.Errorf("rewrite digest = %q, want %q", again.Digest, refA.Digest)
	}
}

func TestReadDetectsDamage(t *testing.T) {
	idx := buildIndex(t, corpus()...)
	tests := []struct {
		name     string
		setup    func(t *testing.T, dir string) core.SegmentRef
		wantPath string
		wantWhy  string
	}{
		{
			name: "missing file",
			setup: func(t *testing.T, dir string) core.SegmentRef {
				return core.SegmentRef{ID: "seg-0009", File: "seg-0009.json", Digest: strings.Repeat("ab", 32)}
			},
			wantPath: "seg-0009.json",
			wantWhy:  "missing",
		},
		{
			name: "no digest recorded",
			setup: func(t *testing.T, dir string) core.SegmentRef {
				ref := writeFirst(t, idx, dir)
				ref.Digest = ""
				return ref
			},
			wantPath: "seg-0001.json",
			wantWhy:  "no digest recorded",
		},
		{
			name: "corrupt contents",
			setup: func(t *testing.T, dir string) core.SegmentRef {
				ref := writeFirst(t, idx, dir)
				path := filepath.Join(dir, ref.File)
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatalf("read: %v", err)
				}
				data[len(data)/2] ^= 0x20
				if err := os.WriteFile(path, data, 0o644); err != nil {
					t.Fatalf("corrupt: %v", err)
				}
				return ref
			},
			wantPath: "seg-0001.json",
			wantWhy:  "digest mismatch",
		},
		{
			name: "digest of another segment",
			setup: func(t *testing.T, dir string) core.SegmentRef {
				ref := writeFirst(t, idx, dir)
				ref.Digest = strings.Repeat("00", 32)
				return ref
			},
			wantPath: "seg-0001.json",
			wantWhy:  "digest mismatch",
		},
		{
			name: "not json",
			setup: func(t *testing.T, dir string) core.SegmentRef {
				body := []byte("this is not a segment\n")
				if err := os.WriteFile(filepath.Join(dir, "seg-0001.json"), body, 0o644); err != nil {
					t.Fatalf("write: %v", err)
				}
				sum := sha256.Sum256(body)
				return core.SegmentRef{ID: "seg-0001", File: "seg-0001.json", Digest: hex.EncodeToString(sum[:])}
			},
			wantPath: "seg-0001.json",
			wantWhy:  "malformed segment json",
		},
		{
			name: "file escapes the directory",
			setup: func(t *testing.T, dir string) core.SegmentRef {
				return core.SegmentRef{ID: "seg-0001", File: "../seg-0001.json", Digest: strings.Repeat("cd", 32)}
			},
			wantPath: "../seg-0001.json",
			wantWhy:  "unusable file name",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			ref := tc.setup(t, dir)
			got, err := Read(dir, ref)
			if got != nil {
				t.Errorf("Read returned a segment on failure: %+v", got)
			}
			if !errors.Is(err, core.ErrIntegrity) {
				t.Fatalf("err = %v, want it to match core.ErrIntegrity", err)
			}
			var ie *core.IntegrityError
			if !errors.As(err, &ie) {
				t.Fatalf("err = %v, want a *core.IntegrityError", err)
			}
			if ie.Path != tc.wantPath {
				t.Errorf("path = %q, want %q", ie.Path, tc.wantPath)
			}
			if ie.Reason != tc.wantWhy {
				t.Errorf("reason = %q, want %q", ie.Reason, tc.wantWhy)
			}
		})
	}
}

// writeFirst writes the first segment of idx into dir and returns its ref.
func writeFirst(t *testing.T, idx *index.Index, dir string) core.SegmentRef {
	t.Helper()
	ref, err := Write(Split(idx, 2)[0], dir)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	return ref
}

func TestWriteRejectsUnusableSegments(t *testing.T) {
	tests := []struct {
		name string
		seg  *Segment
	}{
		{name: "nil segment", seg: nil},
		{name: "no id and no file", seg: &Segment{}},
		{name: "relative escape", seg: &Segment{Ref: core.SegmentRef{ID: "seg-0001", File: "../seg-0001.json"}}},
		{name: "absolute path", seg: &Segment{Ref: core.SegmentRef{ID: "seg-0001", File: "/tmp/seg-0001.json"}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			ref, err := Write(tc.seg, dir)
			if !errors.Is(err, core.ErrUsage) {
				t.Fatalf("err = %v, want it to match core.ErrUsage", err)
			}
			if ref != (core.SegmentRef{}) {
				t.Errorf("ref = %+v, want the zero ref on failure", ref)
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatalf("read dir: %v", err)
			}
			if len(entries) != 0 {
				t.Errorf("directory holds %d entries, want nothing written", len(entries))
			}
		})
	}
}

func TestToIndexRebuildsGlobalStatistics(t *testing.T) {
	idx := buildIndex(t, corpus()...)
	dir := t.TempDir()
	var loaded []*Segment
	for _, seg := range Split(idx, 2) {
		ref, err := Write(seg, dir)
		if err != nil {
			t.Fatalf("Write %s: %v", seg.Ref.ID, err)
		}
		got, err := Read(dir, ref)
		if err != nil {
			t.Fatalf("Read %s: %v", ref.ID, err)
		}
		loaded = append(loaded, got)
	}
	rebuilt := ToIndex(loaded)
	if rebuilt.DocCount() != 5 {
		t.Errorf("DocCount = %d, want 5", rebuilt.DocCount())
	}
	if rebuilt.TermCount() != 5 {
		t.Errorf("TermCount = %d, want 5", rebuilt.TermCount())
	}
	if got, want := rebuilt.AvgDocLength(), 11.0/5.0; math.Abs(got-want) > 1e-9 {
		t.Errorf("AvgDocLength = %v, want %v", got, want)
	}
	wantTerms := []core.TermStats{
		{Term: "alpha", DocFreq: 2, TotalFreq: 3},
		{Term: "beta", DocFreq: 3, TotalFreq: 3},
		{Term: "delta", DocFreq: 1, TotalFreq: 1},
		{Term: "epsilon", DocFreq: 1, TotalFreq: 2},
		{Term: "gamma", DocFreq: 2, TotalFreq: 2},
	}
	if got := rebuilt.Terms(); !reflect.DeepEqual(got, wantTerms) {
		t.Errorf("terms = %+v, want %+v", got, wantTerms)
	}
	wantPostings := []core.Posting{
		{DocID: "a.md", Freq: 2, Positions: []int{0, 2}},
		{DocID: "c.md", Freq: 1, Positions: []int{0}},
	}
	if got := rebuilt.Postings("alpha"); !reflect.DeepEqual(got, wantPostings) {
		t.Errorf("alpha postings = %+v, want %+v", got, wantPostings)
	}
	info, ok := rebuilt.Doc("e.md")
	if !ok {
		t.Fatal("rebuilt index is missing e.md")
	}
	if info.Length != 3 || info.Title != "Echo" || info.Fields["kind"] != "text" {
		t.Errorf("e.md = %+v, want length 3, title Echo, kind text", info)
	}
	original, _ := idx.Doc("e.md")
	if info.ContentHash != original.ContentHash {
		t.Errorf("content hash = %q, want %q", info.ContentHash, original.ContentHash)
	}
}

func TestToIndexHandlesSparseSegments(t *testing.T) {
	if got := ToIndex(nil); got.DocCount() != 0 || got.TermCount() != 0 {
		t.Errorf("ToIndex(nil) = %d docs and %d terms, want an empty index", got.DocCount(), got.TermCount())
	}
	seg := &Segment{
		Ref:  core.SegmentRef{ID: "seg-0001", File: "seg-0001.json"},
		Docs: []core.DocInfo{{ID: "x.md", Title: "X", Fields: map[string]string{"kind": "text"}}},
		Postings: map[string][]core.Posting{
			"pair": {{DocID: "x.md", Freq: 1, Positions: []int{0}}},
			"solo": {{DocID: "x.md", Freq: 2}},
		},
	}
	rebuilt := ToIndex([]*Segment{nil, seg})
	if rebuilt.DocCount() != 1 {
		t.Fatalf("DocCount = %d, want 1", rebuilt.DocCount())
	}
	info, ok := rebuilt.Doc("x.md")
	if !ok {
		t.Fatal("rebuilt index is missing x.md")
	}
	if info.Length != 3 {
		t.Errorf("length = %d, want 3: positionless occurrences still count", info.Length)
	}
	want := []core.Posting{{DocID: "x.md", Freq: 2, Positions: []int{1, 2}}}
	if got := rebuilt.Postings("solo"); !reflect.DeepEqual(got, want) {
		t.Errorf("solo postings = %+v, want %+v", got, want)
	}
}

func TestFileNameResolution(t *testing.T) {
	tests := []struct {
		name string
		ref  core.SegmentRef
		want string
		ok   bool
	}{
		{name: "explicit file wins", ref: core.SegmentRef{ID: "seg-0001", File: "other.json"}, want: "other.json", ok: true},
		{name: "derived from id", ref: core.SegmentRef{ID: "seg-0042"}, want: "seg-0042.json", ok: true},
		{name: "nested is allowed", ref: core.SegmentRef{ID: "seg-0001", File: "parts/seg-0001.json"}, want: "parts/seg-0001.json", ok: true},
		{name: "empty ref", ref: core.SegmentRef{}},
		{name: "parent escape", ref: core.SegmentRef{ID: "seg-0001", File: "../seg-0001.json"}},
		{name: "nested escape", ref: core.SegmentRef{ID: "seg-0001", File: "parts/../../seg-0001.json"}},
		{name: "absolute", ref: core.SegmentRef{ID: "seg-0001", File: "/etc/seg-0001.json"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := fileName(tc.ref)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v (got %q)", ok, tc.ok, got)
			}
			if got != tc.want {
				t.Errorf("name = %q, want %q", got, tc.want)
			}
		})
	}
}

// firstLines returns the first n lines of s for error messages.
func firstLines(s string, n int) string {
	lines := strings.SplitN(s, "\n", n+1)
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}

// tailOf returns the last few bytes of s for error messages.
func tailOf(s string) string {
	if len(s) > 16 {
		return s[len(s)-16:]
	}
	return s
}
