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

