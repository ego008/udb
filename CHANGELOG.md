# Changelog

## V5.5

### Fault injection and recovery

- Added deterministic `CompactFaultPoint` / `CompactFaultInjector` hooks for reliability testing.
- Added a recovery journal for `CompactAndReplace` destructive stages.
- Added startup recovery for interrupted compact-and-replace operations.
- Recovery prefers a valid formal database, then a valid compacted temp file, then a valid backup.
- Added rollback handling for failures before/after backup, replacement, reopen, and post-check stages.
- Temporary compact files and recovery journals are cleaned after successful recovery.
- `CompactTo` now explicitly rejects an existing directory destination instead of relying on bbolt/OS behavior.
- Added V5.5 fault-injection and startup-recovery tests covering Hash, ZSet, `Check`, `CheckZSet`, and post-failure writes.

## V5.5

- Added logical snapshot compaction tests.
- Added repeated compact idempotence tests.
- Added backup/rollback validation.
- Added ZSet corruption detection and repair tests.
- Added mixed Hash/ZSet pressure tests and Compact/Close lifecycle race tests.
- Added close/reopen persistence tests.

## V5.5.1
- Fixed Go compile error caused by comparing `MaintenanceConfig` as a whole after adding the function-valued `CompactFaultInjector` field.
- Replaced whole-struct zero comparison with an explicit zero-value predicate that safely handles the function field.
