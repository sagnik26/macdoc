# S4: the node-prefix query read the whole scan

**Hypothesis (H2 in `plan.md`).** Detectors ask `store.Nodes` for the children of a folder (about nine call sites). The SQL was `path_fold LIKE ? ESCAPE '\'` behind `(? = '' OR …)`, and I guessed that this stops SQLite from using the index.

**Method.** Build a synthetic scan of 300,000 directory nodes through the real `store` API (1,000 projects of 300 directories each, a 59 MB database), time `store.Nodes` for detector-style prefixes, then read the plans with `EXPLAIN QUERY PLAN` on a copy of the file and test candidate queries against it. The data is synthetic, but the planner and the timings are SQLite's own.

## What was wrong

The guess was incomplete. The `LIKE` was not the whole problem.

| Query | Plan | Time per call |
|---|---|---:|
| as it was | `SEARCH nodes USING INDEX nodes_scan_bytes (scan_id=? AND bytes>?)` then a temp B-tree for ordering | **237 ms** |
| path as a range on the primary key | still `nodes_scan_bytes (scan_id=? AND bytes>?)` | 222 ms |
| `+bytes` only (still `LIKE`) | `nodes_scan_bytes (scan_id=?)` | 247 ms |
| `+bytes` and the range | `MULTI-INDEX OR` over `sqlite_autoindex_nodes_1` (scan_id, path_fold) | **0.21 ms** |

The query also asks for `bytes >= ?`, and that predicate matches the secondary index `(scan_id, bytes)`. SQLite chose it, read every node of the scan and filtered on the path row by row, so each lookup cost the size of the whole scan. Both changes are needed: the range, so the primary key *can* answer the path condition, and `+bytes` (a unary plus makes the term non-indexable), so the planner does not pick the bytes index. The results were identical to the old query's.

That is about **1,100 times faster** per lookup, and a detector pass over many folders was quadratic in the scan size before.

## The change

`store.Nodes` has two shapes. Without a prefix it keeps `bytes >= ?` indexable (the `(scan_id, bytes)` index serves it in 0.17 ms). With a prefix it matches the folded prefix itself or a range from `prefix/` up to, not including, `prefix0` (`0` is the character after `/`), with `+bytes`.

## Guards

`internal/store/tests/nodes_prefix_test.go`:

- `TestNodesPrefixIsComponentWise` uses names on both sides of the range edges (`/a/bc`, `/a/b0`, `/a/b.x`, `/a/b+x`, `/a/b_x`, `/a/b%x`, upper case, a trailing slash) and asserts exactly what is included. It passes on the old and the new query, which is the evidence that behavior is unchanged.
- `TestNodesPrefixDoesNotReadTheWholeScan` runs 100 prefix queries on 60,000 nodes and fails above 1.5 s. Negative control: with the old query it fails at 7.7 s; with the new one the whole test, including building the data, takes about 0.4 s.

## Limits

Synthetic data with directories only and evenly spread sizes. On a real scan the kept set is smaller (nodes are kept above 100 MB or for recipe roots), so the absolute gain on a real Mac is unknown; the shape of the cost is not.
