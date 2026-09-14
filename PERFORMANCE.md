## V5.16 CheckIntegrity memory optimization

V5.15 removed the repeated secondary B-tree searches but retained two O(N)
secondary-side structures: a `map[string]secondaryEntry` and `secondaryOrder`.
The latter existed only to preserve deterministic orphan reporting. V5.16 removes
`secondaryOrder`, reports remaining orphans with a final sequential secondary
cursor scan, stores only the secondary score in the map, pre-sizes the map, and
uses a private transaction-scoped zero-copy string view for member lookup keys.

The zero-copy string helper is deliberately private and is never allowed to
escape the active bbolt transaction. Public APIs continue to copy data where
ownership requires it.

Stable benchmarks:

```text
BenchmarkV516ZScan
BenchmarkV516ZScanParallel
BenchmarkV516CheckIntegrity
BenchmarkV516RepairIntegrity
BenchmarkV516ZScanEach
```

Compare V5.15 and V5.16 with the same machine, Go version, bbolt version and
fixture size. The primary acceptance criterion is lower `B/op` and `allocs/op`
for `BenchmarkV516CheckIntegrity` without regressions in integrity semantics.

## V5.15 profile-driven changes

The V5.14 profiles identified two dominant production costs:

- `CheckIntegrity`: repeated secondary-index `Cursor.Seek` calls dominated CPU and allocation samples. V5.15 replaces those lookups with one primary scan plus one secondary scan and a transaction-scoped member map.
- `Zscan`: result copying and `cloneBytes` dominated allocation samples. V5.15 uses an append arena for the public API and provides internal `zscanEach` for zero-copy transaction-scoped consumers.

Re-run the stable benchmarks after building V5.15:

```bash
go test -run '^$' -bench '^BenchmarkV515' -benchmem -count=5
```

CPU profile:

```bash
go test -run '^$' -bench '^BenchmarkV515CheckIntegrity$' -benchmem -cpuprofile cpu-v515-integrity.out -benchtime=5s
go tool pprof -top cpu-v515-integrity.out
```

Heap profile:

```bash
go test -run '^$' -bench '^BenchmarkV515CheckIntegrity$' -benchmem -memprofile mem-v515-integrity.out -benchtime=5s
go tool pprof -top -alloc_space mem-v515-integrity.out
```

# UDB Performance Engineering

## V5.13 baseline

V5.12 established the first benchmark baseline. V5.13 focuses on allocation and cursor overhead without changing the persistence/recovery model.

Run the full benchmark suite:

```bash
go test -run '^$' -bench 'BenchmarkV512' -benchmem -count=5
```

Run allocation-sensitive benchmarks:

```bash
go test -run '^$' -bench 'BenchmarkV512HashGetSingle|BenchmarkV512ZScoreSingle|BenchmarkV512ZScanRange|BenchmarkV512CheckIntegrity|BenchmarkV512RepairIntegrity' -benchmem -count=5
```

## Profiling

CPU:

```bash
go test -run '^$' -bench 'BenchmarkV512ZScanRange|BenchmarkV512CheckIntegrity' -benchmem -cpuprofile cpu.out
 go tool pprof -http=:0 cpu.out
```

Memory:

```bash
go test -run '^$' -bench 'BenchmarkV512ZScanRange|BenchmarkV512CheckIntegrity' -benchmem -memprofile mem.out
 go tool pprof -http=:0 mem.out
```

Mutex contention:

```bash
go test -run '^$' -bench 'BenchmarkV512HashGetParallel|BenchmarkV512HashUpdateParallel|BenchmarkV512ZScanRangeParallel' -benchmem -mutexprofile mutex.out
 go tool pprof -http=:0 mutex.out
```

Blocking profile:

```bash
go test -run '^$' -bench 'BenchmarkV512HashGetParallel|BenchmarkV512HashUpdateParallel|BenchmarkV512ZScanRangeParallel' -benchmem -blockprofile block.out
 go tool pprof -http=:0 block.out
```

## V5.13 optimization principles

1. Keep bbolt mmap-backed values private to the transaction.
2. Optimize allocations only where ownership can remain explicit.
3. Do not introduce a global lock merely to improve a single benchmark.
4. Do not use mtime or heuristic recovery decisions.
5. Keep `CheckIntegrity` read-only.
6. Keep `RepairIntegrity` fail-closed for malformed scores unless the caller explicitly opts into dropping them.
7. Every performance change must pass normal tests, race tests, property tests and fuzz regression seeds.

## Comparing versions

For meaningful before/after comparisons, keep the following constant:

- OS and architecture
- Go version
- bbolt version
- database fixture size
- key/value sizes
- benchmark command and `-count`

Use `benchstat` when available:

```bash
go test -run '^$' -bench 'BenchmarkV512' -benchmem -count=10 > v512.txt
go test -run '^$' -bench 'BenchmarkV512' -benchmem -count=10 > v513.txt
benchstat v512.txt v513.txt
```

## V5.14 profiling baseline

V5.14 does not intentionally change the storage or recovery algorithms. It
adds stable `BenchmarkV514*` names so profiling commands can target one
operation without depending on the historical V5.12 benchmark names.

The V5.14 wrappers reuse the exact V5.12/V5.13 workloads, so results can be
compared directly with the previous baseline.

### CPU profile

For a focused CPU profile:

```bash
go test -run '^$' -bench '^BenchmarkV514CheckIntegrity$' -benchmem -cpuprofile cpu-check.out -benchtime=5s
 go tool pprof -top cpu-check.out
```

Interactive web UI:

```bash
go tool pprof -http=:0 cpu-check.out
```

Recommended CPU targets:

```bash
go test -run '^$' -bench '^BenchmarkV514ZScan$' -benchmem -cpuprofile cpu-zscan.out -benchtime=5s
go test -run '^$' -bench '^BenchmarkV514CheckIntegrity$' -benchmem -cpuprofile cpu-integrity.out -benchtime=5s
go test -run '^$' -bench '^BenchmarkV514RepairIntegrity$' -benchmem -cpuprofile cpu-repair.out -benchtime=5s
go test -run '^$' -bench '^BenchmarkV514Compact$' -benchmem -cpuprofile cpu-compact.out -benchtime=3x
```

### Heap / allocation profile

```bash
go test -run '^$' -bench '^BenchmarkV514ZScan$' -benchmem -memprofile mem-zscan.out -benchtime=5s
go tool pprof -top -alloc_space mem-zscan.out
```

Use `-inuse_space` when the question is retained heap rather than cumulative
allocation:

```bash
go tool pprof -top -inuse_space mem-zscan.out
```

### Mutex contention

Mutex profiling is useful for the parallel read/write paths:

```bash
go test -run '^$' -bench '^BenchmarkV514HashGetParallel$' -benchmem -mutexprofile mutex-hget.out -benchtime=5s
go test -run '^$' -bench '^BenchmarkV514HashUpdateParallel$' -benchmem -mutexprofile mutex-hupdate.out -benchtime=5s
go test -run '^$' -bench '^BenchmarkV514ZScanParallel$' -benchmem -mutexprofile mutex-zscan.out -benchtime=5s
```

Then inspect:

```bash
go tool pprof -top mutex-hget.out
go tool pprof -top mutex-hupdate.out
go tool pprof -top mutex-zscan.out
```

### Blocking profile

```bash
go test -run '^$' -bench '^BenchmarkV514HashGetParallel$' -benchmem -blockprofile block-hget.out -benchtime=5s
go test -run '^$' -bench '^BenchmarkV514HashUpdateParallel$' -benchmem -blockprofile block-hupdate.out -benchtime=5s
go test -run '^$' -bench '^BenchmarkV514ZScanParallel$' -benchmem -blockprofile block-zscan.out -benchtime=5s
```

### Scheduler / execution trace

When a profile shows unexpected goroutine scheduling or blocking overhead,
collect a trace from a short benchmark run:

```bash
go test -run '^$' -bench '^BenchmarkV514HashGetParallel$' -benchtime=3s -trace trace-hget.out
go tool trace trace-hget.out
```

### Stable comparison commands

Run V5.14 with the same machine, Go version, bbolt version, fixture sizes and
benchmark count used for the historical baseline:

```bash
go test -run '^$' -bench 'BenchmarkV514' -benchmem -count=5 > v514.txt
go test -run '^$' -bench 'BenchmarkV512' -benchmem -count=5 > v512.txt
benchstat v512.txt v514.txt
```

For the V5.13.1 source tree, use the V5.12 benchmark names as before. V5.14's
stable names are only a profiling/navigation improvement; they intentionally
call the same benchmark bodies.

### Profiling rules for V5.14

1. Profile one workload at a time before making an optimization.
2. Treat `bbolt`, `syscall`, `fsync`, mmap/page handling and filesystem time as
   potentially unavoidable before changing UDB code.
3. Separate CPU time from allocation volume. A reduction in `allocs/op` is not
   automatically a reduction in wall-clock latency.
4. For parallel benchmarks, inspect mutex and block profiles before adding or
   removing synchronization.
5. Do not weaken transaction ownership, lifecycle admission, recovery journal
   semantics, manifest validation, or integrity guarantees for performance.
6. Any optimization must pass `go test ./...`, `go test -race ./...`, the V5.11
   property/fuzz regression tests, and the V5.13 shared-score integrity test.
