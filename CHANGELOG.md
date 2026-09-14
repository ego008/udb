# Changelog

## V5.10.1

### Crash-recovery matrix compile fix

- Fixed `recovery_matrix_v510_test.go` to consume both return values from `CheckIntegrity()`.
- No production code or recovery semantics were changed.
- Preserved the complete V5.10 crash-recovery verification matrix.

## V5.10

### Crash-recovery verification matrix

- Added process-death recovery matrix coverage for every destructive compaction boundary.
- Audited persisted recovery state with `InspectRecovery()` before startup recovery at each fault point.
- Verified logical snapshot preservation, bbolt structural integrity, and UDB logical integrity after recovery.
- Verified recovery Journal and Manifest are removed after successful recovery.
- Verified `KeepBackup=false` does not leave backup artifacts after successful recovery.
- Added a read-only audit regression test proving `InspectRecovery()` does not mutate ambiguous recovery artifacts.
- The recovery contract is now tested as: deterministic evidence before Open -> safe recovery or explicit failure -> integrity verification after Open.

# Changelog

## V5.9

### Recovery state audit

- Added non-destructive `InspectRecovery(path, opts)` diagnostics.
- Added explicit recovery decisions for clean, manifest, journal, single-artifact, fresh-database, and fail-closed states.
- Added read-only validation and SHA-256 reporting for discovered compact/backup artifacts.
- Added V5.9 regression tests for fresh state, formal-database precedence, ambiguous artifacts, and manifest checksum mismatch.
- Preserved the fail-closed recovery policy: no heuristic promotion is performed when durable recovery evidence is ambiguous.

## V5.7.1

### Integrity & self-healing fix

- Fixed a Go compile error in `integrity.go` caused by using the wrong `bbolt.Tx.ForEach` callback signature.
- Updated top-level bucket scans to use `func(name []byte, b *bbolt.Bucket) error`, matching bbolt v1.5.0.
- Reused the bucket returned by `ForEach` instead of performing redundant bucket lookups.
- Preserved all V5.7 integrity, repair, and compaction verification behavior.

## V5.7

### Integrity & self-healing

- Added `CheckIntegrity()` for read-only logical consistency checking of UDB Hash/ZSet structures.
- Added detailed ZSet checks for missing, orphaned, malformed, and mismatched secondary indexes.
- Added detection of an orphan ZSet key-index bucket without a score bucket.
- Added `RepairIntegrity()` with deterministic secondary-index rebuild from the primary score map.
- Invalid primary ZSet scores are preserved by default; destructive cleanup requires `RepairOptions{DropInvalidScores: true}`.
- Added integrity checks before/after compaction through `MaintenanceConfig.IntegrityBeforeCompact` and `IntegrityAfterCompact`.
- Enabled post-compaction integrity verification by default.
- Added V5.7 integrity regression tests for healthy data, missing/orphan indexes, mismatched indexes, invalid scores, and orphan buckets.

## V5.6.3

### Recovery safety

- Fixed journal-less recovery artifact discovery to recognize both `.compact.<suffix>` and `.compact-<suffix>` temporary artifact names.
- Tightened journal-less recovery: if more than one fully valid recovery artifact exists, recovery now fails closed instead of selecting by mtime.
- Preserved the rule that a valid formal database always wins over stale or corrupt recovery artifacts.
- Added recovery state-matrix coverage for ambiguous artifacts, corrupt journals, single valid artifact recovery, and formal-database precedence.

## V5.6.1

### Crash consistency and recovery

- Fixed startup recovery for a brand-new database with no formal DB, journal, or recovery artifacts.
- Startup now treats that state as a normal fresh-open case instead of reporting a recovery failure.

## V5.6

### Crash consistency and recovery

- Added durability-aware recovery journal persistence: write, file sync, atomic rename, and Unix directory sync.
- Added explicit synchronization of the compacted database before replacement.
- Added directory synchronization around destructive compaction renames/removals on Unix.
- Startup recovery now tolerates corrupt or partially written journals when the formal database is already valid.
- Added fallback artifact scanning when the journal is missing or unusable and the formal database is unavailable.
- Recovery candidates are always validated with a complete bbolt integrity check before promotion.
- Added cross-platform recovery handling for an existing invalid formal file when rename cannot overwrite it directly.
- Added process-death tests for compaction fault boundaries, with and without backups.
- Added tests for corrupt journals, journal-independent temp recovery, repeated recovery idempotence, and atomic journal replacement.

## V5.5.1

- Fixed Go compile error caused by comparing `MaintenanceConfig` as a whole after adding the function-valued `CompactFaultInjector` field.
- Replaced whole-struct zero comparison with an explicit zero-value predicate that safely handles the function field.

## V5.5

- Added deterministic `CompactFaultPoint` / `CompactFaultInjector` hooks for reliability testing.
- Added a recovery journal for `CompactAndReplace` destructive stages.
- Added startup recovery for interrupted compact-and-replace operations.
- Added rollback handling for failures before/after backup, replacement, reopen, and post-check stages.
- Added logical snapshot, backup/rollback, ZSet repair, mixed pressure, lifecycle-race, and close/reopen persistence tests.
- `CompactTo` now explicitly rejects an existing directory destination.

## V5.8

### Durable recovery manifest

- Added a durable recovery manifest alongside the existing recovery journal.
- Added per-compaction operation IDs and SHA-256 digests for compact/backup artifacts.
- Startup recovery now prefers a valid manifest and verifies the recorded artifact digest before promotion.
- A valid manifest with an unverifiable artifact fails closed instead of falling back to filename/mtime guessing.
- Preserved compatibility with V5.6 journal recovery and conservative artifact scanning when the manifest is absent or corrupt.
- Recovery manifest is atomically persisted with file and directory syncs.
- Added V5.8 recovery-manifest regression tests for journal-less recovery, checksum mismatch, and corrupt-manifest fallback.
