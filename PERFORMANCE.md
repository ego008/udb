

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
