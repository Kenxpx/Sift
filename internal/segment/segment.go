// Package segment stores an index on disk as a set of immutable segment
// files.
//
// Split cuts an index into deterministic, document-ordered pieces, Write
// serializes one piece atomically and records the digest of the bytes it
// wrote, Read verifies that digest before it returns anything, and ToIndex
// rebuilds a searchable index from segments that were read back. The same
// index always splits into the same segments, and the same segment always
// writes the same bytes, so a generation can be compared by digest alone.
package segment

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"sift/internal/core"
	"sift/internal/index"
	"sift/internal/store"
)

// idPrefix and fileExt build the SegmentID and file name of every segment
// Split produces: "seg-0001" is stored in "seg-0001.json".
const (
	idPrefix = "seg-"
	fileExt  = ".json"
)

// filePerm is the mode of a written segment file.
const filePerm fs.FileMode = 0o644

// Segment is one immutable slice of an index: the documents it owns, the
// term statistics computed from its own postings, and those postings keyed
// by term.
//
// A segment describes only itself. Its TermStats count the documents in this
// segment alone, so segments can be written, copied and verified
// independently; global statistics are recomputed by ToIndex.
type Segment struct {
	// Ref identifies the segment and, once written, carries the digest of
	// the file it was written to.
	Ref core.SegmentRef
	// Docs are the segment's documents in ascending DocID order.
	Docs []core.DocInfo
	// Terms are the segment-local statistics in ascending Term order.
	Terms []core.TermStats
	// Postings maps a term to its postings in ascending DocID order.
	Postings map[string][]core.Posting
}

// Split cuts idx into segments holding at most maxDocs documents each.
//
// Documents are taken in ascending DocID order and segment IDs run
// "seg-0001", "seg-0002" and so on, so the same index always places the same
// documents in the same segment. Term statistics are recomputed from the
// postings that land in each segment rather than copied from the index. A
// maxDocs below one places every document in a single segment, and a nil or
// empty index yields no segments.
func Split(idx *index.Index, maxDocs int) []*Segment {
	if idx == nil {
		return nil
	}
	docs := idx.Docs()
	if len(docs) == 0 {
		return nil
	}
	if maxDocs < 1 {
		maxDocs = len(docs)
	}
	count := (len(docs) + maxDocs - 1) / maxDocs
	segs := make([]*Segment, 0, count)
	owner := make(map[core.DocID]*Segment, len(docs))
	for i := 0; i < count; i++ {
		lo := i * maxDocs
		hi := lo + maxDocs
		if hi > len(docs) {
			hi = len(docs)
		}
		id := core.SegmentID(fmt.Sprintf("%s%04d", idPrefix, i+1))
		seg := &Segment{
			Ref:      core.SegmentRef{ID: id, File: string(id) + fileExt, DocCount: hi - lo},
			Docs:     make([]core.DocInfo, 0, hi-lo),
			Postings: make(map[string][]core.Posting),
		}
		for _, d := range docs[lo:hi] {
			seg.Docs = append(seg.Docs, copyDocInfo(d))
			owner[d.ID] = seg
		}
		segs = append(segs, seg)
	}
	for _, ts := range idx.Terms() {
		for _, p := range idx.Postings(ts.Term) {
			seg, ok := owner[p.DocID]
			if !ok {
				// A posting for a document the index no longer lists
				// belongs to no segment and is dropped.
				continue
			}
			seg.Postings[ts.Term] = append(seg.Postings[ts.Term], copyPosting(p))
		}
	}
	for _, seg := range segs {
		seg.Terms = termStats(seg.Postings)
		seg.Ref.TermCount = len(seg.Terms)
	}
	return segs
}

// Write serializes seg as indented JSON into dir, creating dir when needed
// and replacing any previous file atomically, and returns the reference a
// manifest should record.
//
// The digest is the SHA-256 of the exact bytes written. Those bytes always
// carry an empty Ref.Digest, so writing a segment again reproduces the same
// file and the same digest. Write fills File, DocCount, TermCount and Digest
// on both the returned reference and seg.Ref. A segment with neither an ID
// nor a file name, or with a file name that leaves dir, is rejected with an
// error matching core.ErrUsage.
func Write(seg *Segment, dir string) (core.SegmentRef, error) {
	if seg == nil {
		return core.SegmentRef{}, fmt.Errorf("segment: write: nil segment: %w", core.ErrUsage)
	}
	name, ok := fileName(seg.Ref)
	if !ok {
		return core.SegmentRef{}, fmt.Errorf("segment %q: write: unusable file name %q: %w", seg.Ref.ID, seg.Ref.File, core.ErrUsage)
	}
	ref := seg.Ref
	ref.File = name
	ref.DocCount = len(seg.Docs)
	ref.TermCount = len(seg.Terms)
	ref.Digest = ""
	body := Segment{Ref: ref, Docs: seg.Docs, Terms: seg.Terms, Postings: seg.Postings}
	data, err := json.MarshalIndent(body, "", "  ")
	if err != nil {
		return core.SegmentRef{}, fmt.Errorf("segment %q: encode: %w", ref.ID, err)
	}
	data = append(data, '\n')
	path := filepath.Join(dir, filepath.FromSlash(name))
	if err := store.EnsureDir(filepath.Dir(path)); err != nil {
		return core.SegmentRef{}, fmt.Errorf("segment %q: %w", ref.ID, err)
	}
	if err := store.WriteFileAtomic(path, data, filePerm); err != nil {
		return core.SegmentRef{}, fmt.Errorf("segment %q: %w", ref.ID, err)
	}
	ref.Digest = store.SHA256Bytes(data)
	seg.Ref = ref
	return ref, nil
}

// Read loads the segment ref names from dir and verifies it before returning
// it.
//
// The file must be present, its SHA-256 must equal ref.Digest, and its
// contents must decode. Every failure is a *core.IntegrityError naming the
// offending file, which errors.Is matches as core.ErrIntegrity, so a caller
// can tell a damaged generation from an unrelated problem. Document and term
// counts are not checked here: comparing a segment against the manifest that
// lists it is manifest validation. The returned segment carries the verified
// digest on its Ref.
func Read(dir string, ref core.SegmentRef) (*Segment, error) {
	name, ok := fileName(ref)
	if !ok {
		return nil, &core.IntegrityError{Path: refPath(ref), Reason: "unusable file name"}
	}
	if ref.Digest == "" {
		return nil, &core.IntegrityError{Path: name, Reason: "no digest recorded"}
	}
	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, &core.IntegrityError{Path: name, Reason: "missing"}
		}
		return nil, &core.IntegrityError{Path: name, Reason: fmt.Sprintf("unreadable: %v", err)}
	}
	digest := store.SHA256Bytes(data)
	if !strings.EqualFold(digest, ref.Digest) {
		return nil, &core.IntegrityError{Path: name, Reason: "digest mismatch"}
	}
	var seg Segment
	if err := json.Unmarshal(data, &seg); err != nil {
		return nil, &core.IntegrityError{Path: name, Reason: "malformed segment json"}
	}
	if seg.Ref.ID == "" {
		seg.Ref.ID = ref.ID
	}
	seg.Ref.File = name
	seg.Ref.Digest = digest
	return &seg, nil
}

// ToIndex rebuilds a searchable index from segs.
//
// Segments are replayed in the given order and their documents in stored
// order, so the result is deterministic; a document present in more than one
// segment keeps the copy replayed last, as index.Merge does. Postings are
// turned back into the token stream they were built from, so the rebuilt
// index recomputes the same global term statistics and document lengths. A
// segment does not store document bodies, so the rebuilt documents have none.
func ToIndex(segs []*Segment) *index.Index {
	idx := index.New()
	for _, seg := range segs {
		if seg == nil {
			continue
		}
		tokens := tokensByDoc(seg)
		for _, d := range seg.Docs {
			idx.Add(document(d), tokens[d.ID])
		}
	}
	return idx
}

// fileName returns the file a reference names, relative to the output
// directory, and reports whether it is usable: named, and contained in that
// directory.
func fileName(ref core.SegmentRef) (string, bool) {
	name := ref.File
	if name == "" && ref.ID != "" {
		name = string(ref.ID) + fileExt
	}
	if name == "" || !filepath.IsLocal(filepath.FromSlash(name)) {
		return "", false
	}
	return name, true
}

// refPath names a reference in an error message even when its file name is
// unusable.
func refPath(ref core.SegmentRef) string {
	if ref.File != "" {
		return ref.File
	}
	return string(ref.ID)
}

// termStats summarizes every term from the postings of one segment. Postings
// are in ascending DocID order, so repeated documents are adjacent and
// DocFreq counts distinct documents without allocating.
func termStats(postings map[string][]core.Posting) []core.TermStats {
	out := make([]core.TermStats, 0, len(postings))
	for term, ps := range postings {
		st := core.TermStats{Term: term}
		var prev core.DocID
		for i, p := range ps {
			if i == 0 || p.DocID != prev {
				st.DocFreq++
				prev = p.DocID
			}
			st.TotalFreq += p.Freq
		}
		out = append(out, st)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Term < out[j].Term })
	return out
}

// tokensByDoc reconstructs the token stream of every document in seg from
// its postings, in ascending position order.
func tokensByDoc(seg *Segment) map[core.DocID][]core.Token {
	terms := make([]string, 0, len(seg.Postings))
	for term := range seg.Postings {
		terms = append(terms, term)
	}
	sort.Strings(terms)

	// unplaced records occurrences stored without positions. They still
	// count towards document length, so they are appended after the located
	// tokens in ascending term order.
	type unplaced struct {
		doc  core.DocID
		term string
		freq int
	}
	var tail []unplaced
	next := make(map[core.DocID]int, len(seg.Docs))
	out := make(map[core.DocID][]core.Token, len(seg.Docs))
	for _, term := range terms {
		for _, p := range seg.Postings[term] {
			if len(p.Positions) == 0 {
				if p.Freq > 0 {
					tail = append(tail, unplaced{doc: p.DocID, term: term, freq: p.Freq})
				}
				continue
			}
			for _, pos := range p.Positions {
				out[p.DocID] = append(out[p.DocID], core.Token{Term: term, Position: pos})
				if pos >= next[p.DocID] {
					next[p.DocID] = pos + 1
				}
			}
		}
	}
	for _, u := range tail {
		for i := 0; i < u.freq; i++ {
			out[u.doc] = append(out[u.doc], core.Token{Term: u.term, Position: next[u.doc]})
			next[u.doc]++
		}
	}
	for id, toks := range out {
		sort.Slice(toks, func(i, j int) bool {
			if toks[i].Position != toks[j].Position {
				return toks[i].Position < toks[j].Position
			}
			return toks[i].Term < toks[j].Term
		})
		out[id] = toks
	}
	return out
}

// document rebuilds the indexable document a stored DocInfo came from. A
// segment does not store bodies, so the body stays empty.
func document(d core.DocInfo) core.Document {
	doc := core.Document{
		ID:          d.ID,
		Path:        string(d.ID),
		Title:       d.Title,
		Kind:        d.Fields["kind"],
		Language:    d.Fields["language"],
		ContentHash: d.ContentHash,
	}
	if d.Fields != nil {
		doc.Fields = make(map[string]string, len(d.Fields))
		for k, v := range d.Fields {
			doc.Fields[k] = v
		}
	}
	return doc
}

// copyDocInfo copies a DocInfo so a segment never shares the index's field
// maps.
func copyDocInfo(d core.DocInfo) core.DocInfo {
	out := d
	if d.Fields != nil {
		out.Fields = make(map[string]string, len(d.Fields))
		for k, v := range d.Fields {
			out.Fields[k] = v
		}
	}
	return out
}

// copyPosting copies a Posting so a segment never shares the index's
// position slices.
func copyPosting(p core.Posting) core.Posting {
	out := p
	if p.Positions != nil {
		out.Positions = make([]int, len(p.Positions))
		copy(out.Positions, p.Positions)
	}
	return out
}
