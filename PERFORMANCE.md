# UDB Performance Engineering

## V5.18 write-path profiler

V5.18 is a measurement release plus one narrowly scoped write-path optimization
for ZSet batching. The goal is to separate three costs that were previously
mixed together:

1. one bbolt write transaction per item;
2. one transaction containing many item mutations;
3. UDB's own per-item ZSet overhead.

### Primary benchmark matrix

```bash
go test -run '^$' -bench '^BenchmarkV518' -benchmem -count=5
```

The most important comparisons are:

- `BenchmarkV518Hash100IndividualTx` vs `BenchmarkV518Hash100Batch` vs `BenchmarkV518Hash100Tx`
- `BenchmarkV518Z100IndividualTx` vs `BenchmarkV518Z100Batch` vs `BenchmarkV518Z100Tx`
- `BenchmarkV518BatchSizeHash/1|10|50|100|500|1000|5000`
- `BenchmarkV518BatchSizeZSet/1|10|50|100|500|1000|5000`
- `BenchmarkV518EmptyUpdate` for the fixed transaction overhead floor

The `*IndividualTx` benchmarks perform exactly 100 independent transactions
per benchmark iteration. Their `items/op` and `items/s` metrics make the
transaction-amortization effect explicit instead of comparing unlike units.

### Writer contention

V5.18 measures explicit writer counts instead of relying on `RunParallel`'s
implementation-defined worker distribution:

```bash
go test -run '^$' -bench '^BenchmarkV518ParallelWriters(Hash|ZSet)$' -benchmem -count=5
```

Each benchmark reports `ops/s` for 1, 2, 4, 8, 16 and 32 concurrent writers.
The expected result is a plateau rather than linear scaling because bbolt
serializes write transactions. Extra writers are useful only if they hide
application-level scheduling gaps; they cannot make the single bbolt writer
itself parallel.

### Durability experiment

```bash
go test -run '^$' -bench '^BenchmarkV518WriteDurability$' -benchmem -count=5
```

This reports `Sync` and `NoSync` separately. `NoSync=true` changes durability
semantics and is **not** a performance setting that should be enabled merely
to make production benchmarks look better.

### Profiles

CPU profile:

```bash
go test -run '^$' -bench '^BenchmarkV518Hash100Batch$' -benchmem -cpuprofile cpu-v518-hash-batch.out -benchtime=5s
go tool pprof -top cpu-v518-hash-batch.out
```

ZSet batch profile:

```bash
go test -run '^$' -bench '^BenchmarkV518Z100Batch$' -benchmem -cpuprofile cpu-v518-zset-batch.out -benchtime=5s
go tool pprof -top cpu-v518-zset-batch.out
```

Mutex profile:

```bash
go test -run '^$' -bench '^BenchmarkV518ParallelWriters(Hash|ZSet)$' -benchmem -mutexprofile mutex-v518.out -benchtime=5s
go tool pprof -top mutex-v518.out
```

Blocking profile:

```bash
go test -run '^$' -bench '^BenchmarkV518ParallelWriters(Hash|ZSet)$' -benchmem -blockprofile block-v518.out -benchtime=5s
go tool pprof -top block-v518.out
```

### V5.18 production change

`ZSetBatch` now looks up the two ZSet buckets once per transaction, encodes
scores into a fixed-width stack buffer, and calls a private bucket-level core.
The core preserves the existing primary member→score map and secondary
score→member index semantics, including unchanged-score index repair and old
index deletion when a score changes.

The public semantics, recovery model, integrity checks and transaction atomicity
are unchanged. `Zset` continues to use the same canonical core, so there is one
implementation of the actual two-index mutation logic.

No new global lock was introduced. No recovery heuristic was changed. No
`NoSync` default was changed.

### Interpreting V5.18

The acceptance questions are:

- How many items/s does one transaction gain over 100 individual transactions?
- At what batch size does throughput stop improving materially?
- Is the knee different for Hash and ZSet?
- How much allocation overhead remains in ZSetBatch after removing repeated
  bucket lookup and score encoding allocations?
- Does writer concurrency plateau as expected?
- How large is the Sync vs NoSync delta, and is that delta worth the durability
  trade-off for a particular application?

Do not optimize further from a single `ns/op` result. Use `benchstat` and CPU,
heap, mutex and block profiles on the same machine, Go version, bbolt version,
fixture and benchmark command.

## V5.19 WritePipeline

V5.19 moves write optimization from individual API calls to transaction
amortization under concurrent load:

```text
producer 1 ─┐
producer 2 ─┼─> bounded queue ─> single writer ─> one bbolt Update per batch
producer N ─┘
```

`WritePipeline` is intentionally conservative. Requests are assigned a
monotonic submission sequence while holding the submission gate, so concurrent
producers cannot overtake one another. The first request starts the batch wait
timer; the writer flushes when `MaxBatchSize` is reached, `MaxWait` expires, a
`Flush` barrier is encountered, or the queue is closed.

The pipeline does **not** coalesce operations in V5.19. For example,
`HSet(k,v1) -> HDel(k) -> HSet(k,v2)` remains exactly that sequence.

Recommended starting points from the V5.18 measurements:

- durable workloads: `MaxBatchSize=100`, `MaxWait=5ms`, `QueueSize=1024`
- higher-throughput ingestion: test 500-1000 item batches before going larger
- keep normal `NoSync=false`; `NoSync` is a durability trade-off, not a normal
  performance switch

Benchmarks:

```bash
go test -run '^$' -bench '^BenchmarkV519' -benchmem -count=5
go test -race ./...
```

## V5.20 Async WritePipeline

V5.20 adds asynchronous submission on top of the V5.19 bounded queue. The synchronous methods remain available; the new `*Async` methods separate request admission from completion so a producer can submit many requests before waiting for their results.

Recommended benchmark comparison:

```bash
go test -run '^$' -bench '^BenchmarkV520' -benchmem -count=5
```

Important metrics are `items/s` for batched workloads and `ops/s` for producer throughput. Compare async workloads with the existing direct and `HSetBatch` benchmarks. Durable throughput remains constrained by bbolt's serialized write transaction and sync/commit costs; asynchronous batching reduces the number of commits per application request but does not make bbolt support concurrent durable write transactions.
