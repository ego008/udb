## V5.15

Performance optimization release driven by V5.14 CPU and allocation profiles.

### ZSet integrity scanner

- Replaced per-primary-member secondary-index `Cursor.Seek` validation with a two-pass primary/secondary scan backed by a transaction-scoped member map.
- Removed the N×B-tree-search behavior that dominated `CheckIntegrity` CPU and allocation profiles.
- Preserved detection of missing, orphaned, malformed and mismatched ZSet indexes, including the historical missing-index + mismatch accounting for a wrong-score secondary entry.
- `CheckIntegrity` and `RepairIntegrity` continue to share the same logical report/repair semantics.

### ZScan

- Replaced one heap allocation per returned ZSet entry with an append arena while retaining the public post-transaction ownership guarantee.
- Added an internal transaction-scoped `zscanEach` zero-copy path for synchronous consumers that do not need to retain Bolt-backed slices.
- Added V5.15 benchmark coverage and regression tests.

### Compatibility

- No storage format or recovery decision logic changed.
- Existing public `Zscan` ownership semantics are preserved.

# Changelog

## V5.14

Performance profiling infrastructure release based on V5.13.1.

### Performance profiling

- Added stable `BenchmarkV514*` benchmark names for Hash, ZSet, Integrity and Compact workloads.
- Reused the established V5.12/V5.13 benchmark bodies so profiling does not change the historical workload.
- Added documented CPU, heap, mutex, block and execution-trace profiling workflows.
- Added a repeatable `benchstat` comparison workflow for V5.12 versus V5.14.

### Compatibility and correctness

- No public API was removed or changed.
- No storage format or recovery decision logic was changed.
- V5.13.1 shared-score integrity behavior remains intact.
- Profiling helpers contain no production-code synchronization or persistence changes.


## V5.13

Performance engineering release based on the V5.12 benchmark baseline.

### Performance changes

- Optimized `Hget` and `Zget` reply construction to avoid the extra `newReply()+append` slice-growth path on successful reads.
- Optimized `Zscan` result copying: one allocation now backs the copied `score||member` index key, with disjoint subslices returned as member and score. The returned data remains independent from bbolt's mmap pages.
- Optimized `CheckIntegrity` ZSet validation by reusing a single secondary-index cursor instead of creating a cursor for every primary score entry.
- Replaced per-entry composite-key construction in the integrity check with cursor seeking by the fixed-width score prefix, avoiding one temporary `score||member` allocation per entry.
- Optimized `RepairIntegrity` to rebuild the secondary index directly from the primary score map instead of first materializing all valid entries into an O(N) temporary slice.
- Added a profiling/performance workflow document covering CPU, memory, mutex and block profiling.

### Compatibility and correctness

- No public API was removed or changed.
- V5.11.1 recovery, integrity, property and fuzz semantics are preserved.
- Malformed primary ZSet scores are still fail-closed unless `RepairOptions.DropInvalidScores` is explicitly enabled.
- Returned `Reply.Data` remains safe after the read transaction closes.

## V5.13.1

- Fixed an integrity-check regression introduced by the V5.13 ZSet allocation optimization.
- `cursorHasScoreMember` now scans the complete contiguous `score||member` range after `Cursor.Seek(score)`, so multiple members sharing the same score are handled correctly.
- Added `TestV513IntegritySharedScore` regression coverage.
