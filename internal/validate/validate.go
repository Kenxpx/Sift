// Package validate checks a published index against its manifest.
//
// Index answers the operational question "can this index be used?": it stops
// at the first problem and reports it as a *core.IntegrityError. Report
// answers the diagnostic question "what is wrong with it?": it lists every
// problem it can find, including drift that is not corruption, such as a
// configuration that changed after the index was published. The first fatal
// problem Report lists is exactly the error Index returns.
package validate

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"sift/internal/config"
	"sift/internal/core"
	"sift/internal/manifest"
	"sift/internal/publish"
	"sift/internal/store"
)

// Findings describes the published index and everything wrong with it.
type Findings struct {
	// Generation is the published generation these findings describe.
	Generation core.Generation
	// Segments is the number of segment files the manifest names.
	Segments int
	// Documents is the number of documents the manifest records.
	Documents int
	// Terms is the number of distinct terms the manifest records.
	Terms int
	// Problems holds one "<path>: <reason>" line per problem, in a stable
	// order: the manifest itself, then segments in manifest order, then the
	// extraction cache, then the output directory. It is empty when the
	// index is intact and current.
	Problems []string
}

// Index reports whether the index published under cfg can be used. It returns
// nil when the manifest, every segment it names and the extraction cache are
// present and match their recorded digests, and a *core.IntegrityError naming
// the first offending path otherwise. A configuration that changed since
// publication is drift rather than damage: it is reported by Report and
// ignored here.
func Index(cfg core.Config) error {
	out := publish.OutputPath(cfg)
	m, err := manifest.Load(out)
	if err != nil {
		return fmt.Errorf("validate: load %s: %w", core.ManifestFile, err)
	}
	for _, found := range inspect(out, m, cfg) {
		if found.fatal {
			return &core.IntegrityError{Path: found.path, Reason: found.reason}
		}
	}
	// The manifest package has the last word on what a valid generation is,
	// so anything it rejects that the checks above accept is still an error.
	return manifest.Validate(out, m)
}

// Report describes the index published under cfg and lists its problems. A
// manifest that is missing or unreadable is an error, because there is then
// nothing to report on; every other problem appears in Findings.Problems and
// Report itself succeeds.
func Report(cfg core.Config) (Findings, error) {
	out := publish.OutputPath(cfg)
	m, err := manifest.Load(out)
	if err != nil {
		return Findings{}, fmt.Errorf("validate: load %s: %w", core.ManifestFile, err)
	}
	f := Findings{
		Generation: m.Generation,
		Segments:   len(m.Segments),
		Documents:  m.DocCount,
		Terms:      m.TermCount,
	}
	for _, found := range inspect(out, m, cfg) {
		f.Problems = append(f.Problems, found.path+": "+found.reason)
	}
	return f, nil
}

// problem is one defect found in a published index.
type problem struct {
	// path is the offending file or directory, relative to the output dir.
	path string
	// reason is a short lower-case explanation.
	reason string
	// fatal marks a problem that makes the generation unusable, as opposed to
	// one that only reports drift from the current configuration.
	fatal bool
}

// inspect examines everything the manifest names, in a fixed order, so two
// runs over the same output directory always report the same problems in the
// same sequence.
func inspect(out string, m core.Manifest, cfg core.Config) []problem {
	found := checkListing(m)
	found = append(found, checkSegments(out, m)...)
	found = append(found, checkCache(out, m)...)
	if m.ConfigHash != "" && m.ConfigHash != config.Hash(cfg) {
		found = append(found, problem{core.ManifestFile,
			fmt.Sprintf("configuration changed since generation %d was published", m.Generation), false})
	}
	if info, err := os.Lstat(filepath.Join(out, publish.StagingDir)); err == nil {
		reason := "left behind by an interrupted publish"
		if !info.IsDir() {
			reason = "not a directory, so the next publish will fail"
		}
		found = append(found, problem{publish.StagingDir, reason, false})
	}
	return found
}

// checkListing looks at the segment list itself, before any file is opened.
func checkListing(m core.Manifest) []problem {
	var found []problem
	seen := make(map[core.SegmentID]bool, len(m.Segments))
	ordered := true
	for i, ref := range m.Segments {
		if seen[ref.ID] {
			found = append(found, problem{core.ManifestFile,
				fmt.Sprintf("segment %s is listed twice", ref.ID), true})
		}
		seen[ref.ID] = true
		if i > 0 && ref.ID < m.Segments[i-1].ID {
			ordered = false
		}
	}
	if !ordered {
		found = append(found, problem{core.ManifestFile, "segments are not in ascending order", false})
	}
	return found
}

// checkSegments reads every segment the manifest names, verifying its digest
// and the counts recorded for it. The totals are only compared when every
// segment could be read, so one damaged file does not turn into a cascade of
// count problems.
func checkSegments(out string, m core.Manifest) []problem {
	var found []problem
	docs := make(map[core.DocID]bool, m.DocCount)
	terms := make(map[string]bool, m.TermCount)
	complete := true
	for _, ref := range m.Segments {
		name := ref.File
		if name == "" {
			name = string(ref.ID)
		}
		seg, err := publish.ReadSegment(out, ref)
		if err != nil {
			complete = false
			var damaged *core.IntegrityError
			if errors.As(err, &damaged) {
				found = append(found, problem{damaged.Path, damaged.Reason, true})
				continue
			}
			found = append(found, problem{name, "cannot be read", true})
			continue
		}
		if len(seg.Docs) != ref.DocCount {
			complete = false
			found = append(found, problem{name,
				fmt.Sprintf("holds %d documents, manifest says %d", len(seg.Docs), ref.DocCount), true})
		}
		if len(seg.Terms) != ref.TermCount {
			complete = false
			found = append(found, problem{name,
				fmt.Sprintf("holds %d terms, manifest says %d", len(seg.Terms), ref.TermCount), true})
		}
		for _, doc := range seg.Docs {
			docs[doc.ID] = true
		}
		for _, term := range seg.Terms {
			terms[term.Term] = true
		}
	}
	if !complete {
		return found
	}
	if len(docs) != m.DocCount {
		found = append(found, problem{core.ManifestFile,
			fmt.Sprintf("doc count %d does not match the %d documents in the segments", m.DocCount, len(docs)), true})
	}
	if len(terms) != m.TermCount {
		found = append(found, problem{core.ManifestFile,
			fmt.Sprintf("term count %d does not match the %d terms in the segments", m.TermCount, len(terms)), true})
	}
	return found
}

// checkCache verifies the extraction cache that belongs to the generation.
func checkCache(out string, m core.Manifest) []problem {
	if m.CacheDigest == "" {
		return nil
	}
	digest, err := store.SHA256File(filepath.Join(out, core.CacheFile))
	switch {
	case isMissing(err):
		return []problem{{core.CacheFile, "missing", true}}
	case err != nil:
		return []problem{{core.CacheFile, "cannot be read", true}}
	case digest != m.CacheDigest:
		return []problem{{core.CacheFile, "digest mismatch", true}}
	}
	return nil
}

// isMissing reports whether err says the file was not there, whichever of the
// two ways of saying so the package that opened it used.
func isMissing(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, core.ErrNotFound)
}
