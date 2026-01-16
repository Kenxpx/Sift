package cli

import (
	"fmt"
	"strings"
)

// command describes one command for the help text.
type command struct {
	// name is the word typed after "sift".
	name string
	// summary is the one-line description listed by help.
	summary string
	// usage is the full help block, ending in a newline.
	usage string
}

// commands lists every command in the order help prints them. It is read-only
// reference data: nothing mutates it.
var commands = []command{
	{
		name:    "index",
		summary: "build and publish the index of a corpus",
		usage: `usage: sift index [-root dir]

Scans the corpus, extracts and tokenizes every file that changed since the
last run, and publishes the result as a new generation under the output
directory. Reports the generation, the counts and the configuration hash.

  -root dir   corpus root directory (default ".")
`,
	},
	{
		name:    "search",
		summary: "query a published index",
		usage: `usage: sift search [-root dir] [-limit n] [-filter field:value]
                      [-facet field] [-format text|md|csv|json] query...

Answers a query against the published index. Terms combine with AND, a
"quoted phrase" matches adjacent words, field:value selects on a document
field and -term excludes. An empty query matches every document.

  -root dir            corpus root directory (default ".")
  -limit n             return at most n results; 0 returns every result
  -filter field:value  keep only documents whose field equals value (repeatable)
  -facet field         count the values of a field over every match
                       (repeatable, comma separated)
  -format f            text, md, csv or json (default "text")
`,
	},
	{
		name:    "stats",
		summary: "summarize a published index",
		usage: `usage: sift stats [-root dir]

Reports the document, term and token counts of the published index, the
documents per kind and per language, and the longest documents.

  -root dir   corpus root directory (default ".")
`,
	},
	{
		name:    "validate",
		summary: "check a published index against its manifest",
		usage: `usage: sift validate [-root dir]

Checks that every segment and the extraction cache the manifest names is
present and matches its recorded digest, and that the manifest counts agree
with the segments. Lists every problem found, including a configuration that
changed since publication, and exits 1 when there is at least one.

  -root dir   corpus root directory (default ".")
`,
	},
	{
		name:    "config",
		summary: "print the resolved configuration of a corpus",
		usage: `usage: sift config [-root dir]

Prints the configuration in force for the corpus: the defaults with
.sift.json applied over them, the output directory and the configuration
hash recorded in a published manifest.

  -root dir   corpus root directory (default ".")
`,
	},
	{
		name:    "workspace",
		summary: "register the corpora of a workspace",
		usage: `usage: sift workspace list [-root dir]
       sift workspace add [-root dir] name corpus-root [output-dir]
       sift workspace remove [-root dir] name

Maintains the workspace registry in <root>/.sift-workspace.json. The
active corpus is marked with "*" and is the first corpus registered until
another is removed in its place.

  -root dir   workspace root directory holding the registry (default ".")
`,
	},
	{
		name:    "serve",
		summary: "serve the published index over HTTP",
		usage: `usage: sift serve [-root dir] [-addr host:port]

Serves GET /search and GET /healthz until the process is stopped. A request
that names no root searches the corpus given here.

  -root dir        corpus root directory (default ".")
  -addr host:port  listen address (default "127.0.0.1:8080")
`,
	},
	{
		name:    "watch",
		summary: "report the corpus files that changed",
		usage: `usage: sift watch [-root dir] [-state file]

Scans the corpus once and reports the files added, modified and removed
since the scan recorded in the state file. Without a state file there is no
baseline, so every file is reported as added; with one, the scan is written
back so the next run compares against it.

  -root dir     corpus root directory (default ".")
  -state file   file holding the previous scan (default: no baseline)
`,
	},
	{
		name:    "version",
		summary: "print the version",
		usage: `usage: sift version

Prints the version of the command line.
`,
	},
	{
		name:    "help",
		summary: "print this help",
		usage: `usage: sift help [command]

Prints the list of commands, or the usage of one command.
`,
	},
}

// usageFor returns the help block of one command, or the overview when the
// command is unknown or empty. A subcommand such as "workspace add" falls back
// to the help of the command it belongs to.
func usageFor(name string) string {
	if command, ok := lookup(name); ok {
		return command.usage
	}
	if head, _, ok := strings.Cut(name, " "); ok {
		if command, ok := lookup(head); ok {
			return command.usage
		}
	}
	return overview()
}

// lookup finds a command by name.
func lookup(name string) (command, bool) {
	for _, c := range commands {
		if c.name == name {
			return c, true
		}
	}
	return command{}, false
}

// overview returns the help text listing every command.
func overview() string {
	var b strings.Builder
	b.WriteString("usage: sift <command> [flags] [arguments]\n")
	b.WriteString("\n")
	b.WriteString("Sift indexes a local corpus and answers queries over the published index.\n")
	b.WriteString("\n")
	b.WriteString("Commands:\n")
	width := 0
	for _, c := range commands {
		if len(c.name) > width {
			width = len(c.name)
		}
	}
	for _, c := range commands {
		fmt.Fprintf(&b, "  %-*s  %s\n", width, c.name, c.summary)
	}
	b.WriteString("\n")
	b.WriteString("Run \"sift help <command>\" for the flags of one command.\n")
	return b.String()
}

// help prints the command overview, or the usage of the named command.
func (r *runner) help(args []string) error {
	switch len(args) {
	case 0:
		fmt.Fprint(r.out, overview())
		return nil
	case 1:
		if command, ok := lookup(args[0]); ok {
			fmt.Fprint(r.out, command.usage)
			return nil
		}
		return &usageError{command: "help", reason: "unknown command: " + args[0]}
	default:
		return &usageError{command: "help", reason: "unexpected argument: " + args[1]}
	}
}

// version prints the version of the command line.
func (r *runner) version(args []string) error {
	if len(args) > 0 {
		return &usageError{command: "version", reason: "unexpected argument: " + args[0]}
	}
	fmt.Fprintf(r.out, "sift %s\n", Version)
	return nil
}
