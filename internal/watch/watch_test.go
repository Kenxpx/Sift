package watch

import (
	"os"
	"path"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"sift/internal/core"
)

// at is a fixed instant the scans in these tests are built around.
var at = time.Date(2024, time.March, 1, 12, 0, 0, 0, time.UTC)

// ref builds a scan entry for a corpus-relative path.
func ref(rel string, size int64, mod time.Time) core.FileRef {
	return core.FileRef{Rel: rel, Abs: "/corpus/" + rel, Size: size, ModTime: mod, Ext: path.Ext(rel)}
}

func TestPlanClassifiesEveryChange(t *testing.T) {
	prev := []core.FileRef{
		ref("docs/intro.md", 100, at),
		ref("docs/gone.md", 20, at),
		ref("src/main.go", 500, at),
	}
	now := []core.FileRef{
		ref("docs/intro.md", 140, at),
		ref("src/main.go", 500, at),
		ref("src/new.go", 30, at),
	}
	got := Plan(prev, now)
	want := []Change{
		{Rel: "docs/gone.md", Kind: KindRemoved},
		{Rel: "docs/intro.md", Kind: KindModified},
		{Rel: "src/new.go", Kind: KindAdded},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Plan() = %+v, want %+v", got, want)
	}
}

func TestPlanTreatsModTimeChangeAsModified(t *testing.T) {
	prev := []core.FileRef{ref("a.txt", 10, at)}
	now := []core.FileRef{ref("a.txt", 10, at.Add(time.Second))}
	got := Plan(prev, now)
	if len(got) != 1 {
		t.Fatalf("len(Plan()) = %d, want 1", len(got))
	}
	if got[0].Kind != KindModified {
		t.Errorf("Kind = %q, want %q", got[0].Kind, KindModified)
	}
}

func TestPlanIdenticalScansReportNothing(t *testing.T) {
	scan := []core.FileRef{ref("a.txt", 10, at), ref("b.txt", 20, at)}
	got := Plan(scan, scan)
	if got == nil {
		t.Fatal("Plan() = nil, want an empty slice")
	}
	if len(got) != 0 {
		t.Errorf("Plan() = %+v, want no changes", got)
	}
}

func TestPlanHandlesEmptyScans(t *testing.T) {
	now := []core.FileRef{ref("a.txt", 1, at), ref("b.txt", 2, at)}
	if got := Plan(nil, nil); len(got) != 0 || got == nil {
		t.Errorf("Plan(nil, nil) = %+v, want an empty non-nil slice", got)
	}
	added := Plan(nil, now)
	want := []Change{{Rel: "a.txt", Kind: KindAdded}, {Rel: "b.txt", Kind: KindAdded}}
	if !reflect.DeepEqual(added, want) {
		t.Errorf("Plan(nil, now) = %+v, want %+v", added, want)
	}
	removed := Plan(now, nil)
	want = []Change{{Rel: "a.txt", Kind: KindRemoved}, {Rel: "b.txt", Kind: KindRemoved}}
	if !reflect.DeepEqual(removed, want) {
		t.Errorf("Plan(prev, nil) = %+v, want %+v", removed, want)
	}
}

func TestPlanSortsByRel(t *testing.T) {
	now := []core.FileRef{ref("z.md", 1, at), ref("a/deep.md", 1, at), ref("m.md", 1, at), ref("a.md", 1, at)}
	got := Plan(nil, now)
	want := []string{"a.md", "a/deep.md", "m.md", "z.md"}
	if len(got) != len(want) {
		t.Fatalf("len(Plan()) = %d, want %d", len(got), len(want))
	}
	for i, c := range got {
		if c.Rel != want[i] {
			t.Errorf("Plan()[%d].Rel = %q, want %q", i, c.Rel, want[i])
		}
	}
}

func TestPlanKeepsLastEntryPerPath(t *testing.T) {
	prev := []core.FileRef{ref("a.txt", 10, at), ref("a.txt", 99, at)}
	now := []core.FileRef{ref("a.txt", 99, at)}
	if got := Plan(prev, now); len(got) != 0 {
		t.Errorf("Plan() = %+v, want no changes because the last entry per path wins", got)
	}
}

// corpus writes the given corpus-relative files into a temporary root and
// returns a configuration pointing at it.
func corpus(t *testing.T, files map[string]string) core.Config {
	t.Helper()
	root := t.TempDir()
	for rel, body := range files {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("MkdirAll(%s) = %v, want nil", filepath.Dir(abs), err)
		}
		if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
			t.Fatalf("WriteFile(%s) = %v, want nil", abs, err)
		}
	}
	return core.Config{Root: root, OutputDir: core.DefaultOutputDir, MinTermLength: 2, SegmentDocs: 256}
}

func TestPollReportsAdditionsThenSettles(t *testing.T) {
	cfg := corpus(t, map[string]string{"a.txt": "alpha", "notes/b.md": "# beta"})
	now, changes, err := Poll(cfg, nil)
	if err != nil {
		t.Fatalf("Poll() = %v, want nil", err)
	}
	if len(now) != 2 {
		t.Fatalf("len(scan) = %d, want 2", len(now))
	}
	want := []Change{{Rel: "a.txt", Kind: KindAdded}, {Rel: "notes/b.md", Kind: KindAdded}}
	if !reflect.DeepEqual(changes, want) {
		t.Errorf("Poll() changes = %+v, want %+v", changes, want)
	}
	again, changes, err := Poll(cfg, now)
	if err != nil {
		t.Fatalf("Poll() = %v, want nil", err)
	}
	if len(again) != 2 {
		t.Errorf("len(scan) = %d, want 2", len(again))
	}
	if len(changes) != 0 {
		t.Errorf("Poll() changes = %+v, want none on an unchanged corpus", changes)
	}
}

func TestPollReportsRemoval(t *testing.T) {
	cfg := corpus(t, map[string]string{"a.txt": "alpha", "notes/b.md": "# beta"})
	prev, _, err := Poll(cfg, nil)
	if err != nil {
		t.Fatalf("Poll() = %v, want nil", err)
	}
	if err := os.Remove(filepath.Join(cfg.Root, "notes", "b.md")); err != nil {
		t.Fatalf("Remove() = %v, want nil", err)
	}
	now, changes, err := Poll(cfg, prev)
	if err != nil {
		t.Fatalf("Poll() = %v, want nil", err)
	}
	if len(now) != 1 {
		t.Errorf("len(scan) = %d, want 1", len(now))
	}
	want := []Change{{Rel: "notes/b.md", Kind: KindRemoved}}
	if !reflect.DeepEqual(changes, want) {
		t.Errorf("Poll() changes = %+v, want %+v", changes, want)
	}
}

func TestPollReportsModification(t *testing.T) {
	cfg := corpus(t, map[string]string{"a.txt": "alpha"})
	prev, _, err := Poll(cfg, nil)
	if err != nil {
		t.Fatalf("Poll() = %v, want nil", err)
	}
	abs := filepath.Join(cfg.Root, "a.txt")
	if err := os.WriteFile(abs, []byte("alpha and then some"), 0o644); err != nil {
		t.Fatalf("WriteFile() = %v, want nil", err)
	}
	later := prev[0].ModTime.Add(2 * time.Second)
	if err := os.Chtimes(abs, later, later); err != nil {
		t.Fatalf("Chtimes() = %v, want nil", err)
	}
	_, changes, err := Poll(cfg, prev)
	if err != nil {
		t.Fatalf("Poll() = %v, want nil", err)
	}
	want := []Change{{Rel: "a.txt", Kind: KindModified}}
	if !reflect.DeepEqual(changes, want) {
		t.Errorf("Poll() changes = %+v, want %+v", changes, want)
	}
}
