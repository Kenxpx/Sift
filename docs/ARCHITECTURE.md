# Architecture

Sift is a pipeline with a small, sharp contract in the middle of it. This
document maps the packages onto that pipeline and follows the data through the
two things Sift does: building an index and answering a query.

## Layers

Every package sits in exactly one layer and depends only downward. There are no
cycles, no global mutable state, and no third-party imports anywhere.

```
  process    cmd/sift                     os.Exit and nothing else
  ---------------------------------------------------------------------
  interface  internal/cli    internal/server flags, exit codes, HTTP
  ---------------------------------------------------------------------
  operation  internal/app                    index, search, stats, validate,
                                             watch, config
  ---------------------------------------------------------------------
  pipeline   scan  extract  token  index  segment  manifest  publish
             query rank     search facet  stats    validate  cache
             report  watch  workspace
  ---------------------------------------------------------------------
  ground     internal/config  internal/store defaults, hashing, atomic I/O
  ---------------------------------------------------------------------
  contract   internal/core                   types and error kinds only
```

- **`internal/core`** holds the shared vocabulary: `FileRef`, `Document`,
  `Token`, `Posting`, `TermStats`, `DocInfo`, `SegmentRef`, `Manifest`,
  `Config`, `SearchOptions`, `SearchResult`, `Facet`, `SearchReport`, the
  `Clock` interface, and the sentinel errors `ErrNotFound`, `ErrIntegrity`,
  `ErrConfig`, `ErrQuery`, `ErrUsage` with their typed companions
  (`IntegrityError`, `ConfigError`, `QueryError`, `CorpusError`). It imports
  only `time`. Every other package speaks these types, which is why none of
  them need to know about each other.
- **`internal/store`** is the only package that writes to disk. Everything goes
  through a temporary file plus `os.Rename`, so a reader never sees a partial
  file even if a writer dies mid-write. It also owns the SHA-256 helpers, so
  "the digest of these bytes" means exactly one thing everywhere.
- **`internal/config`** owns the defaults, the `.sift.json` overlay, the
  validation rules and the canonical configuration hash.
- **`internal/app`** is the only place the pipeline packages are wired
  together. It holds no state beyond a `core.Clock`, and it formats nothing, so
  the same `App` backs both the command line and the HTTP server.
- **`internal/cli`** and **`internal/server`** are the two front ends. `cli.Run`
  returns an exit code instead of calling `os.Exit`, so the whole command line
  is testable with two buffers; `server.Handler` takes an `AppSearcher`
  interface, so it never imports `app` at all.

## Package map

| Package | Responsibility | Key entry points |
| --- | --- | --- |
| `core` | The shared type and error contract. | types only |
| `store` | Atomic writes, JSON read/write, digests, directory helpers. | `WriteFileAtomic`, `WriteJSONAtomic`, `ReadJSON`, `SHA256File`, `SHA256Bytes` |
| `config` | Defaults, `.sift.json`, validation, canonical hash. | `Default`, `Load`, `Validate`, `Hash`, `OutputPath` |
| `scan` | Deterministic corpus walk: globs, size limit, skips. | `Walk`, `Match` |
| `extract` | File bytes to `core.Document`: kind, language, title, body, fields. | `Extract`, `IsBinary`, `SupportedExt` |
| `token` | Body to tokens: lower-case, split, drop short terms and stopwords. | `Tokenize`, `Terms`, `IsStopword`, `DefaultStopwords` |
| `index` | In-memory inverted index and the statistics ranking needs. | `New`, `Add`, `Docs`, `Terms`, `Postings`, `Merge` |
| `segment` | Split an index into immutable files; read them back verified. | `Split`, `Write`, `Read`, `ToIndex` |
| `manifest` | Build, save, load and verify the description of a generation. | `Build`, `Save`, `Load`, `Validate`, `Current` |
| `cache` | Extraction results keyed by document id, guarded by content hash. | `New`, `Get`, `Put`, `Prune`, `Load`, `Save`, `Digest` |
| `publish` | Stage a generation, commit it, prune old ones, load one back. | `Publish`, `Load`, `ReadSegment`, `OutputPath`, `GenerationDir` |
| `query` | Parse the query language; render it back canonically. | `Parse`, `Query.String` |
| `rank` | BM25-lite scoring and the canonical result order. | `Score`, `Order` |
| `facet` | Count field values across a set of documents. | `Count` |
| `search` | Gather, filter, count facets, order, truncate. | `Run` |
| `stats` | Corpus summary: counts, breakdowns, longest documents. | `Compute` |
| `report` | Text, Markdown, CSV, JSON and stats rendering. | `Text`, `Markdown`, `CSV`, `JSON`, `Stats` |
| `validate` | Integrity check (fail fast) and diagnostic findings (list all). | `Index`, `Report` |
| `watch` | Diff two scans into added, modified and removed. | `Plan`, `Poll` |
| `workspace` | The registry of named corpora. | `New`, `Load`, `Save`, `List`, `Add`, `Remove`, `InferOutputPath` |
| `app` | The user-level operations, wired from the packages above. | `Index`, `Search`, `Stats`, `Validate`, `Watch`, `Config` |
| `server` | `GET /search` and `GET /healthz` over an `AppSearcher`. | `Handler` |
| `cli` | Argument parsing, dispatch, rendering, exit codes. | `Run` |

## Indexing data flow

`sift index` resolves the configuration, walks the corpus, turns each file
into tokens (reusing the cache where it can), accumulates an index, splits it
into segments, and publishes the whole thing as one new generation.

```
  .sift.json                       corpus tree
       |                                   |
       v                                   v
  +----------+       core.Config      +----------+
  |  config  |----------------------->|   scan   |
  +----------+                        +----------+
       |                                   |
       | Hash(cfg)                         | []core.FileRef, ascending Rel
       |                                   v
       |                        +----------------------+
       |                        |  app.Index: read     |
       |                        |  each file once      |
       |                        +----------------------+
       |                             |            ^
       |          SHA-256 of bytes   |            |  Entry{Doc, Tokens}
       |                             v            |
       |                        +----------------------+
       |                        |        cache         |  hit: reuse as is
       |                        +----------------------+
       |                             |
       |                        miss |
       |                             v
       |                   +---------+   core.Document   +---------+
       |                   | extract |------------------>|  token  |
       |                   +---------+                   +---------+
       |                                                      |
       |                                                      | []core.Token
       |                                                      v
       |                                                +-----------+
       |                                                |   index   |
       |                                                +-----------+
       |                                                      |
       |                                                      v
       |                                                +-----------+
       |                                                |  segment  |  Split
       |                                                +-----------+
       |                                                      |
       +---------------------- ConfigHash ------------------->|
                                                              v
                                                        +-----------+
                                                        |  publish  |
                                                        +-----------+
                                                              |
                       +--------------------------------------+
                       v
              sift-out/
                .staging/               built here, then renamed
                gen-NNNN/seg-NNNN.json  installed
                extract-cache.json      installed
                manifest.json           written last: this is the commit
```

Notes on the parts that are easy to get wrong:

- **The cache is only reused when the configuration still matches.** Cache
  entries are keyed by content hash, but tokenization depends on
  `min_term_length` and `stopwords`. `app.Index` therefore compares the
  published manifest's `ConfigHash` against `config.Hash(cfg)` and starts from
  an empty cache when they differ, so changing the configuration re-tokenizes
  everything.
- **Nothing in the pipeline is fatal by default.** The scanner skips symlinks,
  `.git`, the output directory and unreadable entries; `app.Index` skips a file
  that vanished between the scan and the read, and skips binary content. One
  bad file never fails an index run.
- **The cache is pruned to the files that exist now**, so it cannot grow
  without bound as a corpus churns.
- **`segment.Split` recomputes term statistics per segment** rather than
  copying them from the index, so each segment file is self-describing and can
  be verified on its own.

## Search data flow

`sift search` validates the published generation before reading any of it,
rebuilds an index from the segments, and answers the query.

```
  query text
       |
       v
  +----------+   core.Query{Clauses, MatchAll}
  |  query   |-------------------------------------+
  +----------+                                     |
                                                   |
  sift-out/manifest.json                        |
       |                                           |
       v                                           |
  manifest.Load + manifest.Validate                |
       |  (every segment digest, the cache digest, |
       |   and the counts, before anything loads)  |
       v                                           |
  segment.Read x N  -->  segment.ToIndex  -->  *index.Index
                                                   |
                                                   v
                                     +-----------------------------+
                                     |         search.Run          |
                                     |                             |
                                     | 1. gather   AND / NOT       |
                                     | 2. filter   exact field =   |
                                     | 3. facet.Count  ALL matches |
                                     | 4. rank.Score   BM25-lite   |
                                     | 5. rank.Order   global      |
                                     | 6. truncate     -limit      |
                                     +-----------------------------+
                                                   |
                                                   v
                                        core.SearchReport
                                     (Total and Facets cover
                                      every match, Results is
                                      the head of the ordering)
                                                   |
              +------------------+-----------------+------------------+
              v                  v                 v                  v
        report.Text      report.Markdown       report.CSV       report.JSON
```

The order of steps 3 through 6 is the load-bearing part: facets are counted
over the complete match set and results are ordered globally, and only then is
`-limit` applied. That is what lets a caller print "shown: 2, total: 3" beside
facet counts that describe all 3, and it guarantees a limited page is always
the head of the full ordering rather than an arbitrary subset.

## Serving

`sift serve` binds a listener and hands `server.Handler` an `AppSearcher`.
The handler is a thin translation layer: query parameters become
`core.SearchOptions`, application errors become HTTP statuses
(`ErrQuery`/`ErrUsage`/`ErrConfig` to 400, `ErrNotFound` to 404, anything else
to 500), and every response body except `/healthz` is JSON with the same
two-space indentation the on-disk files use.

Because the handler depends on an interface rather than on `app`, the CLI can
wrap it: `corpusSearcher` answers a request that names no `root` with the
corpus given on the command line, so serving a single corpus needs no
per-request root.

## Testing strategy

- Each package has table-driven tests beside the code, in the same package
  where white-box access helps.
- `tests/` drives the application layer and the command line end to end against
  real corpora in `t.TempDir()`, so scanning, publishing, loading and searching
  are exercised together rather than in isolation.
- Determinism is testable because time enters only through `core.Clock`: a test
  publishes with a `core.FixedClock` and compares manifest bytes directly.
- No network, no sleeps, no goroutine leaks. The serve command takes its
  listener and its serving function as fields, so the command is tested without
  binding a real port.
