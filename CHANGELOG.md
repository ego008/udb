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

## V5.22

- Added explicit `Atomic` / `AtomicContext` transaction helpers.
- Added explicit `ReadTransaction` / `ReadTransactionContext` helpers.
- Added atomic `Batch` transaction builder for mixed Hash/ZSet operations.
- Added long-lived consistent `Snapshot` read views with idempotent Close.
- Added database-level transaction/snapshot/batch metrics and reset support.
- Preserved V5.21 WritePipeline ordering, async, adaptive batching and backpressure semantics.
- Added V5.22 transaction, batch, snapshot and metrics tests/benchmarks.

## V5.22.1

- Fixed Snapshot long-lived read transaction deadlock/blocking with bbolt mmap growth.
- Snapshot now creates an independent point-in-time bbolt copy using one short-lived source read transaction.
- Snapshot lifetime no longer occupies the source DB lifecycle admission slot.
- Snapshot reads use short-lived read transactions on the private snapshot DB.
- Snapshot.Close is idempotent and removes its temporary snapshot file.
- Added regressions proving an open Snapshot does not block writes or DB.Close, while preserving point-in-time consistency.

## V5.22.2

- Fixed Snapshot/write deadlock on bbolt v1.5.x caused by relying on a copied bbolt database as a long-lived snapshot backend.
- Snapshot is now materialized into UDB logical Hash/ZSet data in memory during one managed read transaction.
- The source read transaction is always released before Snapshot returns, so Snapshot cannot block source mmap growth, writes, compaction, or Close.
- Preserved the existing Snapshot public API and point-in-time consistency semantics.


## V5.23

### Unified write core and observability

- Added one internal ordered mutation engine shared by Batch, WritePipeline, and public batch helpers.
- Added per-transaction bucket caching to reduce repeated Hash/ZSet bucket lookup work.
- Preserved write ordering, ZSet primary/secondary index atomicity, rollback behavior, and existing public APIs.
- Added pipeline queue-wait latency metrics.
- Added Snapshot logical byte-count and capture-duration metrics.
- Extended `ResetMetrics` to clear cumulative Snapshot metrics.
- Added V5.23 regression tests for unified writes, batch metrics, pipeline queue latency, Snapshot metrics, and atomic rollback.
- No changes to recovery format, integrity semantics, lifecycle model, or default bbolt durability.

## V5.24

- Added transaction-scoped `ReadEngine` for unified Hash/ZSet point reads and scans.
- Added bucket lookup reuse within a read transaction without adding allocation cost to single point reads.
- Refactored `Hget`, `HgetInt`, `Zget`, and `Zscore` through the common read core.
- Added high-level `HGetInt`, `ZScore`, `HGetBatch`, and `ZGetBatch` helpers.
- Added V5.24 read-engine correctness, ownership, batch-read, and scan regression tests.
- Added read-path benchmarks comparing single transactions, grouped `ReadTransaction` calls, batch reads, and ZScan/ZScanEach.
- No changes to recovery, integrity, Snapshot, lifecycle, or default durability semantics.

## V5.25

### ReadSession and ownership-safe iterators

- Added reusable `ReadSession` for high-level Hash/ZSet reads.
- Added `ReadSession.Read` / `ReadContext` for grouping reads inside one short-lived consistent transaction.
- Added `HIterator` / `ZIterator` and forward/reverse constructors.
- Iterators materialize their requested range and release the bbolt read transaction before returning.
- Iterator data is copied and remains valid after the source transaction closes.
- Added regressions proving a slow iterator consumer does not hold a read transaction and block writes.
- Added context cancellation and idempotent session/iterator close tests.
- Preserved V5.24 ReadEngine, Snapshot, lifecycle, recovery, integrity and durability semantics.
- No long-lived bbolt read transaction is introduced by V5.25.

## V5.26

- Added callback-based heterogeneous `ReadBatch` with transaction-scoped zero-copy result delivery.
- Added context-aware `ReadBatch.ExecuteContext`.
- Reduced `HIterator` and `ZIterator` per-entry allocation by using contiguous byte arenas.
- Preserved the V5.22+ rule that public iterators never hold a long-lived bbolt read transaction.
- Added V5.26 correctness tests and benchmarks.

## V5.27

### Read Path 2.0

- Added zero-copy `ReadBatch.HGetBorrowed` and `ReadBatch.ZScoreBorrowed` APIs. Borrowed keys must remain unchanged until the batch execution returns.
- Added automatic sorted-batch execution for homogeneous `ReadBatch` operations whose keys are already in ascending byte order. The implementation uses one bbolt cursor and sequential traversal instead of one B-tree search per key.
- Added specialized `HGetMany` / `HGetManyContext` and `ZScoreMany` / `ZScoreManyContext` APIs with callback-scoped borrowed input/output buffers.
- Added sorted-cursor fast paths to the specialized Many APIs while preserving callback order and duplicate-key semantics.
- Preserved ownership-safe `ReadBatch.HGet` / `ReadBatch.ZScore` behavior and all V5.26 transaction/lifecycle semantics.
- Added V5.27 correctness tests and read-path benchmarks for sorted, unsorted, duplicate and borrowed-key workloads.

## V5.28

### Adaptive Read Planner and workload benchmark

- Added `ReadPath`, `ReadPlan`, `ReadPlannerOptions`, `PlanReadKeys`, and `PlanReadKeysWithOptions`.
- Added deterministic automatic selection between B-tree point lookups and sequential cursor traversal for homogeneous reads.
- Planner considers key count, sortedness, adjacent-key locality, and duplicate ratio; it never changes logical result order.
- Wired the planner into `ReadBatch`, `HGetMany`, and `ZScoreMany` while preserving all V5.27 ownership and transaction-lifetime rules.
- Added workload benchmarks for batch size, sorted/reverse workloads, 100%/50%/10% hit rates, ZSet reads, parallel readers, and planner overhead.
- Added planner and semantic regression tests.
- Deliberately did not add a read cache: consistency, invalidation, Snapshot, Repair, Pipeline, and Compact interactions remain out of the V5.28 scope.
