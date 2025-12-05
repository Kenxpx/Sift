package store

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"sift/internal/core"
)

// sha256 of "abc", the standard test vector.
const abcDigest = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"

func TestWriteFileAtomicCreatesParentsAndOverwrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a", "b", "seg.json")

	if err := WriteFileAtomic(path, []byte("first"), 0o644); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := WriteFileAtomic(path, []byte("second"), 0o644); err != nil {
		t.Fatalf("second write: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != "second" {
		t.Errorf("contents = %q, want %q", got, "second")
	}

	// The temporary file must not survive a successful write.
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "seg.json" {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Errorf("directory holds %v, want only [seg.json]", names)
	}
}

func TestWriteFileAtomicAppliesPerm(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix file modes are not represented on windows")
	}
	path := filepath.Join(t.TempDir(), "mode.txt")
	if err := WriteFileAtomic(path, []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("mode = %v, want %v", got, os.FileMode(0o600))
	}
}

func TestWriteJSONAtomicEncoding(t *testing.T) {
	path := filepath.Join(t.TempDir(), "m.json")
	value := map[string]any{
		"zeta":  1,
		"alpha": []string{"x", "y"},
	}
	if err := WriteJSONAtomic(path, value); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	want := "{\n  \"alpha\": [\n    \"x\",\n    \"y\"\n  ],\n  \"zeta\": 1\n}\n"
	if string(got) != want {
		t.Errorf("encoding =\n%q\nwant\n%q", got, want)
	}

	// Re-encoding identical data must reproduce identical bytes.
	if err := WriteJSONAtomic(path, value); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	again, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read again: %v", err)
	}
	if string(again) != want {
		t.Errorf("second encoding differs: %q", again)
	}
}

func TestReadJSON(t *testing.T) {
	dir := t.TempDir()
	type payload struct {
		Name  string
		Count int
	}

	good := filepath.Join(dir, "good.json")
	if err := WriteJSONAtomic(good, payload{Name: "sift", Count: 7}); err != nil {
		t.Fatalf("write: %v", err)
	}
	var round payload
	if err := ReadJSON(good, &round); err != nil {
		t.Fatalf("read: %v", err)
	}
	if round.Name != "sift" || round.Count != 7 {
		t.Errorf("round trip = %+v, want {sift 7}", round)
	}

	missing := filepath.Join(dir, "missing.json")
	err := ReadJSON(missing, &round)
	if !errors.Is(err, core.ErrNotFound) {
		t.Errorf("missing file error = %v, want core.ErrNotFound", err)
	}

	bad := filepath.Join(dir, "bad.json")
	if err := WriteFileAtomic(bad, []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write bad: %v", err)
	}
	err = ReadJSON(bad, &round)
	if err == nil {
		t.Fatal("malformed JSON: got nil error")
	}
	if errors.Is(err, core.ErrNotFound) {
		t.Errorf("malformed JSON reported as not found: %v", err)
	}
}

func TestSHA256BytesAndFile(t *testing.T) {
	if got := SHA256Bytes([]byte("abc")); got != abcDigest {
		t.Errorf("SHA256Bytes = %s, want %s", got, abcDigest)
	}
	if got := SHA256Bytes(nil); got != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Errorf("SHA256Bytes(nil) = %s, want the empty digest", got)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "abc.txt")
	if err := WriteFileAtomic(path, []byte("abc"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := SHA256File(path)
	if err != nil {
		t.Fatalf("SHA256File: %v", err)
	}
	if got != abcDigest {
		t.Errorf("SHA256File = %s, want %s", got, abcDigest)
	}

	if _, err := SHA256File(filepath.Join(dir, "nope.txt")); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("missing file error = %v, want core.ErrNotFound", err)
	}
}

func TestEnsureDir(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "x", "y", "z")
	if err := EnsureDir(nested); err != nil {
		t.Fatalf("first EnsureDir: %v", err)
	}
	if err := EnsureDir(nested); err != nil {
		t.Fatalf("second EnsureDir: %v", err)
	}
	info, err := os.Stat(nested)
	if err != nil || !info.IsDir() {
		t.Fatalf("stat %s: info=%v err=%v", nested, info, err)
	}

	file := filepath.Join(root, "file.txt")
	if err := WriteFileAtomic(file, []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := EnsureDir(file); err == nil {
		t.Error("EnsureDir over an existing file: got nil error")
	}
}

func TestRemoveAllContents(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "staging")
	if err := WriteFileAtomic(filepath.Join(dir, "one.json"), []byte("1"), 0o644); err != nil {
		t.Fatalf("write one: %v", err)
	}
	if err := WriteFileAtomic(filepath.Join(dir, "sub", "two.json"), []byte("2"), 0o644); err != nil {
		t.Fatalf("write two: %v", err)
	}

	if err := RemoveAllContents(dir); err != nil {
		t.Fatalf("RemoveAllContents: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("dir must survive: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("dir holds %d entries, want 0", len(entries))
	}

	if err := RemoveAllContents(filepath.Join(root, "absent")); err != nil {
		t.Errorf("missing dir = %v, want nil", err)
	}

	file := filepath.Join(root, "plain.txt")
	if err := WriteFileAtomic(file, []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := RemoveAllContents(file); err == nil {
		t.Error("RemoveAllContents on a file: got nil error")
	}
}

func TestCopyDir(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")
	dst := filepath.Join(root, "dst")

	files := map[string]string{
		filepath.Join(src, "a.txt"):             "alpha",
		filepath.Join(src, "deep", "b.txt"):     "beta",
		filepath.Join(src, "deep", "c", "d.md"): "delta",
	}
	for path, body := range files {
		if err := WriteFileAtomic(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	if err := CopyDir(src, dst); err != nil {
		t.Fatalf("CopyDir: %v", err)
	}
	for path, body := range files {
		rel := strings.TrimPrefix(path, src)
		got, err := os.ReadFile(filepath.Join(dst, rel))
		if err != nil {
			t.Fatalf("read copy of %s: %v", rel, err)
		}
		if string(got) != body {
			t.Errorf("copy of %s = %q, want %q", rel, got, body)
		}
	}

	// Copying again over an existing tree replaces the files in place.
	if err := WriteFileAtomic(filepath.Join(src, "a.txt"), []byte("alpha2"), 0o644); err != nil {
		t.Fatalf("rewrite source: %v", err)
	}
	if err := CopyDir(src, dst); err != nil {
		t.Fatalf("second CopyDir: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dst, "a.txt"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "alpha2" {
		t.Errorf("copy after update = %q, want %q", got, "alpha2")
	}

	if err := CopyDir(filepath.Join(root, "absent"), filepath.Join(root, "out")); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("missing src error = %v, want core.ErrNotFound", err)
	}
	if err := CopyDir(filepath.Join(src, "a.txt"), filepath.Join(root, "out")); err == nil {
		t.Error("CopyDir with a file source: got nil error")
	}
}
