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
