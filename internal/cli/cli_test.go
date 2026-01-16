package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"sift/internal/app"
	"sift/internal/core"
)

// corpus writes a small corpus under a fresh directory and returns its path.
func corpus(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"docs/alpha.md": "# Alpha Guide\n\nalpha beta gamma alpha\n",
		"docs/beta.md":  "# Beta Notes\n\nbeta gamma\n",
		"src/main.go":   "package main\n\nfunc main() { println(\"alpha\") }\n",
		"readme.txt":    "plain text alpha\n",
	}
	for rel, body := range files {
		write(t, filepath.Join(root, filepath.FromSlash(rel)), body)
	}
	return root
}

// write creates one file and every directory above it.
func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// run executes one command line and returns its code and both streams.
func run(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errs bytes.Buffer
	code := Run(args, &out, &errs)
	return code, out.String(), errs.String()
}

// mustRun executes one command line that has to succeed.
func mustRun(t *testing.T, args ...string) string {
	t.Helper()
	code, out, errs := run(t, args...)
	if code != ExitOK {
		t.Fatalf("%v: exit %d, stderr %s", args, code, errs)
	}
	return out
}

func TestRunVersion(t *testing.T) {
	code, out, errs := run(t, "version")
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d", code, ExitOK)
	}
	if want := "sift " + Version + "\n"; out != want {
		t.Errorf("stdout = %q, want %q", out, want)
	}
	if errs != "" {
		t.Errorf("stderr = %q, want empty", errs)
	}
}

func TestRunHelp(t *testing.T) {
	t.Run("overview lists every command", func(t *testing.T) {
		out := mustRun(t, "help")
		for _, c := range commands {
			if !strings.Contains(out, "  "+c.name) {
				t.Errorf("overview does not list %q:\n%s", c.name, out)
			}
		}
	})

	t.Run("one command", func(t *testing.T) {
		out := mustRun(t, "help", "search")
		if !strings.HasPrefix(out, "usage: sift search") {
			t.Errorf("stdout = %q, want the search usage", out)
		}
	})

	t.Run("-h is not an error", func(t *testing.T) {
		code, out, errs := run(t, "index", "-h")
		if code != ExitOK {
			t.Fatalf("exit = %d, want %d (stderr %s)", code, ExitOK, errs)
		}
		if !strings.HasPrefix(out, "usage: sift index") {
			t.Errorf("stdout = %q, want the index usage", out)
		}
	})

	t.Run("unknown command", func(t *testing.T) {
		code, _, errs := run(t, "help", "frobnicate")
		if code != ExitUsage {
			t.Errorf("exit = %d, want %d", code, ExitUsage)
		}
		if !strings.Contains(errs, "unknown command: frobnicate") {
			t.Errorf("stderr = %q, want the unknown command named", errs)
		}
	})
}

func TestRunUsageErrors(t *testing.T) {
	root := corpus(t)
	cases := []struct {
		name    string
		args    []string
		message string
	}{
		{name: "no command", args: nil, message: "sift: no command given"},
		{name: "unknown command", args: []string{"frobnicate"}, message: "sift frobnicate: unknown command"},
		{name: "unknown flag", args: []string{"index", "-nope"}, message: "sift index: flag provided but not defined"},
		{name: "unexpected argument", args: []string{"index", "extra"}, message: "sift index: unexpected argument: extra"},
		{name: "unknown format", args: []string{"search", "-root", root, "-format", "xml", "alpha"}, message: "sift search: unknown format: xml"},
		{name: "bad filter", args: []string{"search", "-root", root, "-filter", "kind", "alpha"}, message: "sift search: filter must be field:value: kind"},
		{name: "bad query", args: []string{"search", "-root", root, "\"open"}, message: "sift search: bad query at 0: unterminated quote"},
		{name: "no subcommand", args: []string{"workspace"}, message: "sift workspace: no subcommand given"},
		{name: "unknown subcommand", args: []string{"workspace", "frob"}, message: "sift workspace: unknown subcommand: frob"},
		{name: "workspace add arity", args: []string{"workspace", "add", "only-a-name"}, message: "sift workspace add: want a name"},
		{name: "workspace remove arity", args: []string{"workspace", "remove"}, message: "sift workspace remove: want exactly one corpus name"},
		{name: "version argument", args: []string{"version", "extra"}, message: "sift version: unexpected argument: extra"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, out, errs := run(t, tc.args...)
			if code != ExitUsage {
				t.Fatalf("exit = %d, want %d (stderr %s)", code, ExitUsage, errs)
			}
			if !strings.Contains(errs, tc.message) {
				t.Errorf("stderr = %q, want it to contain %q", errs, tc.message)
			}
			if out != "" {
				t.Errorf("stdout = %q, want empty: diagnostics belong on stderr", out)
			}
			if !strings.Contains(errs, "usage: sift") {
				t.Errorf("stderr = %q, want a usage block", errs)
			}
		})
	}
}

func TestRunRuntimeErrors(t *testing.T) {
	root := corpus(t)
	cases := []struct {
		name    string
		args    []string
		message string
	}{
		{name: "search before index", args: []string{"search", "-root", root, "alpha"}, message: "sift search:"},
		{name: "stats before index", args: []string{"stats", "-root", root}, message: "sift stats:"},
		{name: "validate before index", args: []string{"validate", "-root", root}, message: "sift validate:"},
		{name: "index missing corpus", args: []string{"index", "-root", filepath.Join(root, "nowhere")}, message: "sift index:"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, _, errs := run(t, tc.args...)
			if code != ExitError {
				t.Fatalf("exit = %d, want %d (stderr %s)", code, ExitError, errs)
			}
			if !strings.HasPrefix(errs, tc.message) {
				t.Errorf("stderr = %q, want it to start with %q", errs, tc.message)
			}
			if strings.Contains(errs, "usage: sift") {
				t.Errorf("stderr = %q, want no usage block for a runtime failure", errs)
			}
		})
	}
}

func TestRunIndexReportsTheGeneration(t *testing.T) {
	root := corpus(t)

	first := mustRun(t, "index", "-root", root)
	if !strings.HasPrefix(first, "generation: 1\n") {
		t.Errorf("stdout = %q, want generation 1 first", first)
	}
	for _, want := range []string{"segments: 1\n", "documents: 4\n"} {
		if !strings.Contains(first, want) {
			t.Errorf("stdout = %q, want it to contain %q", first, want)
		}
	}
	if strings.Contains(first, "202") {
		t.Errorf("stdout = %q, want no timestamp: the output must not vary between runs", first)
	}

	second := mustRun(t, "index", "-root", root)
	if !strings.HasPrefix(second, "generation: 2\n") {
		t.Errorf("stdout = %q, want generation 2 on the second run", second)
	}
	if strings.TrimPrefix(first, "generation: 1\n") != strings.TrimPrefix(second, "generation: 2\n") {
		t.Errorf("republishing an unchanged corpus changed the report:\n%s\n%s", first, second)
	}
}

func TestRunSearchFormats(t *testing.T) {
	root := corpus(t)
	mustRun(t, "index", "-root", root)

	cases := []struct {
		format string
		prefix string
	}{
		{format: "text", prefix: "query: alpha\n"},
		{format: "md", prefix: "# Search report\n"},
		{format: "csv", prefix: "rank,doc_id,path,title,score,freq,fields,snippet\n"},
		{format: "json", prefix: "{\n"},
	}
	for _, tc := range cases {
		t.Run(tc.format, func(t *testing.T) {
			out := mustRun(t, "search", "-root", root, "-format", tc.format, "alpha")
			if !strings.HasPrefix(out, tc.prefix) {
				t.Errorf("stdout = %q, want it to start with %q", out, tc.prefix)
			}
			if !strings.Contains(out, "docs/alpha.md") {
				t.Errorf("stdout = %q, want the best match named", out)
			}
			if !strings.HasSuffix(out, "\n") {
				t.Errorf("stdout = %q, want a trailing newline", out)
			}
		})
	}

	t.Run("json carries the totals", func(t *testing.T) {
		out := mustRun(t, "search", "-root", root, "-format", "json", "-limit", "1", "alpha")
		var body struct {
			Total   int `json:"total"`
			Shown   int `json:"shown"`
			Results []struct {
				DocID string `json:"doc_id"`
			} `json:"results"`
		}
		if err := json.Unmarshal([]byte(out), &body); err != nil {
			t.Fatalf("decode: %v\n%s", err, out)
		}
		if body.Total != 3 || body.Shown != 1 {
			t.Errorf("total/shown = %d/%d, want 3/1", body.Total, body.Shown)
		}
		if len(body.Results) != 1 || body.Results[0].DocID != "docs/alpha.md" {
			t.Errorf("results = %+v, want only docs/alpha.md", body.Results)
		}
	})
}

func TestRunSearchFiltersAndFacets(t *testing.T) {
	root := corpus(t)
	mustRun(t, "index", "-root", root)

	t.Run("filter narrows", func(t *testing.T) {
		out := mustRun(t, "search", "-root", root, "-filter", "kind:markdown", "alpha")
		if !strings.Contains(out, "total: 1\n") {
			t.Errorf("stdout = %q, want one match", out)
		}
		if strings.Contains(out, "readme.txt") {
			t.Errorf("stdout = %q, want the text file filtered out", out)
		}
	})

	t.Run("facets count every match", func(t *testing.T) {
		out := mustRun(t, "search", "-root", root, "-limit", "1", "-facet", "kind,language", "alpha")
		if !strings.Contains(out, "total: 3\n") || !strings.Contains(out, "shown: 1\n") {
			t.Errorf("stdout = %q, want 3 matches and 1 shown", out)
		}
		for _, want := range []string{"  kind:\n", "    markdown: 1\n", "    source: 1\n", "    text: 1\n", "  language:\n"} {
			if !strings.Contains(out, want) {
				t.Errorf("stdout = %q, want it to contain %q", out, want)
			}
		}
	})

	t.Run("empty query matches everything", func(t *testing.T) {
		out := mustRun(t, "search", "-root", root)
		if !strings.Contains(out, "total: 4\n") {
			t.Errorf("stdout = %q, want every document", out)
		}
		if !strings.Contains(out, "query: (all documents)\n") {
			t.Errorf("stdout = %q, want the empty query labelled", out)
		}
	})
}

func TestRunValidateAndStats(t *testing.T) {
	root := corpus(t)
	mustRun(t, "index", "-root", root)

	t.Run("clean index", func(t *testing.T) {
		out := mustRun(t, "validate", "-root", root)
		if !strings.Contains(out, "problems: none\n") {
			t.Errorf("stdout = %q, want no problems", out)
		}
	})

	t.Run("stats", func(t *testing.T) {
		out := mustRun(t, "stats", "-root", root)
		for _, want := range []string{"documents: 4\n", "by kind:\n", "  markdown: 2\n", "largest documents:\n", "  1. docs/alpha.md: "} {
			if !strings.Contains(out, want) {
				t.Errorf("stdout = %q, want it to contain %q", out, want)
			}
		}
	})

	t.Run("damaged index fails", func(t *testing.T) {
		seg := filepath.Join(root, core.DefaultOutputDir, "gen-0001", "seg-0001.json")
		data, err := os.ReadFile(seg)
		if err != nil {
			t.Fatalf("read segment: %v", err)
		}
		if err := os.WriteFile(seg, append(data, ' '), 0o644); err != nil {
			t.Fatalf("damage segment: %v", err)
		}
		code, out, errs := run(t, "validate", "-root", root)
		if code != ExitError {
			t.Fatalf("exit = %d, want %d", code, ExitError)
		}
		if !strings.Contains(out, "gen-0001/seg-0001.json: digest mismatch\n") {
			t.Errorf("stdout = %q, want the damaged segment named", out)
		}
		if !strings.Contains(errs, "index has 1 problem:") {
			t.Errorf("stderr = %q, want the problem count", errs)
		}
	})
}

func TestRunConfig(t *testing.T) {
	root := corpus(t)
	write(t, filepath.Join(root, ".sift.json"), "{\"include\": [\"*.md\"], \"segment_docs\": 8}\n")

	out := mustRun(t, "config", "-root", root)
	for _, want := range []string{
		"root: " + root + "\n",
		"output: " + filepath.Join(root, core.DefaultOutputDir) + "\n",
		"include: *.md\n",
		"exclude: (none)\n",
		"segment_docs: 8\n",
		"min_term_length: 2\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout = %q, want it to contain %q", out, want)
		}
	}
	if !strings.Contains(out, "hash: ") {
		t.Errorf("stdout = %q, want the configuration hash", out)
	}
}

func TestRunWatchTracksAStateFile(t *testing.T) {
	root := corpus(t)
	state := filepath.Join(t.TempDir(), "scan.json")

	first := mustRun(t, "watch", "-root", root, "-state", state)
	if !strings.Contains(first, "files: 4\n") || !strings.Contains(first, "changes: 4\n") {
		t.Errorf("stdout = %q, want 4 files reported as added", first)
	}
	if _, err := os.Stat(state); err != nil {
		t.Fatalf("state file not written: %v", err)
	}

	quiet := mustRun(t, "watch", "-root", root, "-state", state)
	if !strings.Contains(quiet, "changes: 0\n") {
		t.Errorf("stdout = %q, want no changes", quiet)
	}

	write(t, filepath.Join(root, "docs", "gamma.md"), "# Gamma\n\ngamma\n")
	if err := os.Remove(filepath.Join(root, "readme.txt")); err != nil {
		t.Fatalf("remove: %v", err)
	}
	changed := mustRun(t, "watch", "-root", root, "-state", state)
	if !strings.Contains(changed, "changes: 2\n") {
		t.Errorf("stdout = %q, want two changes", changed)
	}
	for _, want := range []string{"  added     docs/gamma.md\n", "  removed   readme.txt\n"} {
		if !strings.Contains(changed, want) {
			t.Errorf("stdout = %q, want it to contain %q", changed, want)
		}
	}
}

func TestRunWorkspaceLifecycle(t *testing.T) {
	ws := t.TempDir()
	first := corpus(t)
	second := corpus(t)

	if out := mustRun(t, "workspace", "list", "-root", ws); out != "no corpora registered\n" {
		t.Errorf("stdout = %q, want an empty registry", out)
	}
	if out := mustRun(t, "workspace", "add", "-root", ws, "docs", first); out != "registered docs\n" {
		t.Errorf("stdout = %q, want the corpus registered", out)
	}
	if out := mustRun(t, "workspace", "add", "-root", ws, "code", second, "out"); out != "registered code\n" {
		t.Errorf("stdout = %q, want the corpus registered", out)
	}

	list := mustRun(t, "workspace", "list", "-root", ws)
	lines := strings.Split(strings.TrimSuffix(list, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("list = %q, want two lines", list)
	}
	if !strings.HasPrefix(lines[0], "  code\t") {
		t.Errorf("first line = %q, want the corpora sorted by name", lines[0])
	}
	if !strings.HasPrefix(lines[1], "* docs\t") {
		t.Errorf("second line = %q, want the first corpus registered to be active", lines[1])
	}
	if !strings.Contains(lines[1], "output="+filepath.Join(first, core.DefaultOutputDir)) {
		t.Errorf("line = %q, want the inferred output directory", lines[1])
	}

	code, _, errs := run(t, "workspace", "add", "-root", ws, "docs", first)
	if code != ExitError {
		t.Errorf("exit = %d, want %d for a duplicate name", code, ExitError)
	}
	if !strings.Contains(errs, "already registered: docs") {
		t.Errorf("stderr = %q, want the duplicate named", errs)
	}

	if out := mustRun(t, "workspace", "remove", "-root", ws, "docs"); out != "removed docs\n" {
		t.Errorf("stdout = %q, want the corpus removed", out)
	}
	if out := mustRun(t, "workspace", "list", "-root", ws); !strings.HasPrefix(out, "* code\t") {
		t.Errorf("stdout = %q, want code to become active", out)
	}
	code, _, errs = run(t, "workspace", "remove", "-root", ws, "docs")
	if code != ExitError {
		t.Errorf("exit = %d, want %d for an unknown name", code, ExitError)
	}
	if !strings.Contains(errs, "not registered: docs") {
		t.Errorf("stderr = %q, want the unknown name reported", errs)
	}
}

func TestServeAnswersThroughTheHandler(t *testing.T) {
	root := corpus(t)
	mustRun(t, "index", "-root", root)

	var out, errs bytes.Buffer
	var served http.Handler
	r := &runner{
		app: app.New(),
		out: &out,
		err: &errs,
		listen: func(network, address string) (net.Listener, error) {
			if network != "tcp" {
				t.Errorf("network = %q, want tcp", network)
			}
			if address != "127.0.0.1:0" {
				t.Errorf("address = %q, want the address from the flag", address)
			}
			return stubListener{}, nil
		},
		serveHTTP: func(_ net.Listener, h http.Handler) error {
			served = h
			return nil
		},
	}
	if code := r.run([]string{"serve", "-root", root, "-addr", "127.0.0.1:0"}); code != ExitOK {
		t.Fatalf("exit = %d, want %d (stderr %s)", code, ExitOK, errs.String())
	}
	if want := "listening on 127.0.0.1:9999\n"; out.String() != want {
		t.Errorf("stdout = %q, want %q", out.String(), want)
	}
	if served == nil {
		t.Fatal("no handler was served")
	}

	t.Run("healthz", func(t *testing.T) {
		w := httptest.NewRecorder()
		served.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/healthz", nil))
		if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != "ok" {
			t.Errorf("healthz = %d %q, want 200 ok", w.Code, w.Body.String())
		}
	})

	t.Run("search falls back to the served corpus", func(t *testing.T) {
		w := httptest.NewRecorder()
		served.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/search?q=alpha&facet=kind", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("search = %d, body %s", w.Code, w.Body.String())
		}
		var report core.SearchReport
		if err := json.Unmarshal(w.Body.Bytes(), &report); err != nil {
			t.Fatalf("decode: %v\n%s", err, w.Body.String())
		}
		if report.Total != 3 {
			t.Errorf("Total = %d, want 3", report.Total)
		}
		if report.Results[0].DocID != "docs/alpha.md" {
			t.Errorf("first result = %q, want docs/alpha.md", report.Results[0].DocID)
		}
		if len(report.Facets["kind"].Counts) != 3 {
			t.Errorf("kind facet = %v, want three values", report.Facets["kind"].Counts)
		}
	})
}

func TestServeReportsAFailedListen(t *testing.T) {
	root := corpus(t)
	var out, errs bytes.Buffer
	r := &runner{
		app:    app.New(),
		out:    &out,
		err:    &errs,
		listen: func(string, string) (net.Listener, error) { return nil, errors.New("address in use") },
		serveHTTP: func(net.Listener, http.Handler) error {
			t.Fatal("served despite a failed listen")
			return nil
		},
	}
	if code := r.run([]string{"serve", "-root", root, "-addr", "127.0.0.1:1"}); code != ExitError {
		t.Fatalf("exit = %d, want %d", code, ExitError)
	}
	if !strings.Contains(errs.String(), "sift serve: address in use") {
		t.Errorf("stderr = %q, want the listen failure named", errs.String())
	}
	if out.String() != "" {
		t.Errorf("stdout = %q, want nothing announced", out.String())
	}
}

func TestParseFilters(t *testing.T) {
	cases := []struct {
		name  string
		in    []string
		want  map[string]string
		usage bool
	}{
		{name: "none", in: nil, want: nil},
		{name: "one", in: []string{"kind:markdown"}, want: map[string]string{"kind": "markdown"}},
		{name: "repeated", in: []string{"kind:markdown", "dir:docs"}, want: map[string]string{"kind": "markdown", "dir": "docs"}},
		{name: "empty value", in: []string{"language:"}, want: map[string]string{"language": ""}},
		{name: "value keeps colons", in: []string{"path:a:b"}, want: map[string]string{"path": "a:b"}},
		{name: "no colon", in: []string{"kind"}, usage: true},
		{name: "no field", in: []string{":markdown"}, usage: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseFilters(tc.in)
			if tc.usage {
				if !errors.Is(err, core.ErrUsage) {
					t.Fatalf("error = %v, want core.ErrUsage", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseFilters: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("parseFilters(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseFacets(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{name: "none", in: nil, want: nil},
		{name: "one", in: []string{"kind"}, want: []string{"kind"}},
		{name: "comma separated", in: []string{"kind,language"}, want: []string{"kind", "language"}},
		{name: "repeated and trimmed", in: []string{" kind ", "dir,"}, want: []string{"kind", "dir"}},
		{name: "empty entries dropped", in: []string{",", " "}, want: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseFacets(tc.in); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("parseFacets(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestRunAcceptsNilWriters(t *testing.T) {
	root := corpus(t)
	if code := Run([]string{"index", "-root", root}, nil, nil); code != ExitOK {
		t.Fatalf("exit = %d, want %d", code, ExitOK)
	}
	if code := Run([]string{"nonsense"}, nil, nil); code != ExitUsage {
		t.Fatalf("exit = %d, want %d", code, ExitUsage)
	}
}

// stubListener stands in for a bound socket, so the serve command can be
// driven without touching the network.
type stubListener struct{}

// Accept implements net.Listener.
func (stubListener) Accept() (net.Conn, error) { return nil, errors.New("stub listener") }

// Close implements net.Listener.
func (stubListener) Close() error { return nil }

// Addr implements net.Listener.
func (stubListener) Addr() net.Addr { return stubAddr{} }

// stubAddr is the address stubListener reports.
type stubAddr struct{}

// Network implements net.Addr.
func (stubAddr) Network() string { return "tcp" }

// String implements net.Addr.
func (stubAddr) String() string { return "127.0.0.1:9999" }
