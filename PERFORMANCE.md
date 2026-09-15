

## V5.23 profiling focus

V5.23 keeps the V5.22.2 Snapshot lifecycle model: the source bbolt read
transaction ends before `Snapshot()` returns. Snapshot metrics expose the logical
data copied and capture duration so memory/time cost can be measured directly.

The write paths now share `applyWriteOps`, which caches top-level Hash/ZSet
buckets for the lifetime of a write transaction. This targets repeated bucket lookup
overhead without adding a second global write lock. Durable bbolt commit/fsync
remains the expected dominant cost for small transactions.

For pipeline profiling, compare `AvgQueueWaitLatency` with `AvgCommitLatency`: a
high queue wait with normal commit latency indicates admission/batching pressure;
high commit latency with low queue wait indicates the storage commit path is the
limiting factor.

## V5.24 read-path profiling

Use the V5.24 benchmarks to compare per-operation managed transactions against grouped `ReadTransaction` workloads:

```bash
go test -run '^$' -bench '^BenchmarkV524' -benchmem -count=5
```

For CPU/allocation profiling:

```bash
go test -run '^$' -bench '^BenchmarkV524HGet100ReadTransaction$' -benchmem -cpuprofile hget.cpu -memprofile hget.mem
```

The primary expectation is that durable write performance remains unchanged while grouped reads amortize transaction admission and bbolt read-transaction setup across many lookups.

## V5.25 ReadSession / Iterator benchmark plan

Use the V5.25 benchmark group to compare point/batch reads, direct scans, zero-copy
`ZScanEach`, and ownership-safe iterators:

```bash
go test -run '^$' -bench '^BenchmarkV525' -benchmem -count=5
```

Recommended dimensions:

- `HGetBatch`: 10 / 100 / 1000 keys
- `ZScoreBatch`: 10 / 100 / 1000 keys
- `ZScan`: 100 / 1000 entries
- `ZScanEach`: 100 / 1000 entries
- `ZIterator`: 100 / 1000 entries

Iterator benchmarks include iterator construction, materialization, full
consumption, and `Close`, because those operations are the complete ownership-safe
iterator lifecycle. The primary trade-off is lower caller complexity and safe slow
consumption versus memory proportional to the requested result.

## V5.26 Performance Baseline

V5.26 adds two performance-oriented read paths:

1. `ReadBatch`: heterogeneous HGet/ZScore operations executed inside one short read transaction. Results are delivered through a callback and are zero-copy with respect to bbolt pages; callback byte slices must not escape the callback.
2. Iterator arena storage: `HIterator` and `ZIterator` still materialize data before returning, but captured bytes are stored in a contiguous arena instead of cloning every entry into an independent allocation.

Recommended benchmark commands:

```bash
go test -run '^$' -bench '^BenchmarkV526' -benchmem -count=5
```

For CPU/heap profiling:

```bash
go test -run '^$' -bench '^BenchmarkV526ReadBatch100$' -benchmem -cpuprofile cpu.out -memprofile mem.out

go tool pprof -http=:0 cpu.out

go tool pprof -http=:0 mem.out
```

The long-lived read transaction rule from V5.22 remains mandatory: no public iterator may retain a bbolt read transaction between `Next` calls.

## V5.28 Adaptive Read Planner

V5.28 makes the point-vs-cursor choice data-shape aware. The planner uses only the
requested key sequence, so it adds no read transaction and does not inspect or
mutate database state.

Run the workload suite with:

```bash
go test -run '^$' -bench '^BenchmarkV528' -benchmem -count=5
```

Recommended profiling:

```bash
go test -run '^$' -bench '^BenchmarkV528HGetMany100Sorted100Hit$' -benchmem -cpuprofile cpu.out -memprofile mem.out

go tool pprof -http=:0 cpu.out

go tool pprof -http=:0 mem.out
```

Interpretation guide:

- Sorted workloads should use the sequential cursor path once they are large enough.
- Small but strongly clustered keys may use the cursor path through the locality heuristic.
- Reverse/random workloads should remain on point lookups and preserve caller order.
- Hit-rate changes should not alter correctness or force a cache; they are benchmark dimensions for deciding whether a future cache is justified.
- Parallel-reader benchmarks measure bbolt's read concurrency without changing the
  single-transaction ownership model.

The planner is intentionally heuristic rather than benchmark-self-tuning. The
`ReadPlan.Reason`, `Locality`, and `DuplicateRatio` fields make the decision
observable so a real application can compare its workload with benchmark results
before changing planner thresholds.

## V5.29 Read Planner 2.0

V5.29 separates the diagnostic/full planner from the hot-path planner. For batches at or above `MinCursorKeys`, only sortedness can change the physical decision, so the hot path skips locality and duplicate calculations. Small batches retain the V5.28 locality heuristic.

Recommended profiling commands:

```bash
go test -run '^$' -bench '^BenchmarkV529' -benchmem -count=5
go test -run '^$' -bench '^BenchmarkV529HGetMany100Sorted$' -benchmem -cpuprofile cpu.out -memprofile mem.out
```

All database population is performed before `ResetTimer`, so the measured profile represents the read workload rather than benchmark setup writes.

## V5.32 Read Engine 4.0 / Point-First-Seek Access Paths

V5.32 turns the physical read choice into an explicit diagnostic model:

```text
ReadAccessPoint
ReadAccessFirst
ReadAccessSeek
ReadAccessAdaptive
```

The existing planner remains deterministic. It does not measure latency during
normal reads because timing-based self-tuning would add synchronization and
make results sensitive to bbolt mmap/cache state. Instead, `ReadPlan.AccessPath`
records the selected path and `ReadPlannerOptions.CursorStart` can force a
specific cursor start for controlled experiments.

Recommended V5.32 benchmark matrix:

```text
batch:    10 / 32 / 100 / 1000
position: head / middle / deep
path:     Point / First / Seek / Adaptive
```

The direct comparison should include Point `Bucket.Get(key)` repeated N times,
`Cursor.First()+Next()`, `Cursor.Seek(firstKey)+Next()`, and the bounded V5.31
Adaptive cursor. Compare both latency and allocations. Keep benchmark setup
outside `ResetTimer` so database population and bbolt write commits do not
pollute the read profile.

Interpretation rule: choose the path from repeated workload measurements rather
than a single run. In particular, First is only attractive when the requested
range begins very near the bucket head; Seek avoids scanning unrelated keys for
middle/deep ranges; Point remains the natural baseline for small, sparse, or
unsorted requests.

V5.32 does not change the short-lived read transaction model. Long-lived bbolt
read transactions remain intentionally avoided because they can interfere with
mmap growth during writes/compaction.

## V5.31 Read Engine 3.0 / Adaptive Cursor Start

V5.31 addresses the access-pattern difference exposed by V5.30. A sorted cursor does not have one universally optimal starting operation: `First()+Next()` favors head ranges, while `Seek()+Next()` favors deep ranges. Since bbolt does not provide a cheap rank/ordinal API for a key, V5.31 uses a bounded probe.

The default algorithm is:

1. `Cursor.First()`
2. advance at most `HeadProbeKeys` entries (default `8`)
3. if the requested first key is reached, continue with sequential `Next()`
4. otherwise restart with `Cursor.Seek(firstKey)` and continue sequentially

This makes the extra work for deep ranges bounded while retaining the cheap head case. The algorithm is data-independent and does not assume numeric key names or a particular key encoding.

Benchmarks should compare:
- batch sizes `10 / 32 / 100 / 1000`;
- head, middle, and deep starting positions;
- direct Point `Get`;
- `First+Next`;
- `Seek+Next`;
- `Adaptive`;
- allocation counts and transaction setup costs.

The read transaction remains short-lived because previous UDB versions demonstrated that long-lived bbolt readers can interfere with mmap growth.

## V5.30 Read Engine 2.0

V5.30 targets the fixed overhead exposed by the V5.29 profile after the planner
was moved out of the hot CPU path. The main changes are deliberately conservative:

1. Production callbacks construct `ReadEngine` as a stack value through the
   internal `newReadEngine` helper instead of allocating a `*ReadEngine` for
   every short read transaction.
2. Sorted homogeneous cursor reads use `Cursor.Seek(firstKey)` rather than
   `Cursor.First()`. This preserves exact caller ordering while avoiding work
   before the first requested key.
3. The public `NewReadEngine` constructor remains unchanged for compatibility.
4. Read transactions remain short-lived. V5.30 intentionally does not introduce
   long-lived reader transactions, reader pools, global locks, or read caches.

### Benchmarks

Run on the same machine/workload used for V5.29:

```text
BenchmarkV530HGetMany100SortedHead
BenchmarkV530HGetMany100SortedMiddle
BenchmarkV530HGetMany1000SortedMiddle
BenchmarkV530HGetMany100Random
BenchmarkV530NewReadEngine
```

The expected improvement is workload-dependent. The middle/tail sorted cases
should benefit most from `Seek(firstKey)`; the allocation reduction is most
visible in high-frequency managed reads. Use repeated `-count=5` runs and
compare against the V5.29 baseline rather than relying on a single run.
