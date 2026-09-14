# Changelog

## V5.6.2

- Strengthened crash-recovery fallback when the recovery journal is missing or corrupt.
- A valid formal database remains authoritative whenever it exists.
- Journal-less recovery now refuses to guess when both a valid compact temporary file and a valid backup exist; this prevents silent rollback/data loss.
- Added recovery state-matrix tests for ambiguous artifacts, corrupt journals, single valid artifact recovery, and formal-database precedence.
- Preserved Hash/ZSet logical snapshot and bbolt integrity checks across recovery paths.

## V5.6

### Crash consistency and recovery

- Added durability-aware recovery journal persistence: write, file sync, atomic rename, and Unix directory sync.
- Added explicit synchronization of the compacted database before replacement.
- Added directory synchronization around destructive compaction renames/removals on Unix.
- Startup recovery now tolerates corrupt or partially written journals when the formal database is already valid.
- Added fallback artifact scanning when the journal is missing/unusable and the formal database is unavailable.
- Recovery candidates are always validated with a complete bbolt integrity check before promotion.
- Added cross-platform recovery handling for an existing invalid formal file when rename cannot overwrite it directly.
- Added process-death tests for all eight compaction fault boundaries, with and without backups.
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

## V5.6.3

- Fixed journal-less recovery artifact discovery to recognize both `.compact.<suffix>` and `.compact-<suffix>` temporary artifact names.
- Tightened journal-less recovery: if more than one fully valid recovery artifact exists, recovery now fails closed instead of selecting by mtime.
- Preserved the rule that a valid formal database always wins over stale/corrupt recovery artifacts.
