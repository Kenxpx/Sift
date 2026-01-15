package cli

import (
	"errors"
	"fmt"
	"strings"

	"sift/internal/app"
	"sift/internal/config"
	"sift/internal/core"
	"sift/internal/query"
	"sift/internal/report"
	"sift/internal/server"
	"sift/internal/store"
	"sift/internal/token"
	"sift/internal/validate"
	"sift/internal/workspace"
)

// index builds and publishes the index of a corpus.
func (r *runner) index(args []string) error {
	fs := newFlags("index")
	root := fs.String("root", ".", "corpus root directory")
	if err := parse("index", fs, args); err != nil {
		return err
	}
	if err := noArgs("index", fs); err != nil {
		return err
	}
	m, err := r.app.Index(*root)
	if err != nil {
		return commandError("index", err)
	}
	fmt.Fprint(r.out, formatManifest(m))
	return nil
}

// search answers one query against a published index.
func (r *runner) search(args []string) error {
	fs := newFlags("search")
	root := fs.String("root", ".", "corpus root directory")
	limit := fs.Int("limit", 0, "maximum number of results, 0 for every result")
	format := fs.String("format", "text", "output format: text, md, csv or json")
	var filters, facets stringList
	fs.Var(&filters, "filter", "keep only documents whose field:value matches")
	fs.Var(&facets, "facet", "count the values of a field over every match")
	if err := parse("search", fs, args); err != nil {
		return err
	}
	// The format is checked before the index is opened, so a mistyped format
	// costs nothing and is always reported the same way.
	if !knownFormat(*format) {
		return &usageError{command: "search", reason: "unknown format: " + *format}
	}
	selected, err := parseFilters(filters)
	if err != nil {
		return err
	}
	opts := core.SearchOptions{
		Query:   strings.Join(fs.Args(), " "),
		Filters: selected,
		Limit:   *limit,
		Facets:  parseFacets(facets),
	}
	// The query is an argument like any other, so it is checked before the
	// index is opened. Checking it afterwards would report a corpus that was
	// never indexed instead of the typing mistake the user can act on.
	if err := checkQuery(opts.Query); err != nil {
		return err
	}
	rep, err := r.app.Search(*root, opts)
	if err != nil {
		return commandError("search", err)
	}
	text, err := render(*format, rep)
	if err != nil {
		return commandError("search", err)
	}
	fmt.Fprint(r.out, text)
	return nil
}

// stats summarizes a published index.
func (r *runner) stats(args []string) error {
	fs := newFlags("stats")
	root := fs.String("root", ".", "corpus root directory")
	if err := parse("stats", fs, args); err != nil {
		return err
	}
	if err := noArgs("stats", fs); err != nil {
		return err
	}
	corpus, err := r.app.Stats(*root)
	if err != nil {
		return commandError("stats", err)
	}
	fmt.Fprint(r.out, report.Stats(corpus))
	return nil
}

// validate checks a published index and fails when anything is wrong with it.
func (r *runner) validate(args []string) error {
	fs := newFlags("validate")
	root := fs.String("root", ".", "corpus root directory")
	if err := parse("validate", fs, args); err != nil {
		return err
	}
	if err := noArgs("validate", fs); err != nil {
		return err
	}
	f, err := r.app.Validate(*root)
	if err != nil {
		return commandError("validate", err)
	}
	fmt.Fprint(r.out, formatFindings(f))
	if n := len(f.Problems); n > 0 {
		// The findings are on standard output already; the exit code and this
		// one line are what a script acts on.
		return commandError("validate", fmt.Errorf("index has %d %s: %w", n, plural(n, "problem"), core.ErrIntegrity))
	}
	return nil
}

// config prints the configuration in force for a corpus.
func (r *runner) config(args []string) error {
	fs := newFlags("config")
	root := fs.String("root", ".", "corpus root directory")
	if err := parse("config", fs, args); err != nil {
		return err
	}
	if err := noArgs("config", fs); err != nil {
		return err
	}
	cfg, err := r.app.Config(*root)
	if err != nil {
		return commandError("config", err)
	}
	fmt.Fprint(r.out, formatConfig(cfg))
	return nil
}

// workspace maintains the registry of corpora.
func (r *runner) workspace(args []string) error {
	if len(args) == 0 {
		return &usageError{command: "workspace", reason: "no subcommand given, want list, add or remove"}
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "list":
		return r.workspaceList(rest)
	case "add":
		return r.workspaceAdd(rest)
	case "remove":
		return r.workspaceRemove(rest)
	default:
		return &usageError{command: "workspace", reason: "unknown subcommand: " + sub}
	}
}

// workspaceList prints every registered corpus, marking the active one.
func (r *runner) workspaceList(args []string) error {
	const name = "workspace list"
	fs := newFlags(name)
	root := fs.String("root", ".", "workspace root directory")
	if err := parse(name, fs, args); err != nil {
		return err
	}
	if err := noArgs(name, fs); err != nil {
		return err
	}
	w, err := loadWorkspace(*root)
	if err != nil {
		return commandError(name, err)
	}
	corpora := w.List()
	if len(corpora) == 0 {
		fmt.Fprintln(r.out, "no corpora registered")
		return nil
	}
	for _, c := range corpora {
		marker := "  "
		if c.Name == w.Active {
			marker = "* "
		}
		fmt.Fprintf(r.out, "%s%s\troot=%s\toutput=%s\n", marker, c.Name, c.Root, workspace.InferOutputPath(c))
	}
	return nil
}

// workspaceAdd registers a corpus under a name.
func (r *runner) workspaceAdd(args []string) error {
	const name = "workspace add"
	fs := newFlags(name)
	root := fs.String("root", ".", "workspace root directory")
	if err := parse(name, fs, args); err != nil {
		return err
	}
	if fs.NArg() < 2 || fs.NArg() > 3 {
		return &usageError{command: name, reason: "want a name, a corpus root and an optional output directory"}
	}
	w, err := loadWorkspace(*root)
	if err != nil {
		return commandError(name, err)
	}
	if err := w.Add(workspace.Corpus{Name: fs.Arg(0), Root: fs.Arg(1), OutputDir: fs.Arg(2)}); err != nil {
		return commandError(name, err)
	}
	if err := workspace.Save(*root, w); err != nil {
		return commandError(name, err)
	}
	fmt.Fprintf(r.out, "registered %s\n", fs.Arg(0))
	return nil
}

// workspaceRemove deletes a corpus from the registry.
func (r *runner) workspaceRemove(args []string) error {
	const name = "workspace remove"
	fs := newFlags(name)
	root := fs.String("root", ".", "workspace root directory")
	if err := parse(name, fs, args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return &usageError{command: name, reason: "want exactly one corpus name"}
	}
	w, err := loadWorkspace(*root)
	if err != nil {
		return commandError(name, err)
	}
	if !w.Remove(fs.Arg(0)) {
		return commandError(name, fmt.Errorf("not registered: %s: %w", fs.Arg(0), core.ErrNotFound))
	}
	if err := workspace.Save(*root, w); err != nil {
		return commandError(name, err)
	}
	fmt.Fprintf(r.out, "removed %s\n", fs.Arg(0))
	return nil
}

// serve exposes a published index over HTTP until the process is stopped.
func (r *runner) serve(args []string) error {
	fs := newFlags("serve")
	root := fs.String("root", ".", "corpus root directory")
	addr := fs.String("addr", "127.0.0.1:8080", "listen address")
	if err := parse("serve", fs, args); err != nil {
		return err
	}
	if err := noArgs("serve", fs); err != nil {
		return err
	}
	// Resolving the configuration first turns an unusable corpus into a single
	// error before a port is bound, instead of into failing requests.
	if _, err := r.app.Config(*root); err != nil {
		return commandError("serve", err)
	}
	ln, err := r.listen("tcp", *addr)
	if err != nil {
		return commandError("serve", err)
	}
	defer ln.Close()
	fmt.Fprintf(r.out, "listening on %s\n", ln.Addr())
	if err := r.serveHTTP(ln, server.Handler(corpusSearcher{app: r.app, root: *root})); err != nil {
		return commandError("serve", err)
	}
	return nil
}

// watch reports the corpus files that changed since the recorded scan.
func (r *runner) watch(args []string) error {
	fs := newFlags("watch")
	root := fs.String("root", ".", "corpus root directory")
	state := fs.String("state", "", "file holding the previous scan")
	if err := parse("watch", fs, args); err != nil {
		return err
	}
	if err := noArgs("watch", fs); err != nil {
		return err
	}
	prev, err := readScan(*state)
	if err != nil {
		return commandError("watch", err)
	}
	now, changes, err := r.app.Watch(*root, prev)
	if err != nil {
		return commandError("watch", err)
	}
	fmt.Fprintf(r.out, "files: %d\n", len(now))
	fmt.Fprintf(r.out, "changes: %d\n", len(changes))
	for _, c := range changes {
		fmt.Fprintf(r.out, "  %-8s  %s\n", c.Kind, c.Rel)
	}
	if *state != "" {
		if err := store.WriteJSONAtomic(*state, now); err != nil {
			return commandError("watch", err)
		}
	}
	return nil
}

// corpusSearcher answers a request that names no corpus with the corpus the
// command line named, so serving a single corpus needs no root parameter.
type corpusSearcher struct {
	app  app.App
	root string
}

// Search implements server.AppSearcher.
func (s corpusSearcher) Search(root string, opts core.SearchOptions) (core.SearchReport, error) {
	if strings.TrimSpace(root) == "" {
		root = s.root
	}
	return s.app.Search(root, opts)
}

// loadWorkspace reads the registry under root. A registry that does not exist
// yet is an empty workspace rather than an error, so the first add works.
func loadWorkspace(root string) (*workspace.Workspace, error) {
	w, err := workspace.Load(root)
	if err != nil && !errors.Is(err, core.ErrNotFound) {
		return nil, err
	}
	return w, nil
}

// readScan reads the baseline scan of the watch command. No state file and a
// state file that does not exist yet both mean no baseline, so the first watch
// of a corpus reports every file as added instead of failing.
func readScan(path string) ([]core.FileRef, error) {
	if path == "" {
		return nil, nil
	}
	var refs []core.FileRef
	if err := store.ReadJSON(path, &refs); err != nil {
		if errors.Is(err, core.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return refs, nil
}

// checkQuery rejects a query the parser cannot read, as a usage error naming
// the offset the parser stopped at.
func checkQuery(text string) error {
	_, err := query.Parse(text)
	if err == nil {
		return nil
	}
	var bad *core.QueryError
	if errors.As(err, &bad) {
		return &usageError{command: "search", reason: fmt.Sprintf("bad query at %d: %s", bad.Position, bad.Reason)}
	}
	return &usageError{command: "search", reason: err.Error()}
}

// knownFormat reports whether name is a search output format.
func knownFormat(name string) bool {
	switch name {
	case "text", "md", "csv", "json":
		return true
	}
	return false
}

// render formats a search report. The format must already be known.
func render(format string, rep core.SearchReport) (string, error) {
	switch format {
	case "md":
		return report.Markdown(rep), nil
	case "csv":
		return report.CSV(rep), nil
	case "json":
		return report.JSON(rep)
	default:
		return report.Text(rep), nil
	}
}

// parseFilters turns repeated "field:value" flags into the filter map. A value
// may be empty, which selects the documents that lack the field.
func parseFilters(values []string) (map[string]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(values))
	for _, v := range values {
		field, value, ok := strings.Cut(v, ":")
		field = strings.TrimSpace(field)
		if !ok || field == "" {
			return nil, &usageError{command: "search", reason: "filter must be field:value: " + v}
		}
		out[field] = value
	}
	return out, nil
}

// parseFacets splits repeated, comma separated facet flags into field names.
func parseFacets(values []string) []string {
	var out []string
	for _, v := range values {
		for _, field := range strings.Split(v, ",") {
			if field = strings.TrimSpace(field); field != "" {
				out = append(out, field)
			}
		}
	}
	return out
}

// formatManifest renders what a publish committed. The publication time is
// deliberately absent: it changes between runs, and printing it would make the
// output of two identical indexing runs differ.
func formatManifest(m core.Manifest) string {
	var b strings.Builder
	fmt.Fprintf(&b, "generation: %d\n", m.Generation)
	fmt.Fprintf(&b, "segments: %d\n", len(m.Segments))
	fmt.Fprintf(&b, "documents: %d\n", m.DocCount)
	fmt.Fprintf(&b, "terms: %d\n", m.TermCount)
	fmt.Fprintf(&b, "config: %s\n", m.ConfigHash)
	return b.String()
}

// formatFindings renders a validation report.
func formatFindings(f validate.Findings) string {
	var b strings.Builder
	fmt.Fprintf(&b, "generation: %d\n", f.Generation)
	fmt.Fprintf(&b, "segments: %d\n", f.Segments)
	fmt.Fprintf(&b, "documents: %d\n", f.Documents)
	fmt.Fprintf(&b, "terms: %d\n", f.Terms)
	if len(f.Problems) == 0 {
		b.WriteString("problems: none\n")
		return b.String()
	}
	b.WriteString("problems:\n")
	for _, p := range f.Problems {
		fmt.Fprintf(&b, "  %s\n", p)
	}
	return b.String()
}

// formatConfig renders the configuration in force for a corpus.
func formatConfig(cfg core.Config) string {
	var b strings.Builder
	fmt.Fprintf(&b, "root: %s\n", cfg.Root)
	fmt.Fprintf(&b, "output: %s\n", config.OutputPath(cfg))
	fmt.Fprintf(&b, "include: %s\n", patternList(cfg.Include))
	fmt.Fprintf(&b, "exclude: %s\n", patternList(cfg.Exclude))
	fmt.Fprintf(&b, "max_file_bytes: %d\n", cfg.MaxFileBytes)
	fmt.Fprintf(&b, "min_term_length: %d\n", cfg.MinTermLength)
	fmt.Fprintf(&b, "segment_docs: %d\n", cfg.SegmentDocs)
	fmt.Fprintf(&b, "stopwords: %s\n", stopwordSummary(cfg))
	fmt.Fprintf(&b, "hash: %s\n", config.Hash(cfg))
	return b.String()
}

// plural gives noun its English plural when there is not exactly one of it.
func plural(n int, noun string) string {
	if n == 1 {
		return noun
	}
	return noun + "s"
}

// patternList renders a glob list, naming the empty case rather than printing
// a blank value.
func patternList(patterns []string) string {
	if len(patterns) == 0 {
		return "(none)"
	}
	return strings.Join(patterns, ", ")
}

// stopwordSummary reports how many stopwords apply and where they come from.
func stopwordSummary(cfg core.Config) string {
	if len(cfg.Stopwords) == 0 {
		return fmt.Sprintf("%d (default list)", len(token.DefaultStopwords))
	}
	return fmt.Sprintf("%d (configured)", len(cfg.Stopwords))
}
