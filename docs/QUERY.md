# Queries, ranking and facets

Sift's query language is small on purpose: four clause shapes, one negation
prefix, and AND between everything. This document specifies the syntax, the
scoring, how filters and facets interact with paging, and the determinism rules
every renderer obeys.

## Syntax

A query is a whitespace-separated list of clauses. Each clause is one of:

| Form | Meaning |
| --- | --- |
| `term` | The document body contains the term. |
| `"two words"` | The document body contains those terms at adjacent positions. |
| `field:value` | The document's field equals the value, exactly. |
| `field:"two words"` | The document's field equals `two words`, exactly. |
| `-clause` | Any of the above, negated: documents it matches are excluded. |

Clauses combine with **AND**: every non-negated clause must match, and no
negated clause may match. There is no `OR` and no grouping.

An empty query, or one that is only whitespace, matches **every document**. So
does a query made only of negated clauses: they are subtracted from the whole
corpus.

```
$ sift search -root ./corpus                       # every document
$ sift search -root ./corpus search ranking        # both terms
$ sift search -root ./corpus "search report"       # adjacent terms
$ sift search -root ./corpus kind:markdown         # field selector
$ sift search -root ./corpus notes -design         # has notes, not design
$ sift search -root ./corpus -- -search            # everything but search
```

The last line needs `--`: a query whose **first** argument begins with `-`
would otherwise be read as a flag. Once any non-flag argument has appeared,
later `-clauses` are part of the query and need no escaping, which is why
`notes -design` works as written.

### Case and normalization

- Bare terms and phrase words are **lower-cased** by the parser, because the
  tokenizer indexes lower-case terms only. `Search`, `SEARCH` and `search` are
  the same query.
- The value of a `field:value` clause is kept **exactly as written**, because
  field values are compared exactly. `kind:Markdown` matches nothing;
  `kind:markdown` matches.
- Field **names** are lower-cased. `Kind:markdown` and `kind:markdown` are the
  same clause.

`Query.String()` renders a parsed query back to canonical text, and parsing
that text reproduces the same query, so a query survives a round trip through
storage or a URL unchanged.

### Terms that can never match

A query term is matched against the index as one whole token. The tokenizer
splits on runs of characters that are neither letters nor digits, so a term
containing a separator will never be found:

```
$ sift search -root ./corpus search-engine
query: search-engine
total: 0
shown: 0
```

Write it as a phrase instead: `"search engine"`.

For the same reason, terms shorter than `min_term_length` and terms on the
stopword list match nothing, because they were never indexed:

```
$ sift search -root ./corpus the
query: the
total: 0
shown: 0
```

### Parse errors

Parsing is total: every input yields either a query or a `*core.QueryError`
carrying the zero-based byte offset it stopped at. The command line checks the
query **before** it opens the index, so a typing mistake is reported as such
even against a corpus that was never indexed, and exits 2 rather than 1.

| Input | Message |
| --- | --- |
| `-` | `bad query at 0: missing term after '-'` |
| `a:` | `bad query at 1: missing value after ':'` |
| `:x` | `bad query at 0: missing field name before ':'` |
| `"open` | `bad query at 0: unterminated quote` |
| `""` | `bad query at 0: empty phrase` |
| `x"y` | `bad query at 1: unexpected quote` |
| `"ab"cd` | `bad query at 4: unexpected text after quote` |

## Phrases

A phrase matches when its words occupy consecutive token positions. Positions
come from the token stream, not from the raw text, and the tokenizer numbers
surviving tokens contiguously. That has a useful consequence: **terms the
tokenizer dropped do not break a phrase.**

Given the document

```
Improve the ranking of short documents.
Add a snippet extractor to the search report.
```

the tokens are `improve`(0), `ranking`(1), `short`(2), `documents`(3),
`add`(4), `snippet`(5), `extractor`(6), `search`(7), `report`(8): `the`, `of`,
`a` and `to` are stopwords and are gone, and the line break is just another
separator. The phrase `"search report"` therefore matches, and so would
`"extractor search"`.

The frequency reported for a phrase is the number of times the phrase occurs,
not the number of times its words occur.

## Fields

Extraction gives every document four filterable, facetable fields:

| Field | Value |
| --- | --- |
| `kind` | `markdown`, `text`, `source` or `other`, from the extension. |
| `language` | The source language for `source` documents (`go`, `python`, `rust`, `json`, `yaml`, ...), empty otherwise. |
| `dir` | The slash-separated directory of the file relative to the corpus root; `.` for files at the root. |
| `ext` | The lower-case extension including the dot, for example `.md`. |

Field values are compared with `==`, never matched as globs or substrings. A
document that has no value for a field compares equal to the empty string, so
selecting on an empty value selects the documents that lack it.

## Filters versus field clauses

`-filter field:value` and a `field:value` clause in the query text do the same
comparison, and they differ only in ergonomics:

| | Field clause | `-filter` |
| --- | --- | --- |
| Written | Inside the query text | As a repeatable flag |
| Negatable | Yes, `-kind:source` | No |
| Value handling | Kept as written; quote it for spaces | Kept as written, spacing and all |
| Echoed in a report | As part of `query:` | On its own `filters:` line |
| Repeated field | Both clauses must hold, so a repeat with two values matches nothing | The last value given wins |

```
$ sift search -root ./corpus -filter kind:markdown -filter dir:notes design
$ sift search -root ./corpus kind:markdown dir:notes design
```

Select documents missing a field with an empty value:

```
$ sift search -root ./corpus -filter language:
query: (all documents)
filters: language=
total: 3
shown: 3
```

## Ranking

Scoring is BM25-lite: the Okapi BM25 term weight with `k1 = 1.2` and
`b = 0.75`, computed from statistics the index already keeps, so nothing extra
is stored in a generation. Each positive **body** clause contributes

```
  idf(t) * (tf * (k1 + 1)) / (tf + k1 * (1 - b + b * dl / avgdl))

  idf(t) = ln(1 + (N - df + 0.5) / (df + 0.5))
```

where `N` is the number of indexed documents, `df` the number of documents
holding the term, `tf` its frequency in this document, `dl` the document length
in tokens and `avgdl` the mean document length. A phrase is weighed by its
words, since the adjacency check has already been done.

What follows from the formula:

- **Rare terms outweigh common ones.** `idf` is always positive and grows as
  `df` shrinks.
- **Short documents win ties.** With the same term frequency, a shorter
  document scores higher:

  ```
  $ sift search -root ./corpus documents
  query: documents
  total: 3
  shown: 3

  1. notes/todo.txt        score: 0.3944   freq: 1    (9 tokens)
  2. notes/search.md       score: 0.3308   freq: 1   (14 tokens)
  3. README.md             score: 0.3204   freq: 1   (15 tokens)
  ```

- **Term frequency saturates.** The tenth occurrence of a term adds far less
  than the second.
- **Field clauses and negations select but never score.** They contribute
  nothing to the sum, so a query made only of those leaves every candidate at
  zero and the ordering falls through to the tie-breakers:

  ```
  $ sift search -root ./corpus "kind:markdown -design"
  1. README.md
     score: 0.0000
     freq: 0
  ```

Scores are computed by visiting clauses, then terms, then postings in a fixed
order, so a given index and query always produce bit-identical scores.

### Result order

Results are sorted by:

1. `Score` descending,
2. then `Freq` descending,
3. then `DocID` ascending.

The last key makes the order **total**, so equally relevant documents never
change places between runs. `Freq` is the total number of query-term
occurrences the document contributed across every clause that matched it.

## Facets and paging

`-facet field` counts the values of a field. It is repeatable and accepts
comma-separated lists, so these are equivalent:

```
$ sift search -root ./corpus -facet kind,language search
$ sift search -root ./corpus -facet kind -facet language search
```

The ordering of operations inside `search.Run` is the part that matters:

```
  1. gather    clauses: AND the positives, subtract the negatives
  2. filter    apply -filter, exactly
  3. count     Total and Facets, over EVERY match
  4. score     BM25-lite
  5. order     score, freq, doc id
  6. truncate  apply -limit
```

Facets and `total` are computed at step 3, before `-limit` applies at step 6.
So a page of 2 out of 3 matches still reports facet counts describing all 3:

```
$ sift search -root ./corpus -limit 2 -facet kind,language search
query: search
total: 3
shown: 2
...
facets:
  kind:
    markdown: 2
    text: 1
  language:
    (none): 3
```

Because ordering is global and truncation comes last, a limited page is always
the head of the full ordering, never an arbitrary subset. A non-positive
`-limit` returns everything.

Faceting details:

- A requested field with nothing to count is still reported, with no values
  under it.
- A document with no value for a faceted field is counted under the empty
  string, rendered as `(none)`.
- Repeating a field name in `-facet` counts it once.
- Facets are always computed from the index, so they describe the published
  generation, never a stale cache.

## Output formats

`-format` selects the renderer. All four are deterministic and end with exactly
one trailing newline.

| Format | Shape | Facets |
| --- | --- | --- |
| `text` (default) | Header, then one indented block per result, then facet counts. | Yes |
| `md` | A header list, a results table, and one table per facet. Cells escape `\|`, and code spans escape backticks, so a title cannot break the table. | Yes |
| `csv` | RFC 4180 with the header row `rank,doc_id,path,title,score,freq,fields,snippet`. Fields are `key=value` pairs joined with `;`. | No: a flat table has no place for them. Use `json`, `text` or `md`. |
| `json` | An object with `query`, `filters`, `limit`, `requested_facets`, `total`, `shown`, `results` and `facets`. Absent maps and slices render as `{}` and `[]`, never `null`. | Yes |

Scores render with exactly four fractional digits in `text`, `md` and `csv`, so
the same score always produces the same characters. The JSON renderer emits the
full float, for consumers that want to re-sort.

`Snippet` is always empty. An index stores document statistics, not bodies, so
there is no text to excerpt at search time. The field exists in every format so
that adding snippets later cannot change the shape of the output.

## Determinism rules

These hold everywhere in the query path, and are what let tests compare bytes
instead of parsing output:

1. **No map is ever ranged into output.** Document fields, filters and facet
   values are emitted in ascending key order; facets themselves in ascending
   field order.
2. **Every sort key is total.** Results break ties on `DocID`; facets and
   fields on their key; documents and terms on their own identifiers.
3. **Scores are computed in a fixed traversal order**, so floating-point
   addition happens in the same sequence every time and the sums are
   bit-identical.
4. **Renderers are pure functions of the report.** Nothing consults the clock,
   the environment or the filesystem.
5. **Line endings are LF** and every rendered document ends with exactly one
   newline.
6. **Values are flattened, never dropped.** Embedded newlines and tabs in a
   title become spaces, so a value can never break the line-oriented layout.

The same rules apply to the HTTP API: `GET /search` accepts `q`, `limit`,
`filter` (repeatable, `field:value`) and `facet` (repeatable, comma separated),
and answers with the JSON encoding of the same `core.SearchReport` a rendered
report is built from.
