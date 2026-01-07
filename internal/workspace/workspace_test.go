package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"sift/internal/core"
)

func TestAddRegistersAndActivatesFirst(t *testing.T) {
	w := New()
	if err := w.Add(Corpus{Name: "docs", Root: filepath.Join("corpora", "docs")}); err != nil {
		t.Fatalf("Add(docs) = %v, want nil", err)
	}
	if err := w.Add(Corpus{Name: "code", Root: filepath.Join("corpora", "code"), OutputDir: "build/idx"}); err != nil {
		t.Fatalf("Add(code) = %v, want nil", err)
	}
	if w.Active != "docs" {
		t.Errorf("Active = %q, want %q", w.Active, "docs")
	}
	if len(w.Corpora) != 2 {
		t.Errorf("len(Corpora) = %d, want 2", len(w.Corpora))
	}
	got, ok := w.Get("code")
	if !ok {
		t.Fatal("Get(code) = _, false, want true")
	}
	if want := filepath.Join("corpora", "code"); got.Root != want {
		t.Errorf("Root = %q, want %q", got.Root, want)
	}
	if want := filepath.Join("build", "idx"); got.OutputDir != want {
		t.Errorf("OutputDir = %q, want %q", got.OutputDir, want)
	}
	if _, ok := w.Get("missing"); ok {
		t.Error("Get(missing) = _, true, want false")
	}
}

func TestAddRejectsInvalidCorpora(t *testing.T) {
	tests := []struct {
		name      string
		corpus    Corpus
		wantField string
	}{
		{name: "empty name", corpus: Corpus{Name: "  ", Root: "r"}, wantField: "name"},
		{name: "slash in name", corpus: Corpus{Name: "a/b", Root: "r"}, wantField: "name"},
		{name: "backslash in name", corpus: Corpus{Name: `a\b`, Root: "r"}, wantField: "name"},
		{name: "dot name", corpus: Corpus{Name: "..", Root: "r"}, wantField: "name"},
		{name: "empty root", corpus: Corpus{Name: "docs", Root: " "}, wantField: "root"},
		{name: "duplicate", corpus: Corpus{Name: "taken", Root: "r"}, wantField: "name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := New()
			if err := w.Add(Corpus{Name: "taken", Root: "somewhere"}); err != nil {
				t.Fatalf("Add(taken) = %v, want nil", err)
			}
			err := w.Add(tt.corpus)
			if !errors.Is(err, core.ErrConfig) {
				t.Fatalf("Add() = %v, want an error matching core.ErrConfig", err)
			}
			var cfgErr *core.ConfigError
			if !errors.As(err, &cfgErr) {
				t.Fatalf("Add() = %v, want a *core.ConfigError", err)
			}
			if cfgErr.Field != tt.wantField {
				t.Errorf("Field = %q, want %q", cfgErr.Field, tt.wantField)
			}
			if len(w.Corpora) != 1 {
				t.Errorf("len(Corpora) = %d, want 1, the rejected corpus must not be registered", len(w.Corpora))
			}
		})
	}
}

func TestListSortsByName(t *testing.T) {
	w := New()
	for _, name := range []string{"zulu", "alpha", "mike"} {
		if err := w.Add(Corpus{Name: name, Root: "roots/" + name}); err != nil {
			t.Fatalf("Add(%s) = %v, want nil", name, err)
		}
	}
	got := w.List()
	want := []string{"alpha", "mike", "zulu"}
	if len(got) != len(want) {
		t.Fatalf("len(List()) = %d, want %d", len(got), len(want))
	}
	for i, c := range got {
		if c.Name != want[i] {
			t.Errorf("List()[%d].Name = %q, want %q", i, c.Name, want[i])
		}
	}
}

func TestRemoveReassignsActive(t *testing.T) {
	w := New()
	for _, name := range []string{"alpha", "bravo", "charlie"} {
		if err := w.Add(Corpus{Name: name, Root: "roots/" + name}); err != nil {
			t.Fatalf("Add(%s) = %v, want nil", name, err)
		}
	}
	if w.Active != "alpha" {
		t.Fatalf("Active = %q, want %q", w.Active, "alpha")
	}
	if w.Remove("nope") {
		t.Error("Remove(nope) = true, want false")
	}
	if !w.Remove("alpha") {
		t.Fatal("Remove(alpha) = false, want true")
	}
	if w.Active != "bravo" {
		t.Errorf("Active = %q, want %q after removing the active corpus", w.Active, "bravo")
	}
	if !w.Remove("charlie") {
		t.Fatal("Remove(charlie) = false, want true")
	}
	if w.Active != "bravo" {
		t.Errorf("Active = %q, want %q after removing an inactive corpus", w.Active, "bravo")
	}
	if !w.Remove("bravo") {
		t.Fatal("Remove(bravo) = false, want true")
	}
	if w.Active != "" {
		t.Errorf("Active = %q, want %q once the registry is empty", w.Active, "")
	}
	if len(w.List()) != 0 {
		t.Errorf("len(List()) = %d, want 0", len(w.List()))
	}
}

func TestSetActive(t *testing.T) {
	w := New()
	if err := w.Add(Corpus{Name: "docs", Root: "r"}); err != nil {
		t.Fatalf("Add(docs) = %v, want nil", err)
	}
	if err := w.SetActive("ghost"); !errors.Is(err, core.ErrConfig) {
		t.Errorf("SetActive(ghost) = %v, want an error matching core.ErrConfig", err)
	}
	if w.Active != "docs" {
		t.Errorf("Active = %q, want %q after a rejected SetActive", w.Active, "docs")
	}
	if err := w.SetActive(""); err != nil {
		t.Errorf("SetActive() = %v, want nil", err)
	}
	if w.Active != "" {
		t.Errorf("Active = %q, want %q", w.Active, "")
	}
	if err := w.SetActive("docs"); err != nil {
		t.Errorf("SetActive(docs) = %v, want nil", err)
	}
	if w.Active != "docs" {
		t.Errorf("Active = %q, want %q", w.Active, "docs")
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	root := t.TempDir()
	w := New()
	if err := w.Add(Corpus{Name: "docs", Root: filepath.Join(root, "docs")}); err != nil {
		t.Fatalf("Add(docs) = %v, want nil", err)
	}
	if err := w.Add(Corpus{Name: "code", Root: filepath.Join(root, "code"), OutputDir: "idx"}); err != nil {
		t.Fatalf("Add(code) = %v, want nil", err)
	}
	if err := w.SetActive("code"); err != nil {
		t.Fatalf("SetActive(code) = %v, want nil", err)
	}
	if err := Save(root, w); err != nil {
		t.Fatalf("Save() = %v, want nil", err)
	}
	if _, err := os.Stat(filepath.Join(root, File)); err != nil {
		t.Fatalf("Stat(%s) = %v, want the registry file to exist", File, err)
	}
	got, err := Load(root)
	if err != nil {
		t.Fatalf("Load() = %v, want nil", err)
	}
	if got.Active != "code" {
		t.Errorf("Active = %q, want %q", got.Active, "code")
	}
	list := got.List()
	if len(list) != 2 {
		t.Fatalf("len(List()) = %d, want 2", len(list))
	}
	if list[0].Name != "code" || list[0].OutputDir != "idx" {
		t.Errorf("List()[0] = %+v, want name code and output dir idx", list[0])
	}
	if want := filepath.Join(root, "docs"); list[1].Root != want {
		t.Errorf("List()[1].Root = %q, want %q", list[1].Root, want)
	}
}

func TestLoadMissingRegistryIsNotFound(t *testing.T) {
	root := t.TempDir()
	w, err := Load(root)
	if !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("Load() = %v, want an error matching core.ErrNotFound", err)
	}
	if w == nil {
		t.Fatal("Load() returned a nil workspace, want an empty usable one")
	}
	if len(w.List()) != 0 {
		t.Errorf("len(List()) = %d, want 0", len(w.List()))
	}
	if err := w.Add(Corpus{Name: "docs", Root: root}); err != nil {
		t.Errorf("Add() on the returned workspace = %v, want nil", err)
	}
}

func TestLoadNormalizesNamesAndActive(t *testing.T) {
	root := t.TempDir()
	raw := `{"Active":"ghost","Corpora":{"alpha":{"Name":"stale","Root":"/corpora/alpha","OutputDir":"out"},"":{"Name":"blank","Root":"/corpora/blank"}}}`
	if err := os.WriteFile(filepath.Join(root, File), []byte(raw), 0o644); err != nil {
		t.Fatalf("WriteFile() = %v, want nil", err)
	}
	w, err := Load(root)
	if err != nil {
		t.Fatalf("Load() = %v, want nil", err)
	}
	if w.Active != "" {
		t.Errorf("Active = %q, want %q because the recorded corpus is not registered", w.Active, "")
	}
	list := w.List()
	if len(list) != 1 {
		t.Fatalf("len(List()) = %d, want 1, the blank key is dropped", len(list))
	}
	if list[0].Name != "alpha" {
		t.Errorf("List()[0].Name = %q, want %q because the map key is authoritative", list[0].Name, "alpha")
	}
}

func TestLoadMalformedRegistryFails(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, File), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("WriteFile() = %v, want nil", err)
	}
	w, err := Load(root)
	if err == nil {
		t.Fatal("Load() = nil, want an error")
	}
	if errors.Is(err, core.ErrNotFound) {
		t.Errorf("Load() = %v, want an error that is not core.ErrNotFound", err)
	}
	if w == nil || len(w.List()) != 0 {
		t.Errorf("Load() returned %v, want an empty usable workspace", w)
	}
}

func TestSaveClearsUnknownActive(t *testing.T) {
	root := t.TempDir()
	if err := Save(root, &Workspace{Active: "ghost"}); err != nil {
		t.Fatalf("Save() = %v, want nil", err)
	}
	got, err := Load(root)
	if err != nil {
		t.Fatalf("Load() = %v, want nil", err)
	}
	if got.Active != "" {
		t.Errorf("Active = %q, want %q", got.Active, "")
	}
	if len(got.List()) != 0 {
		t.Errorf("len(List()) = %d, want 0", len(got.List()))
	}
	if err := Save(root, nil); err != nil {
		t.Errorf("Save(nil) = %v, want nil", err)
	}
}

func TestInferOutputPath(t *testing.T) {
	root := t.TempDir()
	abs := filepath.Join(root, "elsewhere", "idx")
	tests := []struct {
		name   string
		corpus Corpus
		want   string
	}{
		{name: "empty uses the default", corpus: Corpus{Root: root}, want: filepath.Join(root, core.DefaultOutputDir)},
		{name: "relative resolves against root", corpus: Corpus{Root: root, OutputDir: "build/idx"}, want: filepath.Join(root, "build", "idx")},
		{name: "absolute is used as is", corpus: Corpus{Root: root, OutputDir: abs}, want: abs},
		{name: "blank counts as empty", corpus: Corpus{Root: root, OutputDir: "   "}, want: filepath.Join(root, core.DefaultOutputDir)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := InferOutputPath(tt.corpus); got != tt.want {
				t.Errorf("InferOutputPath() = %q, want %q", got, tt.want)
			}
		})
	}
}
