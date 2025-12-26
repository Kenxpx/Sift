// Package publish makes a complete generation of an index visible under the
// configured output directory, and loads a published generation back.
//
// The output directory holds one manifest, one extraction cache, and one
// directory of immutable segment files per generation:
//
//	manifest.json           names the live generation and every file in it
//	extract-cache.json      the extraction cache of the live generation
//	gen-0001/seg-0001.json  the segment files of generation 1
//	.staging/               a generation under construction, never read
//
// Publish writes every segment and the cache into the staging directory,
// moves that directory to its generation directory, and writes the manifest
// last. The manifest is the only commit point: a reader that loads it sees
// either the previous generation or the new one and never a mixture, and a
// failure at any step leaves the previous generation exactly as it was and
// removes the staging directory. Because each generation owns a directory,
// publishing never writes over a file the previous manifest still names.
package publish

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"sift/internal/cache"
	"sift/internal/config"
	"sift/internal/core"
	"sift/internal/index"
	"sift/internal/manifest"
	"sift/internal/segment"
	"sift/internal/store"
)

// StagingDir is the directory, relative to the output directory, that a
// generation is built in before it becomes visible. It is never read by a
// reader, and one left behind means a publish was interrupted.
const StagingDir = ".staging"

// generationPrefix starts the name of every generation directory.
const generationPrefix = "gen-"

// segmentExt is the extension of a segment file, used only when a segment
// reference carries no file name of its own.
const segmentExt = ".json"

// defaultSegmentDocs caps segment size when the configuration does not.
const defaultSegmentDocs = 256

// cacheFilePerm is the mode of the published extraction cache.
const cacheFilePerm = 0o644

// OutputPath returns the directory generations are published to, applying
// core.DefaultOutputDir when cfg does not name one, so an incomplete
// configuration can never publish into the corpus root itself.
func OutputPath(cfg core.Config) string {
	if cfg.OutputDir == "" {
		cfg.OutputDir = core.DefaultOutputDir
	}
	return config.OutputPath(cfg)
}

// GenerationDir returns the name, relative to the output directory, of the
// directory holding the segment files of generation g.
func GenerationDir(g core.Generation) string {
	return fmt.Sprintf("%s%04d", generationPrefix, g)
}

// Publish writes idx and cacheStore to the output directory of cfg as the
// generation after the one published there, and returns the manifest it
// committed. A nil index or cache publishes an empty generation, and a nil
// clock uses the system clock.
//
// Publish either commits a complete generation or changes nothing that a
// reader can see: on any failure the staging directory is removed, a
// half-installed generation directory is removed, the previous extraction
// cache is put back, and the previous manifest still names the previous
// generation.
func Publish(cfg core.Config, idx *index.Index, cacheStore *cache.Store, clock core.Clock) (core.Manifest, error) {
	if idx == nil {
		idx = index.New()
	}
	if cacheStore == nil {
		cacheStore = cache.New()
	}
	if clock == nil {
		clock = core.SystemClock{}
	}
	out := OutputPath(cfg)
	if err := store.EnsureDir(out); err != nil {
		return core.Manifest{}, fmt.Errorf("publish: create %s: %w", out, err)
	}
	gen := core.Generation(1)
	if current, ok := manifest.Current(out); ok {
		gen = current + 1
	}
	staging := filepath.Join(out, StagingDir)
	if err := clearDir(staging, StagingDir); err != nil {
		return core.Manifest{}, err
	}
	if err := store.EnsureDir(staging); err != nil {
		return core.Manifest{}, fmt.Errorf("publish: create %s: %w", StagingDir, err)
	}
	m, err := stage(cfg, idx, cacheStore, clock, staging, gen)
	if err == nil {
		err = commit(out, staging, m)
	}
	if err != nil {
		// The staging directory is ours alone, so removing it can never
		// damage the generation a reader is using.
		os.RemoveAll(staging)
		return core.Manifest{}, err
	}
	prune(out, GenerationDir(gen))
	return m, nil
}

// stage writes every segment and the extraction cache into the staging
// directory and returns the manifest that describes them. The segment
// references it records already point into the generation directory the
// staging directory becomes.
func stage(cfg core.Config, idx *index.Index, cacheStore *cache.Store, clock core.Clock, staging string, gen core.Generation) (core.Manifest, error) {
	maxDocs := cfg.SegmentDocs
	if maxDocs < 1 {
		maxDocs = defaultSegmentDocs
	}
	genDir := GenerationDir(gen)
	segments := segment.Split(idx, maxDocs)
	refs := make([]core.SegmentRef, 0, len(segments))
	for _, seg := range segments {
		ref, err := segment.Write(seg, staging)
		if err != nil {
			return core.Manifest{}, fmt.Errorf("publish: write segment %s: %w", seg.Ref.ID, err)
		}
		ref.File = genDir + "/" + segmentFile(ref)
		refs = append(refs, ref)
	}
	stagedCache := filepath.Join(staging, core.CacheFile)
	if err := cache.Save(stagedCache, cacheStore); err != nil {
		return core.Manifest{}, fmt.Errorf("publish: write %s: %w", core.CacheFile, err)
	}
	digest, err := store.SHA256File(stagedCache)
	if err != nil {
		return core.Manifest{}, fmt.Errorf("publish: hash %s: %w", core.CacheFile, err)
	}
	return manifest.Build(gen, refs, config.Hash(cfg), digest, clock, idx), nil
}

// commit makes a staged generation visible. It installs the files first and
// writes the manifest last, and undoes whatever it already did when a step
// fails, so a caller that sees an error can trust that the previous
// generation is untouched.
func commit(out, staging string, m core.Manifest) error {
	genName := GenerationDir(m.Generation)
	genDir := filepath.Join(out, genName)
	if err := clearDir(genDir, genName); err != nil {
		return err
	}
	cachePath := filepath.Join(out, core.CacheFile)
	previous, hadPrevious, err := readIfExists(cachePath)
	if err != nil {
		return fmt.Errorf("publish: read %s: %w", core.CacheFile, err)
	}
	if err := os.Rename(staging, genDir); err != nil {
		return fmt.Errorf("publish: install %s: %w", genName, err)
	}
	// Past this point the new files are on disk but no manifest names them,
	// so undoing means removing them and putting the old cache back. A
	// failure while undoing is ignored: the caller is already being told
	// that publishing failed, and there is nothing better left to try.
	undo := func() {
		if hadPrevious {
			store.WriteFileAtomic(cachePath, previous, cacheFilePerm)
		} else {
			os.Remove(cachePath)
		}
		os.RemoveAll(genDir)
	}
	if err := os.Rename(filepath.Join(genDir, core.CacheFile), cachePath); err != nil {
		undo()
		return fmt.Errorf("publish: install %s: %w", core.CacheFile, err)
	}
	if err := manifest.Save(out, m); err != nil {
		undo()
		return fmt.Errorf("publish: write %s: %w", core.ManifestFile, err)
	}
	return nil
}

// prune removes the segment directories of superseded generations. A
// directory that cannot be removed is left behind on purpose: it is dead
// weight, not a defect in the generation that was just committed.
func prune(out, keep string) {
	entries, err := os.ReadDir(out)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == keep || !isGenerationDir(entry.Name()) {
			continue
		}
		os.RemoveAll(filepath.Join(out, entry.Name()))
	}
}

// Load reads the generation published under cfg. The manifest is validated
// before any segment is read, so a damaged index is reported as a
// *core.IntegrityError naming the offending path and no partial index is
// ever handed back.
func Load(cfg core.Config) (*index.Index, core.Manifest, error) {
	out := OutputPath(cfg)
	m, err := manifest.Load(out)
	if err != nil {
		return nil, core.Manifest{}, fmt.Errorf("publish: load %s: %w", core.ManifestFile, err)
	}
	if err := manifest.Validate(out, m); err != nil {
		return nil, core.Manifest{}, err
	}
	segments := make([]*segment.Segment, 0, len(m.Segments))
	for _, ref := range m.Segments {
		seg, err := ReadSegment(out, ref)
		if err != nil {
			return nil, core.Manifest{}, err
		}
		segments = append(segments, seg)
	}
	return segment.ToIndex(segments), m, nil
}

// ReadSegment reads one segment of a published generation and verifies the
// digest recorded for it. outDir is the output directory and ref comes from
// the manifest of the generation that named the segment; the file is located
// through ref.File, so segments are found wherever their generation put them.
func ReadSegment(outDir string, ref core.SegmentRef) (*segment.Segment, error) {
	file := segmentPath(ref)
	local := ref
	local.File = path.Base(file)
	seg, err := segment.Read(filepath.Join(outDir, filepath.FromSlash(path.Dir(file))), local)
	if err != nil {
		var damaged *core.IntegrityError
		if errors.As(err, &damaged) {
			// Report the path the manifest uses, not the file name alone.
			return nil, &core.IntegrityError{Path: file, Reason: damaged.Reason}
		}
		return nil, fmt.Errorf("publish: read segment %s: %w", file, err)
	}
	return seg, nil
}

// segmentPath returns the slash-separated path of ref relative to the output
// directory.
func segmentPath(ref core.SegmentRef) string {
	file := filepath.ToSlash(ref.File)
	if file == "" {
		return string(ref.ID) + segmentExt
	}
	return file
}

// segmentFile returns the file name of ref within its generation directory.
func segmentFile(ref core.SegmentRef) string {
	return path.Base(segmentPath(ref))
}

// clearDir removes an existing directory at dir so it can be created again
// or used as a rename target. A non-directory there is never removed: it is
// not something a published index put in the output directory, so removing it
// could destroy data that has nothing to do with this index.
func clearDir(dir, rel string) error {
	info, err := os.Lstat(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("publish: inspect %s: %w", rel, err)
	}
	if !info.IsDir() {
		return &core.IntegrityError{Path: rel, Reason: "not a directory"}
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("publish: remove %s: %w", rel, err)
	}
	return nil
}

// readIfExists reads file, reporting whether it was there at all.
func readIfExists(file string) ([]byte, bool, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return data, true, nil
}

// isGenerationDir reports whether name is a generation directory produced by
// GenerationDir, so pruning never touches anything else in the output.
func isGenerationDir(name string) bool {
	digits, ok := strings.CutPrefix(name, generationPrefix)
	if !ok || digits == "" {
		return false
	}
	for _, r := range digits {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
