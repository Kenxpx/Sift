// Package workspace keeps the registry of corpora a user works with: the name,
// root and output directory of each one, plus which corpus is active. The
// registry is a single JSON file at the workspace root, so switching between
// corpora needs no other state and the file can be committed or copied.
package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"sift/internal/core"
	"sift/internal/store"
)

// File is the registry file name, relative to the workspace root.
const File = ".sift-workspace.json"

// Corpus is one registered corpus.
type Corpus struct {
	// Name identifies the corpus within the workspace. It is the registry key,
	// so it must be non-empty and must not look like a path.
	Name string
	// Root is the corpus root directory.
	Root string
	// OutputDir is where the corpus publishes generations. An absolute value is
	// used as-is, a relative one resolves against Root and an empty one means
	// the default output directory. Resolve it with InferOutputPath.
	OutputDir string
}

// Workspace is the set of registered corpora together with the active one.
type Workspace struct {
	// Active is the name of the selected corpus, or "" when none is selected.
	Active string
	// Corpora is keyed by corpus name.
	Corpora map[string]Corpus
}

// New returns an empty workspace with no active corpus.
func New() *Workspace {
	return &Workspace{Corpora: make(map[string]Corpus)}
}

// Load reads the registry from <root>/.sift-workspace.json.
//
// The returned workspace is never nil. When the file does not exist Load
// returns an empty workspace and an error wrapping core.ErrNotFound, so a
// caller that treats a missing registry as "nothing registered yet" can ignore
// that single error and keep using the value. Entries are normalized on the
// way in: the map key is the authoritative corpus name, entries under an empty
// key are dropped, and an Active naming an unregistered corpus is cleared.
func Load(root string) (*Workspace, error) {
	w := New()
	path := filepath.Join(root, File)
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return w, fmt.Errorf("workspace: %s: %w", path, core.ErrNotFound)
		}
		return w, fmt.Errorf("workspace: %s: %w", path, err)
	}
	var raw Workspace
	if err := store.ReadJSON(path, &raw); err != nil {
		return w, fmt.Errorf("workspace: read %s: %w", path, err)
	}
	for name, c := range raw.Corpora {
		if name == "" {
			continue
		}
		c.Name = name
		w.Corpora[name] = c
	}
	if _, ok := w.Corpora[raw.Active]; ok {
		w.Active = raw.Active
	}
	return w, nil
}

// Save writes the registry to <root>/.sift-workspace.json atomically, so a
// concurrent reader sees either the previous registry or the new one. A nil
// workspace is written as an empty registry, every entry is stored under its
// own name, and an Active naming an unregistered corpus is cleared.
func Save(root string, w *Workspace) error {
	if w == nil {
		w = New()
	}
	out := Workspace{Active: w.Active, Corpora: make(map[string]Corpus, len(w.Corpora))}
	for name, c := range w.Corpora {
		if name == "" {
			continue
		}
		c.Name = name
		out.Corpora[name] = c
	}
	if _, ok := out.Corpora[out.Active]; !ok {
		out.Active = ""
	}
	path := filepath.Join(root, File)
	if err := store.EnsureDir(filepath.Dir(path)); err != nil {
		return fmt.Errorf("workspace: create %s: %w", filepath.Dir(path), err)
	}
	if err := store.WriteJSONAtomic(path, out); err != nil {
		return fmt.Errorf("workspace: write %s: %w", path, err)
	}
	return nil
}

// List returns every registered corpus sorted by name.
func (w *Workspace) List() []Corpus {
	out := make([]Corpus, 0, len(w.Corpora))
	for name, c := range w.Corpora {
		c.Name = name
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Get returns the named corpus and reports whether it is registered.
func (w *Workspace) Get(name string) (Corpus, bool) {
	c, ok := w.Corpora[name]
	if !ok {
		return Corpus{}, false
	}
	c.Name = name
	return c, true
}

// Add registers a corpus. The name must be non-empty, must not look like a
// path and must not already be registered, and the root must be non-empty;
// every rejection is a *core.ConfigError. Registering the first corpus also
// makes it active.
func (w *Workspace) Add(c Corpus) error {
	name := strings.TrimSpace(c.Name)
	switch {
	case name == "":
		return &core.ConfigError{Field: "name", Reason: "must not be empty"}
	case strings.ContainsAny(name, `/\`), name == ".", name == "..":
		return &core.ConfigError{Field: "name", Reason: "must not be a path: " + name}
	}
	root := strings.TrimSpace(c.Root)
	if root == "" {
		return &core.ConfigError{Field: "root", Reason: "corpus " + name + " needs a root directory"}
	}
	if w.Corpora == nil {
		w.Corpora = make(map[string]Corpus)
	}
	if _, ok := w.Corpora[name]; ok {
		return &core.ConfigError{Field: "name", Reason: "already registered: " + name}
	}
	out := strings.TrimSpace(c.OutputDir)
	if out != "" {
		out = filepath.Clean(out)
	}
	w.Corpora[name] = Corpus{Name: name, Root: filepath.Clean(root), OutputDir: out}
	if w.Active == "" {
		w.Active = name
	}
	return nil
}

// Remove deletes a corpus and reports whether it was registered. Removing the
// active corpus selects the first remaining corpus in name order, or clears
// the selection when none remain.
func (w *Workspace) Remove(name string) bool {
	if _, ok := w.Corpora[name]; !ok {
		return false
	}
	delete(w.Corpora, name)
	if w.Active == name {
		w.Active = ""
		if rest := w.List(); len(rest) > 0 {
			w.Active = rest[0].Name
		}
	}
	return true
}

// SetActive selects a registered corpus. The empty name clears the selection;
// any other unregistered name is rejected with a *core.ConfigError.
func (w *Workspace) SetActive(name string) error {
	if name == "" {
		w.Active = ""
		return nil
	}
	if _, ok := w.Corpora[name]; !ok {
		return &core.ConfigError{Field: "active", Reason: "not registered: " + name}
	}
	w.Active = name
	return nil
}

// InferOutputPath resolves where a corpus publishes its generations: an
// absolute OutputDir is used as-is, a relative one resolves against the corpus
// root, and an empty one means <Root>/sift-out.
func InferOutputPath(c Corpus) string {
	dir := strings.TrimSpace(c.OutputDir)
	if dir == "" {
		dir = core.DefaultOutputDir
	}
	if filepath.IsAbs(dir) {
		return filepath.Clean(dir)
	}
	return filepath.Join(c.Root, dir)
}
