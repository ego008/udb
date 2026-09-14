## V5.18

### Write-path profiling and ZSet batch optimization

- Added transaction-amortization benchmarks with a true 100-independent-transaction baseline.
- Added batch-size matrix benchmarks for 1/10/50/100/500/1000/5000 items.
- Added explicit 1/2/4/8/16/32 writer contention benchmarks with `ops/s` reporting.
- Added explicit Sync vs NoSync durability benchmark; production defaults are unchanged.
- Added empty `Update` benchmark to expose transaction overhead floor.
- Optimized `ZSetBatch` to reuse bucket lookups and avoid per-item `I2b` heap allocations.
- Centralized ZSet index mutation in a private bucket-level core used by both `Zset` and `ZSetBatch`.
- No changes to recovery, integrity semantics, lifecycle synchronization or durability defaults.


## V5.19

### WritePipeline

- Added `WritePipeline`: concurrent producers -> bounded queue -> single writer -> batched bbolt transaction.
- Added configurable `MaxBatchSize`, `MaxWait`, and `QueueSize`.
- Added `HSet`, `ZSet`, `HDel`, and `ZDel` pipeline operations plus context-aware variants.
- Added ordered `Flush` barrier and graceful `Close` that drains accepted requests.
- Added bounded-queue backpressure and request input-buffer copying.
- Preserved request order; no write coalescing or reordering is performed.
- Added pipeline statistics and V5.19 throughput/contention benchmarks.
- No changes to default bbolt durability settings, recovery, or integrity semantics.

## V5.20

- Added asynchronous `WriteFuture` based write submission.
- Added `HSetAsync`, `ZSetAsync`, `HDelAsync`, and `ZDelAsync` plus context variants.
- Async submission returns after bounded-queue admission instead of waiting for commit.
- Preserved strict submission ordering, bounded backpressure, Flush barriers, and Close drain semantics.
- Preserved batch-level bbolt transaction atomicity; a failed transaction reports the same error to every request in that batch.
- Added V5.20 async correctness and performance benchmarks.
- No operation coalescing or reordering was introduced.

## V5.21

### Adaptive Write Pipeline and Observability

- Added `PipelineStats` latency, batch, throughput and queue metrics.
- Added `BatchPolicy` with `FixedBatchPolicy` and `AdaptiveBatchPolicy`.
- Added adaptive batching based on queue pressure and observed commit latency.
- Added `BackpressurePolicy` with block, reject and context-driven timeout modes.
- Added `ErrWritePipelineFull` for explicit queue rejection.
- Added V5.21 metrics, policy and backpressure tests.
- Added V5.21 adaptive and parallel-producer benchmarks.
- Preserved V5.20 ordering, Flush, Close, Future, input-copy and transaction-atomicity semantics.
- No changes to recovery, integrity, ZSet storage format or default durability.
