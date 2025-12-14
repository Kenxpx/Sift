// Package manifest builds, saves, loads and verifies the manifest that
// describes one published generation of a Sift index.
//
// The manifest is the entry point to a generation. It lists every segment
// file together with the digest of its contents, records the digest of the
// extraction cache published beside them, and carries the document and term
// counts a reader can check before trusting anything on disk. Validate
// re-reads those files and reports the first path that disagrees with the
// manifest, so a partially written or tampered generation is rejected as a
// whole instead of being half loaded.
package manifest

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"sift/internal/core"
	"sift/internal/index"
	"sift/internal/store"
)

// Build assembles the manifest for one generation.
//
// The segment references are copied and sorted by SegmentID, so the caller's
// slice is never modified and the same inputs always produce the same
// manifest. DocCount and TermCount come from the index rather than from the
// segment references: segments partition documents but share terms, so
// distinct terms must be counted once over the whole index and never summed.
// When idx is nil the counts fall back to the segment totals. A nil clock is
// treated as core.SystemClock.
func Build(gen core.Generation, refs []core.SegmentRef, cfgHash, cacheDigest string, clock core.Clock, idx *index.Index) core.Manifest {
	segs := make([]core.SegmentRef, len(refs))
	copy(segs, refs)
	sort.Slice(segs, func(i, j int) bool {
		if segs[i].ID != segs[j].ID {
			return segs[i].ID < segs[j].ID
		}
		return segs[i].File < segs[j].File
	})

	docCount, termCount := 0, 0
	if idx != nil {
		docCount = idx.DocCount()
		termCount = idx.TermCount()
	} else {
		for _, ref := range segs {
			docCount += ref.DocCount
			termCount += ref.TermCount
		}
	}

	var c core.Clock = core.SystemClock{}
	if clock != nil {
		c = clock
	}

	return core.Manifest{
		Generation:  gen,
		Segments:    segs,
		DocCount:    docCount,
		TermCount:   termCount,
		ConfigHash:  cfgHash,
		CacheDigest: cacheDigest,
		CreatedAt:   c.Now(),
	}
}

// Save writes m to <dir>/manifest.json, creating dir when needed. The write
// is atomic, so a reader never observes a truncated manifest.
func Save(dir string, m core.Manifest) error {
	if err := store.EnsureDir(dir); err != nil {
		return fmt.Errorf("manifest: prepare %s: %w", dir, err)
	}
	p := filepath.Join(dir, core.ManifestFile)
	if err := store.WriteJSONAtomic(p, m); err != nil {
		return fmt.Errorf("manifest: write %s: %w", p, err)
	}
	return nil
}

// Load reads the manifest from <dir>/manifest.json. A missing manifest yields
// an error matching core.ErrNotFound.
func Load(dir string) (core.Manifest, error) {
	var m core.Manifest
	p := filepath.Join(dir, core.ManifestFile)
	if err := store.ReadJSON(p, &m); err != nil {
		return core.Manifest{}, fmt.Errorf("manifest: read %s: %w", p, err)
	}
	return m, nil
}

// Current reports the generation published in dir. The second result is false
// when no readable manifest is present.
func Current(dir string) (core.Generation, bool) {
	m, err := Load(dir)
	if err != nil {
		return 0, false
	}
	return m.Generation, true
}

// Validate checks that dir really holds the generation m describes: every
// segment file is present with the recorded digest, the extraction cache is
// present with the recorded digest, and the manifest counts agree with the
// segments.
//
// The checks run in that order and stop at the first problem, so the returned
// *core.IntegrityError always names the offending path relative to dir. The
// error matches core.ErrIntegrity.
func Validate(dir string, m core.Manifest) error {
	seen := make(map[core.SegmentID]bool, len(m.Segments))
	sumDocs, sumTerms, maxTerms := 0, 0, 0

	for _, ref := range m.Segments {
		where := ref.File
		if where == "" {
			where = core.ManifestFile
		}
		if ref.ID == "" {
			return &core.IntegrityError{Path: where, Reason: "empty segment id"}
		}
		if seen[ref.ID] {
			return &core.IntegrityError{Path: where, Reason: fmt.Sprintf("duplicate segment id %s", ref.ID)}
		}
		seen[ref.ID] = true

		if err := verify(dir, ref.File, ref.Digest); err != nil {
			return err
		}

		sumDocs += ref.DocCount
		sumTerms += ref.TermCount
		if ref.TermCount > maxTerms {
			maxTerms = ref.TermCount
		}
	}

	if err := verify(dir, core.CacheFile, m.CacheDigest); err != nil {
		return err
	}

	if m.DocCount != sumDocs {
		return &core.IntegrityError{
			Path:   core.ManifestFile,
			Reason: fmt.Sprintf("doc count %d but segments hold %d", m.DocCount, sumDocs),
		}
	}
	// Distinct terms are shared between segments, so the index-wide count sits
	// between the largest single segment and the sum of every segment.
	if m.TermCount < maxTerms || m.TermCount > sumTerms {
		return &core.IntegrityError{
			Path:   core.ManifestFile,
			Reason: fmt.Sprintf("term count %d outside segment range %d..%d", m.TermCount, maxTerms, sumTerms),
		}
	}
	return nil
}

// verify checks that name exists under dir with the digest want. It returns
// nil, not a typed nil, when the file is sound.
func verify(dir, name, want string) error {
	if name == "" {
		return &core.IntegrityError{Path: core.ManifestFile, Reason: "empty file name"}
	}
	clean := path.Clean(filepath.ToSlash(name))
	if filepath.IsAbs(name) || clean == ".." || strings.HasPrefix(clean, "../") {
		return &core.IntegrityError{Path: name, Reason: "escapes the output directory"}
	}

	p := filepath.Join(dir, filepath.FromSlash(clean))
	info, err := os.Stat(p)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return &core.IntegrityError{Path: name, Reason: "missing"}
	case err != nil:
		return &core.IntegrityError{Path: name, Reason: "unreadable"}
	case info.IsDir():
		return &core.IntegrityError{Path: name, Reason: "not a regular file"}
	}
	if want == "" {
		return &core.IntegrityError{Path: name, Reason: "no digest recorded"}
	}
	got, err := store.SHA256File(p)
	if err != nil {
		return &core.IntegrityError{Path: name, Reason: "unreadable"}
	}
	if got != want {
		return &core.IntegrityError{Path: name, Reason: "digest mismatch"}
	}
	return nil
}
