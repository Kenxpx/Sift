// Package cache keeps extraction results keyed by document ID so a rebuild can
// reuse the document and tokens of every source file whose contents have not
// changed since the previous generation.
//
// A cache is an optimization and never a source of truth: it is stored next to
// a published index as a single JSON file, and Load turns a missing, unreadable
// or malformed file into an empty cache so callers can always rebuild from the
// corpus itself. The file bytes are canonical, so Digest reports the same hex
// SHA-256 that hashing the saved file would.
package cache

import (
	"encoding/json"
	"fmt"
	"os"

	"sift/internal/core"
	"sift/internal/store"
)

// filePerm is the mode used for the cache file.
const filePerm os.FileMode = 0o644

// Entry is one cached extraction: the document and tokens produced from a
// source file whose contents hashed to ContentHash.
type Entry struct {
	// ContentHash is the hex SHA-256 of the raw source file the entry was
	// built from. An entry is only reusable while this value still matches.
	ContentHash string
	// Doc is the extracted document.
	Doc core.Document
	// Tokens are the tokens produced from Doc.Body, in ascending position
	// order.
	Tokens []core.Token
}

// Store holds cache entries keyed by document ID.
type Store struct {
	// Entries maps the string form of a core.DocID to its cached extraction.
	Entries map[string]Entry
}

// New returns an empty cache ready for use.
func New() *Store {
	return &Store{Entries: make(map[string]Entry)}
}

// Get returns the cached entry for id when one exists and its recorded content
// hash equals contentHash. The returned entry is a copy, so mutating it never
// affects the cache. A nil or empty store always reports a miss.
func (s *Store) Get(id core.DocID, contentHash string) (Entry, bool) {
	if s == nil || len(s.Entries) == 0 {
		return Entry{}, false
	}
	e, ok := s.Entries[string(id)]
	if !ok || e.ContentHash != contentHash {
		return Entry{}, false
	}
	return cloneEntry(e), true
}

// Put stores e under id, replacing any previous entry. The entry is copied, so
// later changes to the caller's fields map or token slice do not reach the
// cache. Put on a nil store does nothing.
func (s *Store) Put(id core.DocID, e Entry) {
	if s == nil {
		return
	}
	if s.Entries == nil {
		s.Entries = make(map[string]Entry)
	}
	s.Entries[string(id)] = cloneEntry(e)
}

// Prune removes every entry whose document ID is not marked true in keep and
// returns the number of entries removed. A nil keep map empties the cache.
func (s *Store) Prune(keep map[core.DocID]bool) int {
	if s == nil || len(s.Entries) == 0 {
		return 0
	}
	removed := 0
	for id := range s.Entries {
		if !keep[core.DocID(id)] {
			delete(s.Entries, id)
			removed++
		}
	}
	return removed
}

// Load reads the cache file at path. A missing, unreadable or malformed file
// yields an empty cache and no error, because a cache can always be rebuilt;
// the returned error exists for future use and is always nil today.
func Load(path string) (*Store, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return New(), nil
	}
	var loaded Store
	if err := json.Unmarshal(data, &loaded); err != nil {
		return New(), nil
	}
	if loaded.Entries == nil {
		return New(), nil
	}
	return &Store{Entries: loaded.Entries}, nil
}

// Save writes s to path atomically, so a reader sees either the previous cache
// or the complete new one. A nil store is written as an empty cache.
func Save(path string, s *Store) error {
	data, err := encode(s)
	if err != nil {
		return fmt.Errorf("cache: encode %s: %w", path, err)
	}
	if err := store.WriteFileAtomic(path, data, filePerm); err != nil {
		return fmt.Errorf("cache: write %s: %w", path, err)
	}
	return nil
}

// Digest returns the hex SHA-256 of the canonical bytes of s. It is the digest
// a manifest records for the cache file, and it equals the digest of the file
// Save writes. A nil store digests as an empty cache.
func Digest(s *Store) string {
	data, err := encode(s)
	if err != nil {
		return ""
	}
	return store.SHA256Bytes(data)
}

// encode renders s as the canonical cache bytes: two-space indented JSON with
// a trailing newline, with map keys sorted by encoding/json. A nil store and a
// store with a nil map both encode as an empty cache.
func encode(s *Store) ([]byte, error) {
	entries := map[string]Entry{}
	if s != nil && s.Entries != nil {
		entries = s.Entries
	}
	data, err := json.MarshalIndent(Store{Entries: entries}, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// cloneEntry copies the parts of an entry that callers could otherwise share
// with the cache: the document fields map and the token slice.
func cloneEntry(e Entry) Entry {
	out := e
	if e.Doc.Fields != nil {
		fields := make(map[string]string, len(e.Doc.Fields))
		for k, v := range e.Doc.Fields {
			fields[k] = v
		}
		out.Doc.Fields = fields
	}
	if e.Tokens != nil {
		tokens := make([]core.Token, len(e.Tokens))
		copy(tokens, e.Tokens)
		out.Tokens = tokens
	}
	return out
}
