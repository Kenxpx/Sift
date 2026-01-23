# Worked examples

Five sessions with a real corpus, from an empty directory to a served index.
Every command and every block of output below was produced by running it; only
the paths have been shortened to `./corpus`.

The examples build on one another, so the generation numbers advance from 1 to
3 as you read.

## The corpus

```
corpus/
  README.md
  notes/
    search.md
    todo.txt
  src/
    rank.go
```

```
$ cat corpus/README.md
# Release notes

Sift indexes a local corpus and answers queries over the published index.
The search engine ranks documents with BM25.

$ cat corpus/notes/todo.txt
Improve the ranking of short documents.
Add a snippet extractor to the search report.
```

`notes/search.md` is a two-line note about the search path, and `src/rank.go`
is a four-line Go file.

## Example 1: index it and ask it questions

No configuration file, so the defaults apply: every readable file under the
root is a candidate, terms shorter than two runes and the 86 built-in stopwords
are dropped, and up to 256 documents go in a segment.

```
$ sift index -root ./corpus
generation: 1
segments: 1
documents: 4
terms: 38
config: 704866891f94cd7177b9e997fc159cff781070ee8bdcb27b2b6b5a38ca94f95b
```

Four documents, one segment, and the hash of the configuration that produced
them. Here is what landed on disk:

```
$ find corpus/sift-out
corpus/sift-out
corpus/sift-out/extract-cache.json
corpus/sift-out/gen-0001
corpus/sift-out/gen-0001/seg-0001.json
corpus/sift-out/manifest.json
```

Get the shape of the corpus:

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

Now query it. Two terms means both must be present:

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

A single common term reaches three documents, and length normalization decides
the order: all three contain `documents` exactly once, and the shortest one
wins.

```
$ sift search -root ./corpus documents
query: documents
total: 3
shown: 3

1. notes/todo.txt
   title: Improve the ranking of short documents.
   score: 0.3944
   freq: 1
   fields: dir=notes, ext=.txt, kind=text, language=

2. notes/search.md
   title: Search design
   score: 0.3308
   freq: 1
   fields: dir=notes, ext=.md, kind=markdown, language=

3. README.md
   title: Release notes
   score: 0.3204
   freq: 1
   fields: dir=., ext=.md, kind=markdown, language=
```

A phrase matches adjacent tokens. Note that `to` and `the` sit between `search`
and `report` in the source text; they are stopwords, so they were never
indexed, and the phrase still matches:

```
$ sift search -root ./corpus '"search report"'
query: "search report"
total: 1
shown: 1

1. notes/todo.txt
   title: Improve the ranking of short documents.
   score: 1.7259
   freq: 1
   fields: dir=notes, ext=.txt, kind=text, language=
```

Page the results and count facets at the same time. `total` and the facet
counts describe **all** matches, while `shown` describes the page:

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

Finally, confirm that what is on disk is what the manifest says it is:

```
$ sift validate -root ./corpus
generation: 1
segments: 1
documents: 4
terms: 38
problems: none
```

## Example 2: narrow the corpus with .sift.json

Say you only want prose indexed, you want longer terms, and you want small
segments so the generation directory is easier to inspect.

```
$ cat > corpus/.sift.json <<'JSON'
{
  "include": ["*.md", "*.txt"],
  "exclude": [".sift.json"],
  "min_term_length": 3,
  "segment_docs": 2
}
JSON
```

`include` and `exclude` are matched against both the slash path and the base
name, so `*.md` catches `notes/search.md` as well as `README.md`. The `exclude`
entry is there because `.sift.json` is a file in the corpus like any other
and would otherwise be indexed as a JSON source document.

Check what is now in force before doing anything:

```
$ sift config -root ./corpus
root: corpus
output: corpus/sift-out
include: *.md, *.txt
exclude: .sift.json
max_file_bytes: 1048576
min_term_length: 3
segment_docs: 2
stopwords: 86 (default list)
hash: 6fc08f83b7abc6706ae7b9eec2b29f4e763cde3493ac763854035c6ac26517b5
```

The hash changed, so the published generation is now stale relative to your
settings. `validate` says so, and exits non-zero, without claiming anything is
corrupt:

```
$ sift validate -root ./corpus
generation: 1
segments: 1
documents: 4
terms: 38
problems:
  manifest.json: configuration changed since generation 1 was published
sift validate: index has 1 problem: sift: index integrity
$ echo $?
1
```

Rebuild. The new generation drops `src/rank.go`, splits three documents across
two segments, and re-tokenizes everything (a longer minimum term length means
the extraction cache from generation 1 cannot be reused):

```
$ sift index -root ./corpus
generation: 2
segments: 2
documents: 3
terms: 31
config: 6fc08f83b7abc6706ae7b9eec2b29f4e763cde3493ac763854035c6ac26517b5
```

```
$ find corpus/sift-out
corpus/sift-out
corpus/sift-out/extract-cache.json
corpus/sift-out/gen-0002
corpus/sift-out/gen-0002/seg-0001.json
corpus/sift-out/gen-0002/seg-0002.json
corpus/sift-out/manifest.json
```

`gen-0001/` is gone: superseded generation directories are pruned once the new
manifest is committed. The Go file is gone from the statistics too:

```
$ sift stats -root ./corpus
documents: 3
terms: 31
tokens: 38

by kind:
  markdown: 2
  text: 1

by language:
  (none): 3

largest documents:
  1. README.md: 15
  2. notes/search.md: 14
  3. notes/todo.txt: 9
```

```
$ sift validate -root ./corpus
generation: 2
segments: 2
documents: 3
terms: 31
problems: none
```

Scores changed as well, because `N`, `avgdl` and the term statistics all
changed with the corpus. Facet on the directory this time:

```
$ sift search -root ./corpus -facet dir documents
query: documents
total: 3
shown: 3

1. notes/todo.txt
   title: Improve the ranking of short documents.
   score: 0.1515
   freq: 1
   fields: dir=notes, ext=.txt, kind=text, language=

2. notes/search.md
   title: Search design
   score: 0.1280
   freq: 1
   fields: dir=notes, ext=.md, kind=markdown, language=

3. README.md
   title: Release notes
   score: 0.1242
   freq: 1
   fields: dir=., ext=.md, kind=markdown, language=

facets:
  dir:
    .: 1
    notes: 2
```

## Example 3: re-index only when something moved

`sift watch` compares the corpus against a recorded scan and writes the new
scan back, so a loop can skip the work when nothing changed. Keep the state
file outside the corpus, or exclude it, so it does not index itself.

The first run has no baseline, so everything is new:

```
$ sift watch -root ./corpus -state ./corpus.scan.json
files: 3
changes: 3
  added     README.md
  added     notes/search.md
  added     notes/todo.txt
```

The second run compares against what the first one wrote:

```
$ sift watch -root ./corpus -state ./corpus.scan.json
files: 3
changes: 0
```

Now edit a file, add another, and delete the Go file:

```
$ echo 'Snippets are still on the list.' >> corpus/notes/todo.txt
$ echo 'Draft.' > corpus/notes/idea.md
$ rm corpus/src/rank.go
$ sift watch -root ./corpus -state ./corpus.scan.json
files: 4
changes: 2
  added     notes/idea.md
  modified  notes/todo.txt
```

Only two changes: `src/rank.go` was outside the `include` patterns, so it was
never part of the scan and its removal is not a change to this corpus.

Re-index, and the extraction cache does its job. Every file is still read
once, but the configuration has not moved, so the two untouched documents are
taken straight from `extract-cache.json`; only the modified and the new file
are extracted and tokenized again:

```
$ sift index -root ./corpus
generation: 3
segments: 2
documents: 4
terms: 35
config: 6fc08f83b7abc6706ae7b9eec2b29f4e763cde3493ac763854035c6ac26517b5
```

```
$ sift watch -root ./corpus -state ./corpus.scan.json
files: 4
changes: 0
```

A minimal incremental loop:

```sh
#!/bin/sh
# Re-index only when the corpus moved.
set -eu
root=${1:-.}
state=${2:-./corpus.scan.json}
if sift watch -root "$root" -state "$state" | grep -q '^changes: 0$'; then
  echo "no changes"
else
  sift index -root "$root"
fi
```

## Example 4: serve the index

```
$ sift serve -root ./corpus -addr 127.0.0.1:8080
listening on 127.0.0.1:8080
```

The address is echoed after the listener is bound, so `-addr 127.0.0.1:0`
tells you which port you got. The configuration is resolved before the port is
bound, so an unusable corpus is one error at startup rather than a stream of
failing requests.

```
$ curl -s http://127.0.0.1:8080/healthz
ok
```

`GET /search` takes the same options as the command line: `q`, `limit`,
`filter` (repeatable, `field:value`) and `facet` (repeatable, comma
separated).

```
$ curl -s 'http://127.0.0.1:8080/search?q=documents&limit=1&facet=kind'
{
  "Options": {
    "Query": "documents",
    "Filters": null,
    "Limit": 1,
    "Facets": [
      "kind"
    ]
  },
  "Total": 3,
  "Results": [
    {
      "DocID": "notes/todo.txt",
      "Path": "notes/todo.txt",
      "Title": "Improve the ranking of short documents.",
      "Score": 0.3369812353776982,
      "Freq": 1,
      "Fields": {
        "dir": "notes",
        "ext": ".txt",
        "kind": "text",
        "language": ""
      },
      "Snippet": ""
    }
  ],
  "Facets": {
    "kind": {
      "Field": "kind",
      "Counts": {
        "markdown": 2,
        "text": 1
      }
    }
  }
}
```

Errors are JSON too, with the status repeated in the body. A query the parser
cannot read is a client error:

```
$ curl -s -w ' status=%{http_code}\n' 'http://127.0.0.1:8080/search?q=%22open'
{
  "Error": "search \"\\\"open\": sift: query: at 0: unterminated quote",
  "Status": 400
}
 status=400
```

A corpus that was never indexed answers 404, and anything else answers 500.
Only `GET` and `HEAD` are accepted; other methods get 405 with an `Allow`
header.

To pipe results into another tool, the CLI is usually easier than the API,
because it renders CSV and Markdown as well as JSON:

```
$ sift search -root ./corpus -format csv documents > hits.csv
$ sift search -root ./corpus -format md documents >> report.md
```

Diagnostics go to standard error, so the redirects above never pick up an error
message by accident.

## Example 5: catch a damaged index

Every segment file and the extraction cache are covered by a SHA-256 digest in
the manifest. Change one byte of a segment:

```
$ printf ' ' >> corpus/sift-out/gen-0003/seg-0002.json
$ sift validate -root ./corpus
generation: 3
segments: 2
documents: 4
terms: 35
problems:
  gen-0003/seg-0002.json: digest mismatch
sift validate: index has 1 problem: sift: index integrity
$ echo $?
1
```

A reader refuses the whole generation rather than returning half of it, and
names the same path:

```
$ sift search -root ./corpus documents
sift search: app: load corpus: sift: index integrity: gen-0003/seg-0002.json: digest mismatch
$ echo $?
1
```

`validate` reports everything it finds, not just the first problem, including
things that are untidy rather than corrupt:

```
$ rm corpus/sift-out/extract-cache.json
$ mkdir corpus/sift-out/.staging
$ sift validate -root ./corpus
generation: 3
segments: 2
documents: 4
terms: 35
problems:
  extract-cache.json: missing
  .staging: left behind by an interrupted publish
sift validate: index has 2 problems: sift: index integrity
```

The fix for all of these is the same, and it is always safe: run
`sift index` again. Publishing builds the new generation in `.staging`,
installs it, and commits by writing the manifest last, so the damaged
generation is replaced whole or not at all.

## Example 6: several corpora in one workspace

The registry lives in `<root>/.sift-workspace.json` and holds a name, a root
and an output directory per corpus. The first corpus registered is the active
one, marked `*`.

```
$ sift workspace add -root ./ws docs ../corpus
registered docs
$ sift workspace add -root ./ws scratch ../scratch index-out
registered scratch
$ sift workspace list -root ./ws
* docs      root=../corpus    output=../corpus/sift-out
  scratch   root=../scratch   output=../scratch/index-out
```

An output directory given as an absolute path is used as is, a relative one
resolves against the corpus root, and an omitted one means
`<root>/sift-out`. (Path separators are the platform's own, so Windows
prints backslashes here.)

```
$ sift workspace remove -root ./ws scratch
removed scratch
$ sift workspace list -root ./ws
* docs      root=../corpus    output=../corpus/sift-out
```

The registry is a plain JSON file written atomically, so it can be committed
alongside a project. The commands themselves still take `-root`: the workspace
records where your corpora are, it does not change what a command points at.
