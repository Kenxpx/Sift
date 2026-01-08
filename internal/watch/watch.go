// Package watch turns two scans of a corpus into the list of differences
// between them, so an incremental indexer can re-extract only what moved. It
// polls on demand rather than subscribing to filesystem events: a poll is
// portable, has no background goroutine to leak, and produces the same plan
// for the same pair of scans.
package watch

import (
	"fmt"
	"sort"

	"sift/internal/core"
	"sift/internal/scan"
)

// Change kinds reported by Plan.
const (
	// KindAdded marks a file present now but not before.
	KindAdded = "added"
	// KindModified marks a file whose size or modification time moved.
	KindModified = "modified"
	// KindRemoved marks a file present before but not now.
	KindRemoved = "removed"
)

// Change is one difference between two scans of a corpus.
type Change struct {
	// Rel is the slash-separated path relative to the corpus root.
	Rel string
	// Kind is KindAdded, KindModified or KindRemoved.
	Kind string
}

// Plan reports the changes that turn prev into now, sorted by Rel and never
// nil. A file counts as modified when its size or modification time differs, so
// a file rewritten with identical bytes in the same instant is reported as
// unchanged, which is what the extraction cache assumes as well. When either
// scan lists the same Rel twice the last entry wins, so the plan holds at most
// one change per path.
func Plan(prev []core.FileRef, now []core.FileRef) []Change {
	before := byRel(prev)
	after := byRel(now)
	changes := make([]Change, 0, len(before)+len(after))
	for rel, cur := range after {
		old, ok := before[rel]
		switch {
		case !ok:
			changes = append(changes, Change{Rel: rel, Kind: KindAdded})
		case moved(old, cur):
			changes = append(changes, Change{Rel: rel, Kind: KindModified})
		}
	}
	for rel := range before {
		if _, ok := after[rel]; !ok {
			changes = append(changes, Change{Rel: rel, Kind: KindRemoved})
		}
	}
	sort.Slice(changes, func(i, j int) bool {
		if changes[i].Rel != changes[j].Rel {
			return changes[i].Rel < changes[j].Rel
		}
		return changes[i].Kind < changes[j].Kind
	})
	return changes
}

// Poll scans the corpus described by cfg and returns the files it holds now
// together with the changes since prev. The returned scan is what the caller
// should pass as prev on the next poll.
func Poll(cfg core.Config, prev []core.FileRef) ([]core.FileRef, []Change, error) {
	now, err := scan.Walk(cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("watch: scan %s: %w", cfg.Root, err)
	}
	return now, Plan(prev, now), nil
}

// byRel indexes a scan by relative path, keeping the last entry per path.
func byRel(refs []core.FileRef) map[string]core.FileRef {
	out := make(map[string]core.FileRef, len(refs))
	for _, f := range refs {
		out[f.Rel] = f
	}
	return out
}

// moved reports whether a file changed between two scans.
func moved(old, cur core.FileRef) bool {
	return old.Size != cur.Size || !old.ModTime.Equal(cur.ModTime)
}
