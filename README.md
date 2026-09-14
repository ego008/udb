## V5.18 write-path optimization

V5.18 adds a transaction-amortization benchmark suite and a narrowly scoped ZSet batch optimization. `ZSetBatch` now reuses bucket lookups and avoids per-item score-buffer heap allocation while preserving the same two-index atomic semantics. See `PERFORMANCE.md` for the benchmark matrix, durability experiment, writer contention tests and profiling commands.

## V5.17 write/transaction performance

V5.17 focuses on the write path identified after the V5.16 integrity work.
The main optimization is explicit high-level batch APIs so callers can group
many mutations into one managed bbolt write transaction. This avoids repeated
transaction acquisition and commit/fsync overhead when an application already
has a batch of changes.

Added APIs:

```go
db.HSetBatch(name, []udb.Entry{...})
db.ZSetBatch(name, []udb.ZEntry{...})
db.HDelBatch(name, keys)
db.ZDelBatch(name, keys)
```

Batch operations are atomic at the bbolt transaction boundary. Empty batches
are no-ops. Validation is performed before mutation for HSetBatch/ZSetBatch.
No storage format or recovery semantics changed.

The V5.17 benchmark suite compares high-level single writes with 100-item
batches and compares the new batch APIs with the existing transaction-level
Hmset/Zmset primitives. It also keeps parallel single-write benchmarks to
watch for concurrency regressions.

## V5.16 performance

V5.16 continues the V5.15 profile-driven optimization of ZSet integrity
checking. `CheckIntegrity` removes the redundant `secondaryOrder` allocation,
uses a compact `map[string][]byte`, pre-sizes the map, and performs a final
sequential scan for orphan indexes. Member lookup keys use a private
transaction-scoped zero-copy string view. No public API or storage format
changes.

The zero-copy conversion is used only while the bbolt transaction is alive; it
is never exposed by the public API.

## V5.15 performance

V5.15 applies profile-driven optimization to ZSet integrity verification and scanning. `CheckIntegrity` no longer performs one secondary B-tree seek per primary member, and `Zscan` uses an arena to reduce per-result allocations. An internal transaction-scoped `zscanEach` path is available for synchronous zero-copy consumers.

# UDB V5.10.1


## V5.10.1 crash-recovery matrix fix

V5.10.1 is a maintenance correction for the V5.10 recovery test suite.

- Fixed `recovery_matrix_v510_test.go` to correctly consume both return values from `CheckIntegrity()`.
- The call now uses `if _, err := recovered.CheckIntegrity(); err != nil`.
- No production recovery behavior was changed.
- This release preserves the complete V5.10 process-death recovery matrix and integrity verification coverage.

UDB is a small Go embedded database wrapper built on `go.etcd.io/bbolt`, exposing Hash and ZSet primitives while keeping transaction ownership inside UDB.


## Recovery safety (V5.6.2)

Startup recovery follows a fail-closed rule:

1. A valid formal database always wins over stale/corrupt recovery metadata.
2. A valid journal is authoritative for choosing its compact temp or backup.
3. If the journal is missing/corrupt, only a single unambiguous valid artifact class may be promoted.
4. If both a valid compact temp and a valid backup exist without a usable journal, `Open` returns an error instead of guessing and risking silent rollback.
5. Every promoted candidate is fully checked by bbolt before recovery succeeds.

This deliberately favors data safety over availability when recovery metadata cannot establish ordering.

## V5.6 focus

V5.6 upgrades compaction recovery from **process-alive rollback** to a **crash-consistency protocol**:

- recovery journal uses write + `fsync` + atomic rename + directory synchronization on Unix;
- compacted database is explicitly synchronized before replacement;
- every destructive rename/remove boundary is followed by directory synchronization on Unix;
- startup recovery tolerates a partially written/corrupt journal when the formal database is already valid;
- if the formal database is missing or invalid, recovery can scan validated compact-temp and backup artifacts when the journal is unavailable or unusable;
- recovery never promotes an artifact unless a complete bbolt integrity check succeeds;
- existing invalid formal files are removed before promotion, which also makes replacement recovery work on platforms whose rename does not overwrite an existing file;
- process-death tests exercise every compaction boundary with both backup enabled and disabled;
- repeated startup recovery is tested for idempotence.

The design remains intentionally conservative: **a valid formal database always wins; otherwise only a fully checked recovery candidate may be promoted**.

## Fault injection

Production configurations should leave `MaintenanceConfig.FaultInjector` nil. Tests may inject an error at:

- `FaultAfterCompact`
- `FaultBeforeBackup`
- `FaultAfterBackup`
- `FaultBeforeReplace`
- `FaultAfterReplace`
- `FaultBeforeReopen`
- `FaultAfterReopen`
- `FaultAfterCheck`

Example:

```go
cfg := db.Options().Maintenance
cfg.FaultInjector = func(point udb.CompactFaultPoint) error {
    if point == udb.FaultBeforeReplace {
        return errors.New("synthetic failure")
    }
    return nil
}
_, _, err := db.CompactAndReplace(cfg)
```

## Startup recovery policy

At startup, UDB first checks whether the formal database is already valid.

1. **Formal DB valid** → keep it; stale/corrupt recovery journal cannot block startup.
2. **Formal DB invalid/missing + valid journal** → prefer the journal's valid compact temp, then its valid backup.
3. **Journal missing/corrupt/partial** → scan only compaction/backup filename patterns and consider candidates in newest-first order.
4. **Every candidate is integrity-checked** before promotion.
5. **No valid candidate** → opening fails rather than guessing.

This policy favors **data integrity over availability**.

## Durability boundary

V5.6 distinguishes three levels:

- normal bbolt transaction durability;
- UDB compaction state durability, where journal and directory-entry changes are explicitly synchronized;
- actual hardware/power-loss durability, which still depends on the operating system, filesystem, storage device, and their guarantees.

The process-death tests therefore validate recovery semantics after `os.Exit`, while not claiming to emulate a physical power cut.

## Testing

Normal tests:

```bash
go test ./...
```

Race detector:

```bash
go test -race ./...
```

Targeted V5.6 crash/recovery tests:

```bash
go test -run 'TestV56ProcessDeathAtCompactionBoundaries|TestV56CorruptJournalDoesNotBlockValidDB|TestV56RecoverWithoutUsableJournalFromNewestTemp|TestV56RepeatedRecoveryIsIdempotent' -count=1
```

The pressure tests that deliberately use `NoSync=true` test concurrency/index consistency rather than crash durability. Persistence/recovery tests use the normal durable default.

## bbolt version

```go
require go.etcd.io/bbolt v1.5.0
```

All source files import it as:

```go
import bolt "go.etcd.io/bbolt"
```

## Integrity & self-healing (V5.7)

UDB V5.7.1 separates structural database validation from logical data-structure validation:

- `Check()` validates the underlying bbolt structure.
- `CheckIntegrity()` validates UDB Hash/ZSet logical consistency without modifying data.
- `RepairIntegrity()` repairs ZSet secondary indexes from the primary `member -> score` map.

A repair is deliberately conservative. Malformed primary score values are reported and cause repair to fail unless the caller explicitly enables:

```go
report, err := db.RepairIntegrity(udb.RepairOptions{
    DropInvalidScores: true,
})
```

For compaction, `MaintenanceConfig.IntegrityAfterCompact` is enabled by default. `IntegrityBeforeCompact` can also be enabled when a full logical validation before compaction is desired.

The design principle is: **detect automatically, repair deterministically, and never silently discard application data.**

## V5.8 Recovery Manifest

V5.8 在 V5.7 完整性检查基础上增加了 durable Recovery Manifest（恢复清单），与原有 recovery journal 并存：

- 每次 `CompactAndReplace` 创建唯一 operation ID。
- 恢复清单使用 write + fsync + atomic rename + directory fsync 持久化。
- compact 临时数据库和 backup 会记录 SHA-256 摘要。
- 启动恢复优先使用有效的 Recovery Manifest；正式数据库文件仍然拥有最高优先级。
- manifest 指向的恢复文件如果 checksum 不匹配，默认 fail-closed，不再根据文件名或 mtime 猜测恢复对象。
- manifest 损坏/缺失时仍兼容 V5.6 的 recovery journal 与 artifact scan 策略。
- 成功恢复或成功完成 compact 后，manifest 与旧 journal 一起清理。

设计目标仍然是：**宁可拒绝启动，也不因为恢复阶段的不确定性静默回滚用户数据。**

## V5.9 Recovery State Audit

V5.9 adds a non-destructive recovery diagnostic API:

```go
report, err := udb.InspectRecovery(path, opts)
```

`InspectRecovery` never promotes, deletes, renames, or mutates recovery artifacts. It reports:

- whether the formal database exists and passes bbolt integrity checks;
- whether the legacy recovery journal exists and is structurally valid;
- whether the V5.8 recovery manifest exists and is valid;
- every discovered compact/backup artifact and its read-only bbolt validity;
- SHA-256 for valid artifacts;
- the deterministic recovery decision: `clean`, `manifest`, `journal`, `single_artifact`, `fresh_database`, or `fail_closed`.

This is useful for startup diagnostics, monitoring, incident analysis, and testing crash-recovery state matrices without modifying the database. The recovery policy remains conservative: a valid formal database always wins, and ambiguous recovery states fail closed.

## V5.10 Crash-Recovery Verification Matrix

V5.10 turns the compaction fault hooks into an explicit process-death verification matrix. Every destructive boundary is exercised in a child process that terminates with `os.Exit`, so deferred cleanup does not run.

The matrix covers:

- `FaultAfterCompact`
- `FaultBeforeBackup`
- `FaultAfterBackup`
- `FaultBeforeReplace`
- `FaultAfterReplace`
- `FaultBeforeReopen`
- `FaultAfterReopen`
- `FaultAfterCheck`

Each point is tested with both `KeepBackup=false` and `KeepBackup=true`. Before the recovery process starts, `InspectRecovery()` audits the persisted state without modifying any artifact. After `Open()`, tests verify:

1. the logical snapshot is unchanged;
2. bbolt structural integrity passes;
3. UDB logical integrity passes;
4. recovery Journal and Manifest are cleaned up;
5. backups obey the `KeepBackup` policy.

This establishes an important invariant: **after a process death at any tested compaction boundary, startup either has deterministic recovery evidence or fails closed; a successful recovery must produce the same logical database state and pass both structural and logical integrity checks.**

Targeted test:

```bash
go test -run 'TestV510' -count=1
```

## V5.11.1 Property-Based / Fuzz Testing

V5.11.1 adds a test-only verification layer rather than changing the database API:

- randomized Hash/ZSet operation sequences are checked against an independent reference model;
- ZSet secondary-index corruption is injected and repaired repeatedly;
- repair is followed by compact-and-replace and another integrity check;
- `FuzzV511RandomOperationStream` accepts arbitrary byte streams and exercises the same logical operations;
- fuzz cases are bounded to 300 operations and 64 MiB of database growth.

Run the deterministic property tests with:

```bash
go test -run 'TestV511Property' -count=1
```

Run the fuzz target briefly with:

```bash
go test -run '^$' -fuzz FuzzV511RandomOperationStream -fuzztime=30s
```

## V5.14 performance profiling

V5.14 adds stable `BenchmarkV514*` benchmark names and profiling instructions
in `PERFORMANCE.md`. The profiling benchmarks reuse the established workloads
and do not alter the database implementation or persistence semantics.

## V5.19 WritePipeline

For applications with many concurrent writers, use `WritePipeline` to amortize
bbolt's durable transaction/commit cost:

```go
pipeline, err := db.NewWritePipeline(udb.WritePipelineOptions{
    MaxBatchSize: 100,
    MaxWait:      5 * time.Millisecond,
    QueueSize:    1024,
})
if err != nil {
    return err
}
defer pipeline.Close()

if err := pipeline.HSet("users", []byte("42"), []byte("alice")); err != nil {
    return err
}
if err := pipeline.ZSet("leaderboard", []byte("42"), 100); err != nil {
    return err
}
if err := pipeline.Flush(); err != nil {
    return err
}
```

`HSet`/`ZSet` are synchronous from the caller's perspective, but concurrent
callers share the bounded queue and are batched by one writer transaction.
`Flush` is an ordered barrier. `Close` rejects new requests and drains all
already accepted requests. V5.19 deliberately does not coalesce or reorder
writes.

> Shutdown order: applications should call `WritePipeline.Close()` before
> `DB.Close()`. The pipeline owns a writer goroutine and drains its accepted
> requests before returning from `Close()`.

## V5.20 Async WritePipeline

V5.20 keeps the V5.19 synchronous pipeline APIs and adds asynchronous submission:

```go
p, err := db.NewWritePipeline(WritePipelineOptions{
    MaxBatchSize: 100,
    MaxWait:      5 * time.Millisecond,
    QueueSize:    1024,
})
if err != nil { /* handle */ }
defer p.Close()

futures := make([]*WriteFuture, 0, 100)
for i := 0; i < 100; i++ {
    f, err := p.HSetAsync("users", []byte(fmt.Sprintf("u-%d", i)), []byte("ok"))
    if err != nil { /* handle */ }
    futures = append(futures, f)
}
for _, f := range futures {
    if err := f.Wait(); err != nil { /* handle */ }
}
```

`HSetAsync`/`ZSetAsync`/`HDelAsync`/`ZDelAsync` return after the request is accepted by the bounded queue. `WriteFuture.Wait()` waits for the actual transaction result. `WaitContext` can stop waiting without cancelling an already accepted write.

The pipeline still executes requests in submission order and does not coalesce or reorder operations. A bbolt transaction is the atomicity unit: if any operation in a batch fails, the transaction rolls back and every future in that batch receives the transaction error.

Applications should close the pipeline before closing the database:

```text
producers -> bounded queue -> batch collector -> single writer -> one bbolt Update
```

## V5.21 adaptive write pipeline and observability

V5.21 keeps the V5.20 asynchronous write semantics and adds runtime
observability and adaptive batching.

### Metrics

`PipelineStats` now exposes queue depth/capacity, transaction and batch counts,
total and last commit latency, min/max latency, average batch size, average
commit latency and an approximate committed throughput. Metrics are snapshots;
they do not affect ordering or transaction semantics.

### Batch policy

The optional `BatchPolicy` interface selects a target batch size for each new
transaction. `FixedBatchPolicy` provides deterministic behavior. The
`AdaptiveBatchPolicy` is a conservative feedback controller using queue
pressure and observed commit latency. It is bounded by `MinBatchSize` and
`MaxBatchSize` and is capped by `WritePipelineOptions.MaxBatchSize`.

Example:

```go
policy := udb.DefaultAdaptiveBatchPolicy()
p, err := db.NewWritePipeline(udb.WritePipelineOptions{
    MaxBatchSize: 1000,
    MaxWait: 5 * time.Millisecond,
    QueueSize: 4096,
    BatchPolicy: policy,
})
```

### Backpressure

`WritePipelineOptions.Backpressure` supports:

- `BackpressureBlock`: wait for queue admission; this is the default and keeps
  V5.20 behavior.
- `BackpressureReject`: return `ErrWritePipelineFull` when the queue is full.
- `BackpressureTimeout`: wait according to the caller's context and return its
  error if admission cannot complete before the deadline.

### Compatibility guarantees

V5.21 does not introduce operation coalescing or reordering. Submission order,
`Flush` barriers, `Close` drain behavior, Future completion, input-buffer
copying, and batch-level transaction atomicity remain unchanged.

## V5.22 — Transaction / Batch / Snapshot / Metrics

V5.22 adds a unified transaction layer while preserving the V5.21 API and semantics.

### Atomic transaction helpers

```go
err := db.Atomic(func(tx *Tx) error {
    if err := db.Hset(tx, "user", []byte("name"), []byte("alice")); err != nil {
        return err
    }
    return db.Zset(tx, "rank", []byte("alice"), 100)
})
```

`Atomic` is an explicit name for one managed write transaction. `ReadTransaction`
is the corresponding explicit read-side helper. Context variants check cancellation
before transaction admission and between Batch operations; they do not forcibly abort
a bbolt transaction that is already executing.

### Transaction Batch builder

```go
batch, err := db.NewBatch()
if err != nil { return err }
_ = batch.HSet("user", []byte("name"), []byte("alice"))
_ = batch.ZSet("rank", []byte("alice"), 100)
if err := batch.Commit(); err != nil { return err }
```

`Batch` executes HSet/HDel/ZSet/ZDel in one managed write transaction, in submission
order. Inputs are copied when added. `Commit` is one-shot; `Abort` discards pending
operations. The builder itself is intentionally not safe for concurrent mutation.

### Read Snapshot

```go
snap, err := db.Snapshot()
if err != nil { return err }
defer snap.Close()

r := snap.HGet("user", []byte("name"))
```

A Snapshot materializes the public Hash/ZSet state during one managed read
transaction and releases that transaction before returning. Snapshot methods are
safe for concurrent readers and `Close` is idempotent. A long-lived Snapshot does
not occupy a source bbolt read transaction and therefore cannot block source
writes, mmap growth, compaction, or Close. The trade-off is memory proportional to
the logical snapshot size.

### Database metrics

```go
m := db.Metrics()
```

V5.22 exposes transaction-level View/Update counts, errors and cumulative latency,
plus snapshot lifecycle counters and Batch commit/item counters. `ResetMetrics`
resets these database-level counters without touching persisted data. WritePipeline
metrics remain available separately through `pipeline.Stats()`.

### V5.22.1 Snapshot 修复

V5.22.1 修复了长期持有 bbolt read transaction 导致源数据库写事务在 mmap 扩容阶段长期等待的问题。

Snapshot 现在采用独立快照文件：创建 Snapshot 时仅短暂打开一次源数据库只读事务，并通过 bbolt 的一致性复制生成独立数据库；复制完成后立即释放源事务。之后 Snapshot 的读取均在自己的只读数据库上执行。

因此，一个长期存活的 Snapshot 不再阻塞源数据库的：

- HSet/ZSet 等写事务
- mmap 扩容
- CompactAndReplace
- DB.Close

Snapshot 仍保持创建瞬间的一致性视图，Snapshot.Close 幂等，并负责关闭及删除临时快照文件。

### V5.22.2 Snapshot deadlock fix

Snapshot now materializes the public Hash/ZSet state into memory while one managed read transaction is active. The source transaction is released before Snapshot returns. This avoids bbolt mmap/nested-transaction deadlocks while preserving point-in-time reads. The trade-off is memory usage proportional to the logical snapshot size.

### V5.23 — Unified write core and deeper observability

V5.23 unifies the internal mutation engine used by `Batch`, `WritePipeline`, and the
public batch helpers (`HSetBatch`, `ZSetBatch`, `HDelBatch`, `ZDelBatch`). All paths
now share the same ordered Hash/ZSet mutation semantics and per-transaction bucket
cache. This reduces implementation drift while preserving atomic rollback.

`PipelineStats` now also reports queue-wait latency:

- `TotalQueueWaitLatency`
- `LastQueueWaitLatency`
- `MaxQueueWaitLatency`
- `AvgQueueWaitLatency`

Database metrics now include logical Snapshot size and capture duration:

- `SnapshotBytes` / `SnapshotMaxBytes`
- `SnapshotDuration`
- `AvgSnapshotBytes`
- `AvgSnapshotDuration`

These metrics describe the cost of creating an in-memory logical Snapshot; they do
not represent the physical bbolt file size. `ResetMetrics` resets these cumulative
Snapshot metrics as well.
