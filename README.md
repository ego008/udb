# UDB V5.5

UDB is a small Go embedded database wrapper built on `go.etcd.io/bbolt`, exposing Hash and ZSet primitives while keeping transaction ownership inside UDB.

## V5.5 focus

V5.5 adds **fault injection and crash-recovery protection** around compaction:

- managed lifecycle admission/drain for `View`, `Update`, `Close`, and `CompactAndReplace`;
- deterministic compaction fault points for tests;
- a recovery journal written before destructive replacement stages;
- automatic rollback while the process is alive;
- conservative startup recovery after an interrupted replacement;
- validation of formal DB, compacted temp DB, and backup DB before promotion;
- cleanup of temporary files and recovery journals after successful recovery;
- explicit rejection of a directory passed to `CompactTo`;
- full logical Hash/ZSet snapshot verification after injected failures.

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

When `<db>.compact-recovery.json` exists at startup:

1. If the formal database is valid, keep it and remove stale recovery artifacts.
2. Otherwise, if the compacted temp file is valid, promote it.
3. Otherwise, if the backup is valid, restore it.
4. If none is valid, opening fails rather than guessing or promoting a corrupt file.

This policy intentionally favors **data integrity over availability**.

## Testing

Normal tests:

```bash
go test ./...
```

Race detector:

```bash
go test -race ./...
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
