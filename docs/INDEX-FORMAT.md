# Index format

Everything Sift publishes is plain JSON in an ordinary directory. This
document is the contract for those bytes: what is in each file, how the digests
are computed, how generations succeed one another, and exactly how a generation
is committed so that a reader never sees a partial one.

All files are UTF-8, indented with two spaces, end with a single trailing
newline, and use LF line endings. Key order is fixed: struct fields serialize in
declaration order and `encoding/json` sorts map keys, so the same index always
produces byte-identical files.

## Output directory

The output directory is `<corpus-root>/sift-out` unless `.sift.json` sets
`output_dir` (which must be relative to the root).

```
sift-out/
  manifest.json           the live generation; the only entry point
  extract-cache.json      the extraction cache of the live generation
  gen-0002/               segment files of generation 2
    seg-0001.json
    seg-0002.json
  .staging/               present only during a publish
```

Three invariants hold at all times:

1. `manifest.json` names exactly one generation, and every file it names is
   present with the recorded digest.
2. Segment files live in the generation directory that owns them, so
   publishing generation N+1 never overwrites a file generation N still names.
3. `.staging/` is private to the publisher. A reader never opens it, and one
   left behind means a publish was interrupted.

## manifest.json

The manifest is the commit point and the first file any reader opens.

```json
{
  "Generation": 1,
  "Segments": [
    {
      "ID": "seg-0001",
      "File": "gen-0001/seg-0001.json",
      "Digest": "7a5582e910d5d1c446b1ad0783b269f4d3a283377215fa599306d482665915f2",
      "DocCount": 4,
      "TermCount": 38
    }
  ],
  "DocCount": 4,
  "TermCount": 38,
  "ConfigHash": "704866891f94cd7177b9e997fc159cff781070ee8bdcb27b2b6b5a38ca94f95b",
  "CacheDigest": "0db0bf15b65f41c3691b78990ff42b76801738c0f66ca6ffa9dc9ecb60d6bb54",
  "CreatedAt": "2026-08-23T00:06:47.1059082+05:30"
}
```

| Field | Meaning |
| --- | --- |
| `Generation` | The generation number, starting at 1 and increasing by exactly one. |
| `Segments` | The segment references, sorted by `ID` then `File`. |
| `Segments[].ID` | The segment id, `seg-0001`, `seg-0002` and so on. Unique within a manifest. |
| `Segments[].File` | The slash-separated path of the segment file relative to the output directory, for example `gen-0001/seg-0001.json`. It must not be absolute and must not escape the output directory. |
| `Segments[].Digest` | Hex SHA-256 of the segment file's exact bytes. |
| `Segments[].DocCount` | Documents in that segment. |
| `Segments[].TermCount` | Distinct terms in that segment alone. |
| `DocCount` | Documents across the whole generation. Equal to the sum of the segment counts, because segments partition documents. |
| `TermCount` | Distinct terms across the whole generation. **Not** the sum of the segment counts: segments share terms, so this is computed once over the merged index. |
| `ConfigHash` | Hex SHA-256 of the canonical configuration that produced the generation. |
| `CacheDigest` | Hex SHA-256 of `extract-cache.json` as published. |
| `CreatedAt` | Publication time, RFC 3339 with nanoseconds. The only value that changes between two otherwise identical runs. |

`CreatedAt` comes from a `core.Clock`, so a test can publish with a
`core.FixedClock` and compare manifest bytes byte for byte.

## Segment files

A segment is one immutable slice of an index: the documents it owns, the term
statistics computed from its own postings, and those postings keyed by term.
Documents are assigned in ascending document-id order, at most `segment_docs`
per segment, so the same index always splits the same way.

```json
{
  "Ref": {
    "ID": "seg-0001",
    "File": "seg-0001.json",
    "Digest": "",
    "DocCount": 4,
    "TermCount": 38
  },
  "Docs": [
    {
      "ID": "README.md",
      "Length": 15,
      "Fields": {
        "dir": ".",
        "ext": ".md",
        "kind": "markdown",
        "language": ""
      },
      "Title": "Release notes",
      "ContentHash": "c8e5c48c944eac1a66cd4e76d1f76913dbeed699f21a70560960d8ac40ff18ed"
    }
  ],
  "Terms": [
    { "Term": "add",     "DocFreq": 1, "TotalFreq": 1 },
    { "Term": "answers", "DocFreq": 1, "TotalFreq": 1 },
    { "Term": "bm25",    "DocFreq": 1, "TotalFreq": 1 }
  ],
  "Postings": {
    "add": [
      { "DocID": "notes/todo.txt", "Freq": 1, "Positions": [4] }
    ],
    "answers": [
      { "DocID": "README.md", "Freq": 1, "Positions": [6] }
    ]
  }
}
```

Ordering rules, all enforced on write and relied on by readers:

- `Docs` is sorted by `ID` ascending. `ID` is the source file's path relative
  to the corpus root, slash-separated, so it is stable across machines and
  orderable as a plain string.
- `Terms` is sorted by `Term` ascending.
- Each posting list is sorted by `DocID` ascending, and `Positions` ascending
  within a posting.
- `Postings` is a JSON object, so `encoding/json` emits its keys in sorted
  order.

Two subtleties:

- **`Ref.Digest` is always empty inside the file.** A file cannot contain its
  own hash. Writing a segment always emits an empty digest, which is what makes
  the bytes reproducible; the digest of those bytes is then recorded in the
  manifest. On read, the digest is recomputed from the file and compared
  against the manifest before anything is parsed.
- **`Ref.File` inside the file is the bare file name** (`seg-0001.json`), while
  the manifest records the path within the output directory
  (`gen-0001/seg-0001.json`). A segment describes itself; the manifest places
  it.

`Terms` in a segment are **segment-local**: `DocFreq` counts documents in that
segment only. Global statistics are recomputed when segments are merged back
into an index for searching, which is why a term appearing in two segments is
never double-counted.

## extract-cache.json

The extraction cache stores what extraction and tokenization produced for each
document, keyed by document id and guarded by content hash.

```json
{
  "Entries": {
    "README.md": {
      "ContentHash": "c8e5c48c944eac1a66cd4e76d1f76913dbeed699f21a70560960d8ac40ff18ed",
      "Doc": {
        "ID": "README.md",
        "Path": "README.md",
        "Title": "Release notes",
        "Kind": "markdown",
        "Language": "",
        "Body": "# Release notes\n\nSift indexes a local corpus ...",
        "Fields": { "dir": ".", "ext": ".md", "kind": "markdown", "language": "" },
        "Size": 139,
        "ContentHash": "c8e5c48c944eac1a66cd4e76d1f76913dbeed699f21a70560960d8ac40ff18ed"
      },
      "Tokens": [
        { "Term": "release", "Position": 0 },
        { "Term": "notes",   "Position": 1 }
      ]
    }
  }
}
```

The cache is an optimization and never a source of truth:

- A missing, unreadable or malformed cache file loads as an **empty** cache
  with no error, so a caller can always rebuild from the corpus itself.
- An entry is reusable only while its `ContentHash` still equals the SHA-256 of
  the file's current bytes.
- The cache is reused only when the published manifest's `ConfigHash` still
  equals the hash of the configuration in force, because tokenization depends
  on `min_term_length` and `stopwords`.
- After each run the cache is pruned to the documents that exist now, so it
  cannot grow without bound.

Deleting `extract-cache.json` costs one full re-extraction and nothing else,
but note that `manifest.json` records its digest: deleting it makes the current
generation fail validation until the next `sift index`.

## Digests

There is exactly one digest function in Sift: lower-case hex SHA-256, via
`internal/store`.

| Digest | Covers |
| --- | --- |
| `SegmentRef.Digest` | The exact bytes of the segment file, which always carry an empty `Ref.Digest`. |
| `Manifest.CacheDigest` | The exact bytes of `extract-cache.json`. |
| `DocInfo.ContentHash` | The raw bytes of the source file, before any normalization. |
| `Manifest.ConfigHash` | The canonical encoding of the configuration, not of `.sift.json` itself. |

The configuration hash deliberately excludes the corpus root and is invariant
to the order of `include`, `exclude` and `stopwords`: each list is sorted and
every value is length-prefixed before hashing, under a version tag
(`sift-config/1`) that must change if the encoding ever changes. The result
is that the same corpus checked out at two different paths, or with its glob
lists written in a different order, produces identical manifest bytes.

`DocInfo.ContentHash` hashes the file as it is on disk, so a change that
normalization would erase (a CRLF becoming an LF, a trailing space) still
invalidates the cache entry. That is the safe direction to err in.

## Generations

A generation is one complete published version of an index.

- Numbering starts at 1 and increases by exactly one. The next number is read
  from the current manifest; a directory with no readable manifest starts at 1.
- Each generation owns a directory named `gen-NNNN` (zero-padded to four
  digits; wider for generation 10000 and beyond) holding its segment files.
- Only one generation is live: the one `manifest.json` names.
- After a successful commit, the directories of superseded generations are
  removed. A directory that cannot be removed is left in place on purpose, as
  dead weight rather than as a failure of the generation just committed. Only
  directories matching `gen-` followed by digits are ever pruned, so nothing
  else in the output directory is at risk.

## Staging and swap

Publishing is a two-phase operation. The manifest is the only commit point.

**Phase 1, stage.** Nothing a reader can see is touched.

1. Ensure the output directory exists and read the current generation number.
2. Remove any leftover `.staging/` and create it fresh. If `.staging` exists
   and is not a directory, publishing stops with an integrity error rather than
   removing it, because it is not something a published index put there.
3. Split the index and write every segment into `.staging/`, recording each
   digest from the bytes actually written. The segment reference records the
   path the file will have once installed (`gen-NNNN/seg-NNNN.json`).
4. Write `.staging/extract-cache.json` and hash it.
5. Build the manifest in memory from the segment references, the configuration
   hash, the cache digest, the clock and the index counts.

**Phase 2, commit.** Ordered so that every step before the last is invisible.

6. Remove any stale `gen-NNNN/` directory for the generation being written.
7. Read the current `extract-cache.json` into memory, if there is one, so it
   can be restored on failure.
8. `os.Rename(.staging, gen-NNNN)`. The new segment files are now on disk, but
   no manifest names them, so nothing has changed for a reader.
9. `os.Rename(gen-NNNN/extract-cache.json, extract-cache.json)`.
10. Write `manifest.json` atomically. **This is the commit.**
11. Prune the generation directories of superseded generations.

**On failure at any step**, the publisher restores what it changed: the
previous cache bytes are written back (or the file is removed if there was
none), the half-installed generation directory is removed, and `.staging/` is
removed. The previous `manifest.json` is never touched until step 10 succeeds,
so a reader that loads it still gets the previous generation, complete and
verified.

Every write in this sequence, including the manifest itself, goes through
`store.WriteFileAtomic` or `store.WriteJSONAtomic`: a temporary file in the
destination directory followed by `os.Rename`. Rename within a directory is
atomic on the platforms Sift targets, so a file holds either its previous
bytes or the complete new bytes, never a prefix.

## Reading a generation

A reader must validate before it parses:

1. Load `manifest.json`. Missing means the corpus was never indexed
   (`core.ErrNotFound`).
2. Validate it against the directory. In order, stopping at the first problem:
   - every segment reference has a non-empty, unique `ID`;
   - every `File` is relative and stays inside the output directory;
   - every segment file exists, is a regular file, and hashes to its recorded
     `Digest`;
   - `extract-cache.json` exists and hashes to `CacheDigest`;
   - `DocCount` equals the sum of the segment document counts;
   - `TermCount` lies between the largest single segment's term count and the
     sum of all of them, since segments share terms.
   A failure is a `*core.IntegrityError` naming the offending path relative to
   the output directory, and it matches `core.ErrIntegrity`.
3. Only then read each segment, verifying its digest again on the way in, and
   merge them into a searchable index.

Nothing is loaded partially: a damaged generation yields an error and no index.

`sift validate` runs the same checks but reports every problem instead of
stopping at the first, and adds two non-fatal findings the loader does not
care about:

- `manifest.json: configuration changed since generation N was published`, when
  `ConfigHash` no longer matches the configuration in force. The index is
  intact but stale relative to your settings; re-run `sift index`.
- `.staging: left behind by an interrupted publish`.

Both still make the command exit 1, because "the index on disk does not match
what you asked for" is worth a non-zero status in a script.

## Compatibility

The format has no version field of its own. The pair that identifies it is the
generation layout described here plus `Manifest.ConfigHash`, whose encoding is
tagged `sift-config/1`. Two rules follow:

- Any change to the canonical configuration encoding must bump that tag, so old
  and new hashes can never be compared as equal.
- Any change to the segment or manifest shape must change the digest of every
  file it touches, which is exactly what validation already detects: an index
  published by an incompatible build fails validation rather than being
  misread.

Because every file is self-contained JSON with recorded digests, an output
directory can be copied, archived or shipped as is. Point a build of the same
version at it and it will load, or refuse and tell you which path is wrong.
