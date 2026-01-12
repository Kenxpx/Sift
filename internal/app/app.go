// Package app wires the Sift packages into the operations a user asks for:
// build an index, query it, summarize it, check it and watch the corpus for
// changes.
//
// Every operation starts from a corpus root and ends in a value the caller can
// render; nothing here formats output or reads command-line arguments, so the
// same App backs both the command line and the HTTP server. The only state an
// App holds is its clock, which lets a test publish byte-identical manifests.
package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"sift/internal/cache"
	"sift/internal/config"
	"sift/internal/core"
	"sift/internal/extract"
	"sift/internal/index"
	"sift/internal/manifest"
	"sift/internal/publish"
	"sift/internal/scan"
	"sift/internal/search"
	"sift/internal/stats"
	"sift/internal/store"
	"sift/internal/token"
	"sift/internal/validate"
	"sift/internal/watch"
)

// App performs the Sift operations against a corpus on disk. The zero value
// is usable and behaves exactly like the value New returns.
type App struct {
	// Clock stamps published manifests. A nil clock means the system clock.
	Clock core.Clock
}

// New returns an App that stamps manifests with the wall-clock time.
func New() App {
	return App{Clock: core.SystemClock{}}
}

// Index rebuilds the index of the corpus at root and publishes it as a new
// generation, returning the manifest it committed.
//
// Every file the scanner accepts is read once. A file whose contents still
// hash to the value recorded in the extraction cache reuses the cached
// document and tokens instead of being extracted and tokenized again; every
// other file is extracted afresh. Binary files are skipped, as are files that
// disappear between the scan and the read, because neither makes the corpus
// unindexable. The cache is pruned to the files that exist now, so it can
// never grow without bound, and it is only reused when the configuration that
// produced it still hashes to the configuration in force, because tokenization
// depends on that configuration.
func (a App) Index(root string) (core.Manifest, error) {
	cfg, err := a.Config(root)
	if err != nil {
		return core.Manifest{}, err
	}
	files, err := scan.Walk(cfg)
	if err != nil {
		return core.Manifest{}, fmt.Errorf("app: index %s: %w", cfg.Root, err)
	}

	entries := a.loadCache(cfg)
	idx := index.New()
	keep := make(map[core.DocID]bool, len(files))
	for _, f := range files {
		data, err := os.ReadFile(f.Abs)
		if err != nil {
			// The scanner accepted this file, so it vanished or became
			// unreadable since. Skipping it matches the scanner, which also
			// omits a file it cannot open rather than failing the run.
			continue
		}
		id := core.DocID(f.Rel)
		entry, ok := entries.Get(id, store.SHA256Bytes(data))
		if !ok {
			doc, err := extract.Extract(f, data)
			if err != nil {
				if errors.Is(err, extract.ErrBinary) {
					continue
				}
				return core.Manifest{}, fmt.Errorf("app: index %s: %w", f.Rel, err)
			}
			entry = cache.Entry{
				ContentHash: doc.ContentHash,
				Doc:         doc,
				Tokens:      token.Tokenize(doc.Body, cfg),
			}
			entries.Put(id, entry)
		}
		idx.Add(entry.Doc, entry.Tokens)
		keep[id] = true
	}
	entries.Prune(keep)

	m, err := publish.Publish(cfg, idx, entries, a.clock())
	if err != nil {
		return core.Manifest{}, fmt.Errorf("app: index %s: %w", cfg.Root, err)
	}
	return m, nil
}

// Search answers one query against the index published for the corpus at root.
// The published generation is validated before any segment is read, so a
// damaged index yields a *core.IntegrityError and never a partial answer, and
// a corpus that was never indexed yields an error matching core.ErrNotFound.
// An unparsable query yields an error matching core.ErrQuery.
func (a App) Search(root string, opts core.SearchOptions) (core.SearchReport, error) {
	idx, _, err := a.load(root)
	if err != nil {
		return core.SearchReport{}, err
	}
	return search.Run(idx, opts)
}

// Stats summarizes the index published for the corpus at root.
func (a App) Stats(root string) (stats.Corpus, error) {
	idx, _, err := a.load(root)
	if err != nil {
		return stats.Corpus{}, err
	}
	return stats.Compute(idx), nil
}

// Validate describes the index published for the corpus at root and lists
// every problem with it, including drift from the current configuration. A
// corpus with no readable manifest is an error; a corpus with a damaged index
// is a successful call whose findings hold the problems.
func (a App) Validate(root string) (validate.Findings, error) {
	cfg, err := a.Config(root)
	if err != nil {
		return validate.Findings{}, err
	}
	f, err := validate.Report(cfg)
	if err != nil {
		return validate.Findings{}, fmt.Errorf("app: validate %s: %w", cfg.Root, err)
	}
	return f, nil
}

// Watch scans the corpus at root and reports both the files it holds now and
// the changes since the prev scan. The returned scan is what the caller passes
// as prev next time; a nil prev reports every file as added.
func (a App) Watch(root string, prev []core.FileRef) ([]core.FileRef, []watch.Change, error) {
	cfg, err := a.Config(root)
	if err != nil {
		return nil, nil, err
	}
	now, changes, err := watch.Poll(cfg, prev)
	if err != nil {
		return nil, nil, fmt.Errorf("app: watch %s: %w", cfg.Root, err)
	}
	return now, changes, nil
}

// Config resolves the configuration of the corpus at root, applying
// <root>/.sift.json over the defaults. An empty root means the working
// directory. An unusable configuration yields a *core.ConfigError.
func (a App) Config(root string) (core.Config, error) {
	if root == "" {
		root = "."
	}
	cfg, err := config.Load(filepath.Clean(root))
	if err != nil {
		return core.Config{}, fmt.Errorf("app: config %s: %w", root, err)
	}
	return cfg, nil
}

// load reads the published index of the corpus at root.
func (a App) load(root string) (*index.Index, core.Manifest, error) {
	cfg, err := a.Config(root)
	if err != nil {
		return nil, core.Manifest{}, err
	}
	idx, m, err := publish.Load(cfg)
	if err != nil {
		return nil, core.Manifest{}, fmt.Errorf("app: load %s: %w", cfg.Root, err)
	}
	return idx, m, nil
}

// loadCache returns the extraction cache that may be reused for cfg. A cache
// belongs to the configuration that produced it: tokenization depends on the
// minimum term length and the stopword list, so a cache published under a
// different configuration hash is dropped rather than trusted, and a rebuild
// after a configuration change re-tokenizes every document.
func (a App) loadCache(cfg core.Config) *cache.Store {
	out := publish.OutputPath(cfg)
	m, err := manifest.Load(out)
	if err != nil || m.ConfigHash != config.Hash(cfg) {
		return cache.New()
	}
	entries, err := cache.Load(filepath.Join(out, core.CacheFile))
	if err != nil {
		return cache.New()
	}
	return entries
}

// clock returns the clock to stamp manifests with.
func (a App) clock() core.Clock {
	if a.Clock == nil {
		return core.SystemClock{}
	}
	return a.Clock
}
