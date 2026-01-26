# Changelog

All notable changes to Sift are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Sift has been standard-library-only since the first commit, and that is a
compatibility promise, not an implementation detail: no release will ever add a
third-party dependency.

## [Unreleased]

Nothing yet.

## [1.0.0] - 2025-11-18

First stable release. The command line, the on-disk format and the exit codes
are now covered by semantic versioning.

### Added

- `internal/app`: the single wiring layer that turns the pipeline packages into
  user-level operations (`Index`, `Search`, `Stats`, `Validate`, `Watch`,
  `Config`). Both the command line and the HTTP server now sit on top of it, so
  there is exactly one definition of what each operation means.
- `internal/cli`: `Run(args, stdout, stderr) int` returns an exit code instead
  of ending the process, which makes the whole command line testable with two
  buffers. `cmd/sift/main.go` is now the only file that touches the process.
- Per-command help: `sift help <command>`, plus `-h` on any command, which
  is a successful outcome rather than a usage error.
- The exit-code contract: `0` success, `1` a command that ran and failed, `2`
  an invocation the command line rejected. A usage error always names the
  command and prints that command's usage on standard error.
- `sift version`, and the `Version` constant behind it.
- `tests/`: an end-to-end suite that drives the application layer and the
  command line against real corpora in temporary directories.
- `README.md` and `docs/` (`ARCHITECTURE.md`, `INDEX-FORMAT.md`, `QUERY.md`,
  `EXAMPLES.md`).

### Changed

- `sift search` now validates `-format` and parses the query *before* it
  opens the index. A query with a typo is a usage error with the parse offset,
  not a report that the corpus was never indexed.
- The extraction cache is reused only while the published manifest's
  `ConfigHash` still equals the hash of the configuration in force. Cache
  entries are keyed by content hash, but tokenization depends on
  `min_term_length` and `stopwords`, so a configuration change now correctly
  re-tokenizes every document instead of resurrecting stale tokens.
- `sift index` no longer prints the publication timestamp. It was the only
  part of the output that changed between two otherwise identical runs.
- Errors from the pipeline are wrapped so every message names the command that
  produced it.

### Fixed

- `sift serve` resolved the corpus configuration on every request, so an
  unusable corpus produced a stream of failing responses. It is now resolved
  once, before the port is bound, and a bad corpus is a single startup error.
- A `nil` writer passed to `cli.Run` panicked instead of discarding output.

## [0.5.0] - 2025-09-22

Working with more than one corpus, and more ways to get results out.

### Added

- `internal/workspace`: a registry of named corpora at
  `<root>/.sift-workspace.json`, with `sift workspace list|add|remove`.
  An absolute output directory is used as is, a relative one resolves against
  the corpus root, and an empty one means `<root>/sift-out`.
- `internal/watch`: `Plan` diffs two scans into added, modified and removed
  entries, and `Poll` produces the current scan alongside the plan. Exposed as
  `sift watch -state file`, which writes the new scan back so the next run
  has a baseline. Polling was chosen over filesystem events deliberately: it is
  portable, has no background goroutine to leak, and gives the same plan for
  the same pair of scans.
- `internal/server`: `GET /search` (parameters `root`, `q`, `limit`, `filter`,
  `facet`) and `GET /healthz`, exposed as `sift serve -addr host:port`.
  The handler depends on an `AppSearcher` interface, not on a concrete
  application type.
- `report.Markdown` and `report.CSV`, selectable with `-format md` and
  `-format csv`.

### Changed

- Every renderer now guarantees LF line endings and exactly one trailing
  newline, and formats scores with a fixed four fractional digits, so rendered
  output can be compared byte for byte in tests.
- Markdown cells escape `|`, and code spans escape backticks, so a document
  title can no longer break a table.
- HTTP responses use the same two-space JSON indentation as the on-disk files.

### Fixed

- Facet counts were computed after `-limit` was applied, so they described the
  page rather than the result set. Counting now happens before truncation, and
  ordering is global, so a limited page is always the head of the full
  ordering.
- `sift watch` failed on its first run when the state file did not exist
  yet. No baseline now means every file is reported as added.

## [0.4.0] - 2025-07-14

Crash-safe publication and incremental rebuilds. This release changed the
on-disk layout.

### Added

- `internal/publish`: a generation is built in `.staging/` inside the output
  directory, installed with `os.Rename`, and committed by writing
  `manifest.json` last. A failure at any step removes the staging directory,
  restores the previous extraction cache and leaves the previous generation
  exactly as it was.
- Generation directories: segment files now live in `gen-NNNN/`, and the
  directories of superseded generations are pruned after a successful commit.
- `internal/cache`: extraction results keyed by document id and guarded by
  content hash, published beside the generation as `extract-cache.json` with
  its digest recorded in the manifest.
- `internal/validate`: `Index` stops at the first problem for callers that only
  need to know whether an index is usable; `Report` lists everything, including
  non-fatal drift such as a configuration that changed since publication or a
  `.staging` directory left behind.
- `sift validate` and `sift config`.

### Changed

- **Breaking (on disk):** segments moved from `sift-out/seg-NNNN.json` to
  `sift-out/gen-NNNN/seg-NNNN.json`, so publishing a new generation can
  never overwrite a file the live manifest still names. Re-run `sift index`
  to migrate; the previous layout is not read.
- `manifest.json` is now the single commit point. Readers validate it, and
  every digest it records, before any segment is parsed.
- `sift index` prints the generation, the counts and the configuration hash.

### Fixed

- An index run interrupted mid-write could leave a manifest naming a segment
  that was never finished. Staging plus a final manifest write makes that
  state unreachable.
- A malformed extraction cache aborted the run. It now loads as an empty cache
  with no error, because a cache is an optimization and never a source of
  truth.
- The extraction cache grew without bound as documents were removed from a
  corpus. It is now pruned to the documents that exist.

## [0.3.0] - 2025-05-05

Queries.

### Added

- `internal/query`: the clause language (bare terms, `"quoted phrases"`,
  `field:value`, and `-` negation), a parser that is total and reports the byte
  offset of any problem, and a canonical `String` that round-trips through
  `Parse`.
- `internal/rank`: BM25-lite scoring with `k1 = 1.2` and `b = 0.75` over the
  statistics the index already keeps, plus the canonical result order (score
  descending, frequency descending, document id ascending).
- `internal/search`: gather, filter, count, score, order, truncate.
- `internal/facet`: value counts over a set of documents.
- `internal/report`: plain text and JSON rendering.
- `sift search` with `-limit`, `-filter field:value`, `-facet field` and
  `-format text|json`.

### Changed

- `core.DocInfo` now carries `Length` and `Fields`, so ranking and faceting
  need only the index and never the document bodies.
- Query terms and phrase words are lower-cased by the parser to line up with
  the tokenizer, while the value of a `field:value` clause is kept exactly as
  written, because field values are compared exactly.

### Fixed

- Phrase matching worked on raw text offsets, so a phrase spanning a dropped
  stopword failed. It now works on token positions, which are contiguous, so
  `"search report"` matches text reading `search the report`.
- The result order was not total, so equally scored documents could swap places
  between runs. Document id is now the final tie-breaker.

## [0.2.0] - 2025-03-10

An index that lives on disk.

### Added

- `internal/index`: the in-memory inverted index, with `Merge` for combining
  indexes (a document id appearing twice is replaced, never duplicated).
- `internal/segment`: `Split` cuts an index into deterministic,
  document-ordered pieces of at most `segment_docs` documents; `Write`
  serializes one atomically and records the digest of the bytes it wrote;
  `Read` verifies that digest before returning anything; `ToIndex` rebuilds a
  searchable index.
- `internal/manifest`: build, save, load and verify the description of a
  generation, including digest checks for every file it names.
- `internal/stats`: document, term and token counts, breakdowns by kind and
  language, and the ten longest documents.
- `sift index` and `sift stats`.

### Changed

- Segment files always serialize an empty `Ref.Digest`, since a file cannot
  contain its own hash. Writing a segment twice therefore produces identical
  bytes and an identical digest.
- Term statistics are derived from the stored postings on every read rather
  than accumulated, which is what keeps replacement and `Merge` honest.
- `Manifest.TermCount` is computed once over the merged index instead of
  summing the segment counts. Segments partition documents but share terms, so
  summing over-counted every term that appeared in more than one segment.

### Removed

- `sift scan`, whose output was only ever a debugging aid. `sift index`
  reports the same information as counts.

## [0.1.0] - 2025-01-20

Foundations: everything needed to turn a directory into tokens, and nothing
that persists an index yet.

### Added

- `internal/core`: the shared type and error contract every other package
  speaks (`FileRef`, `Document`, `Token`, `Posting`, `TermStats`, `DocInfo`,
  `SegmentRef`, `Manifest`, `Config`, the search shapes, the `Clock` interface,
  and the sentinel and typed errors).
- `internal/store`: the only package that writes to disk. Every write goes to a
  temporary file in the destination directory and is renamed into place, so a
  reader never observes a half-written file. Also the single definition of
  "the SHA-256 of these bytes".
- `internal/config`: the defaults, the optional `.sift.json` overlay
  (matched ignoring case, underscores and dashes), validation, and the
  canonical configuration hash.
- `internal/scan`: a deterministic corpus walk in ascending path order that
  never follows symlinks, never descends into `.git` or the output directory,
  applies the include, exclude and size rules, and skips unreadable entries
  rather than failing the walk.
- `internal/extract`: file bytes to a document, with kind and language from the
  extension, a title from the first Markdown heading or first non-empty line or
  base name, a normalized body, and the `kind`, `language`, `dir` and `ext`
  fields. Binary content is reported as skippable, not fatal.
- `internal/token`: the tokenizer (lower-case, split on non-alphanumeric runs,
  drop short terms and stopwords, contiguous positions) and the built-in list
  of 86 English stopwords.
- `sift scan`, which printed the files a corpus would index.

[Unreleased]: https://example.com/sift/compare/v1.0.0...HEAD
[1.0.0]: https://example.com/sift/compare/v0.5.0...v1.0.0
[0.5.0]: https://example.com/sift/compare/v0.4.0...v0.5.0
[0.4.0]: https://example.com/sift/compare/v0.3.0...v0.4.0
[0.3.0]: https://example.com/sift/compare/v0.2.0...v0.3.0
[0.2.0]: https://example.com/sift/compare/v0.1.0...v0.2.0
[0.1.0]: https://example.com/sift/releases/tag/v0.1.0
