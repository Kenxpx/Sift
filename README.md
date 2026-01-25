# Sift

Sift is a dependency-free local document index and query engine.

Point it at a directory, run `sift index`, and it walks the tree, extracts
text from Markdown, plain text and source files, tokenizes it, and publishes an
inverted index as a set of immutable JSON files. `sift search` then answers
BM25-ranked queries over that index, with phrases, field selectors, negation,
filters and facets, in text, Markdown, CSV or JSON.

Everything is built on the Go standard library. There are no third-party
imports, no database, no daemon and no network access: an index is a directory
of ordinary files you can copy, diff, commit or delete.

- **Deterministic.** The same corpus and the same configuration always produce
  the same bytes. Nothing is emitted from an unsorted map, and the publication
  timestamp is the only value in a generation that changes between runs.
- **Crash-safe.** Every file is written to a temporary file and renamed into
  place. A generation is built in a staging directory and committed by writing
  one manifest last, so a reader sees the previous generation or the new one
  and never a mixture.
- **Verifiable.** The manifest records a SHA-256 digest for every segment file
  and for the extraction cache. `sift validate` re-reads them and reports
  the first path that disagrees.

## Requirements

Go 1.22 or newer. That is the whole list.

## Install

Build the command from a checkout:

```
git clone <repository-url> sift
cd sift
go build -o bin/sift ./cmd/sift
```

Or install it onto your `PATH` (into `go env GOBIN`, or `go env GOPATH`/bin
when `GOBIN` is unset):

```
go install ./cmd/sift
```

A container image is also provided. It runs the test suite during the build, so
a successful image is a tested one. The image sets no entrypoint, so name the
binary in the command:

```
docker build -t sift .
docker run --rm -v "$PWD:/corpus" sift sift index -root /corpus
docker run --rm -v "$PWD:/corpus" sift sift search -root /corpus index
```

## Quick start

Index a corpus, then query it. The examples below use a small corpus with a
`README.md`, two files under `notes/` and one under `src/`.

```
$ sift index -root ./corpus
generation: 1
segments: 1
documents: 4
terms: 38
config: 704866891f94cd7177b9e997fc159cff781070ee8bdcb27b2b6b5a38ca94f95b
```

```
$ sift search -root ./corpus search ranking
query: search ranking
total: 1
shown: 1

1. notes/todo.txt
   title: Improve the ranking of short documents.
   score: 1.7259
   freq: 2
   fields: dir=notes, ext=.txt, kind=text, language=
```

Limit the page and count facets over every match, not just the page:

```
$ sift search -root ./corpus -limit 2 -facet kind,language search
query: search
total: 3
shown: 2

1. notes/search.md
   title: Search design
   score: 0.4654
   freq: 2
   fields: dir=notes, ext=.md, kind=markdown, language=

2. notes/todo.txt
   title: Improve the ranking of short documents.
   score: 0.3944
   freq: 1
   fields: dir=notes, ext=.txt, kind=text, language=

facets:
  kind:
    markdown: 2
    text: 1
  language:
    (none): 3
```

Summarize the corpus and check the index on disk:

```
$ sift stats -root ./corpus
documents: 4
terms: 38
tokens: 47

by kind:
  markdown: 2
  source: 1
  text: 1

by language:
  (none): 3
  go: 1

largest documents:
  1. README.md: 15
  2. notes/search.md: 14
  3. notes/todo.txt: 9
  4. src/rank.go: 9
```

```
$ sift validate -root ./corpus
generation: 1
segments: 1
documents: 4
terms: 38
problems: none
```

Serve the same index over HTTP:

```
$ sift serve -root ./corpus -addr 127.0.0.1:8080
listening on 127.0.0.1:8080
```

```
$ curl -s "http://127.0.0.1:8080/search?q=search&limit=1&facet=kind"
```

More end-to-end walkthroughs live in [docs/EXAMPLES.md](docs/EXAMPLES.md).

## Output layout

Indexing publishes into `<root>/sift-out` unless `output_dir` says
otherwise. After two indexing runs the directory looks like this:

```
corpus/
  README.md                     source files
  notes/
  src/
  .sift.json                 optional per-corpus configuration
  sift-out/
    manifest.json               the live generation: counts, digests, config hash
    extract-cache.json          extraction cache of the live generation
    gen-0002/                   segment files of generation 2
      seg-0001.json
      seg-0002.json
```

- `manifest.json` is the single commit point and the only file a reader opens
  first. It names the generation, lists every segment file with its SHA-256
  digest, and records the digest of the extraction cache and the hash of the
  configuration that produced them.
- `gen-NNNN/seg-NNNN.json` are the immutable segment files: documents, term
  statistics and postings for one slice of the corpus. Each generation owns its
  own directory, so publishing never overwrites a file the previous manifest
  still names. Superseded generation directories are pruned after a successful
  commit.
- `extract-cache.json` maps each document to the content hash, document and
  tokens produced for it. A rebuild reuses an entry whose content hash still
  matches, which is what makes re-indexing an unchanged corpus cheap. It is an
  optimization only: delete it and the next run rebuilds from the corpus.
- `.staging/` appears only while a generation is being built. One left behind
  means a publish was interrupted; `sift validate` reports it, and the next
  publish removes it.

The full on-disk contract is in [docs/INDEX-FORMAT.md](docs/INDEX-FORMAT.md).

## Concepts

| Concept | What it is |
| --- | --- |
| **Corpus** | A directory tree Sift indexes, plus its optional `.sift.json`. The corpus root is the only thing a command needs to be pointed at. |
| **Document** | One source file turned into indexable form: an id (its slash-separated path relative to the root), a title, a kind, a normalized body, and the fields `kind`, `language`, `dir` and `ext`. |
| **Token** | One term occurrence: a lower-cased run of letters and digits plus its zero-based position in the document. Terms below the minimum length and stopwords are dropped; positions stay contiguous, so a document's length is the number of tokens kept. |
| **Index** | The in-memory inverted index: which documents exist, which terms exist, and the postings (document, frequency, positions) for each term. Term statistics are derived from the postings on every read, so a document added twice is counted once. |
| **Segment** | One immutable, self-describing slice of an index on disk, holding at most `segment_docs` documents. Segments are cut in document-id order and named `seg-0001`, `seg-0002` and so on, so the same index always splits the same way. |
| **Generation** | One complete published version of an index, numbered from 1 upward. A reader always observes a whole generation. |
| **Manifest** | The description of a generation: its number, its segments and their digests, the document and term counts, the configuration hash and the cache digest. Writing it is what commits a generation. |
| **Cache** | The extraction cache published beside a generation, keyed by document id and validated by content hash. Missing or malformed means "rebuild", never an error. |
| **Query** | A whitespace-separated list of clauses: bare terms, quoted phrases, `field:value` selectors, and any of those negated with a leading `-`. An empty query matches every document. |
| **Facets** | Counts of a field's values across every match, computed before the result page is truncated, so "2 of 3 shown" sits beside counts describing all 3. |
| **Workspace** | A registry of named corpora at `<root>/.sift-workspace.json`: name, root and output directory for each, with one marked active. |

## CLI reference

Run `sift help <command>` for the flags of one command.

| Command | What it does | Flags |
| --- | --- | --- |
| `sift index` | Scans the corpus, extracts and tokenizes what changed, and publishes the result as a new generation. Prints the generation, counts and configuration hash. | `-root dir` |
| `sift search [query...]` | Answers a query against the published index. | `-root dir`, `-limit n`, `-filter field:value` (repeatable), `-facet field` (repeatable, comma separated), `-format text/md/csv/json` |
| `sift stats` | Prints document, term and token counts, the breakdown by kind and language, and the longest documents. | `-root dir` |
| `sift validate` | Checks every segment and the cache against the digests in the manifest and lists every problem, including a configuration that changed since publication. Exits 1 when there is at least one. | `-root dir` |
| `sift config` | Prints the configuration in force: defaults with `.sift.json` applied over them, the output path and the configuration hash. | `-root dir` |
| `sift workspace list` | Lists the registered corpora, marking the active one with `*`. | `-root dir` |
| `sift workspace add name corpus-root [output-dir]` | Registers a corpus under a name. | `-root dir` |
| `sift workspace remove name` | Removes a corpus from the registry. | `-root dir` |
| `sift serve` | Serves `GET /search` and `GET /healthz` until stopped. | `-root dir`, `-addr host:port` (default `127.0.0.1:8080`) |
| `sift watch` | Scans once and reports the files added, modified and removed since the scan in the state file, then writes the new scan back. | `-root dir`, `-state file` |
| `sift version` | Prints the version. | none |
| `sift help [command]` | Prints the command list, or the usage of one command. | none |

`-root` defaults to `.` everywhere.

### Exit codes

| Code | Meaning |
| --- | --- |
| `0` | The command succeeded. `-h` on any command also exits 0. |
| `1` | The command ran and failed: a corpus that was never indexed, a damaged index, a validation that found problems. |
| `2` | The invocation was rejected: unknown command, unknown flag, bad `-filter`, unknown `-format`, or a query the parser could not read. The usage of the command is printed to standard error. |

Normal output goes to standard output and every diagnostic to standard error,
so `sift search ... > results.csv` never mixes the two.

### Configuration

A corpus is configured by an optional `.sift.json` in its root. Only the
keys present override the defaults. Key names are matched ignoring case,
underscores and dashes, so `output_dir`, `outputDir` and `OutputDir` all name
the same setting; an unknown key is an error.

```json
{
  "output_dir": "sift-out",
  "include": ["*.md", "*.txt"],
  "exclude": ["vendor/*", "*.min.js"],
  "max_file_bytes": 1048576,
  "min_term_length": 3,
  "segment_docs": 256,
  "stopwords": ["the", "and", "of"]
}
```

| Setting | Default | Meaning |
| --- | --- | --- |
| `output_dir` | `sift-out` | Where generations are published. Must be relative to the corpus root. |
| `include` | empty | Glob patterns a file must match, tried against both the slash path and the base name. Empty means every file is a candidate. |
| `exclude` | empty | Glob patterns that skip a file, matched the same way. |
| `max_file_bytes` | `1048576` | Skip files larger than this. `0` means no limit. |
| `min_term_length` | `2` | Drop terms shorter than this, measured in runes. |
| `segment_docs` | `256` | Maximum documents per segment file. |
| `stopwords` | empty | Terms to drop. Empty means the built-in list of 86 English stopwords. |

The corpus root is deliberately not part of the configuration hash, so the
same corpus checked out at two paths produces identical manifest bytes. Note
that `.sift.json` is itself a file in the corpus and is indexed unless you
exclude it.

## Development

From the repository root:

```
go build ./...      # compile every package
go test ./...       # run every test, including the end-to-end suite in tests/
go vet ./...        # report suspicious constructs
gofmt -l .          # must print nothing
```

House rules for contributions:

- Standard library only. No third-party imports, ever.
- ASCII in code and comments, LF endings, tabs (that is, `gofmt` output).
- A doc comment on every exported symbol, and a package comment on one file per
  package.
- Determinism: sort before returning, and never range a map straight into
  output.
- Errors wrapped with `%w` and matched with `errors.Is`; no panics in library
  code; no global mutable state; time only through `core.Clock`; file writes
  only through `internal/store`.
- Tests are table-driven, live beside the code they cover, use `t.TempDir()`
  for filesystem work, and assert specific values rather than non-nil.

## Project layout

```
.
+-- cmd/
|   +-- sift/         main: the only file that touches the process
+-- internal/
|   +-- app/             wires the packages into user-level operations
|   +-- cache/           extraction cache keyed by content hash
|   +-- cli/             argument parsing, dispatch, exit codes, help
|   +-- config/          defaults, .sift.json, validation, config hash
|   +-- core/            shared types and error kinds (the contract)
|   +-- extract/         file bytes to Document (kind, title, body, fields)
|   +-- facet/           value counts over a set of documents
|   +-- index/           in-memory inverted index
|   +-- manifest/        build, save, load and verify a generation manifest
|   +-- publish/         staging, commit, prune, and loading a generation
|   +-- query/           the query language: parse and canonical rendering
|   +-- rank/            BM25-lite scoring and the canonical result order
|   +-- report/          text, Markdown, CSV, JSON and stats rendering
|   +-- scan/            deterministic corpus walk with glob and size rules
|   +-- search/          gather, filter, facet, order, truncate
|   +-- segment/         split an index into immutable segment files
|   +-- server/          GET /search and GET /healthz
|   +-- stats/           corpus summary
|   +-- store/           atomic writes, JSON helpers, digests, directories
|   +-- token/           tokenization and stopwords
|   +-- validate/        integrity checks and diagnostic findings
|   +-- watch/           diff two scans into added, modified and removed
|   +-- workspace/       the registry of named corpora
+-- tests/               end-to-end tests over real temporary corpora
+-- docs/
|   +-- ARCHITECTURE.md  package map and data flow
|   +-- INDEX-FORMAT.md  on-disk formats, digests, generations, commit
|   +-- QUERY.md         query syntax, ranking, filters, facets, determinism
|   +-- EXAMPLES.md      worked examples
+-- CHANGELOG.md
+-- Dockerfile
+-- LICENSE
+-- README.md
+-- go.mod
```

## Documentation

- [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) - the package map and how data
  flows through indexing and search.
- [docs/INDEX-FORMAT.md](docs/INDEX-FORMAT.md) - segment and manifest formats,
  digests, generations, staging and swap.
- [docs/QUERY.md](docs/QUERY.md) - query syntax, ranking, filters, facets and
  the determinism rules.
- [docs/EXAMPLES.md](docs/EXAMPLES.md) - worked examples.
- [CHANGELOG.md](CHANGELOG.md) - release history.

## License

MIT. See [LICENSE](LICENSE).
