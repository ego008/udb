# UDB V5.6

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
