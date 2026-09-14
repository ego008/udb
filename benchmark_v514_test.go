package udb

import "testing"

// V5.14 benchmarks are intentionally thin wrappers around the stable V5.12
// benchmark fixtures. Their purpose is to provide stable benchmark names for
// CPU, heap, mutex, and block profiling without changing the workload used by
// the historical V5.12/V5.13 baseline.
//
// Keep fixture construction outside the timed region wherever possible. Do
// not add logging or extra validation to these wrappers: pprof should observe
// the same production path that the normal benchmark observes.

func BenchmarkV514HashSet(b *testing.B) {
	BenchmarkV512HashSetSingle(b)
}

func BenchmarkV514HashGet(b *testing.B) {
	BenchmarkV512HashGetSingle(b)
}

func BenchmarkV514HashBatchSet(b *testing.B) {
	BenchmarkV512HashBatchSet(b)
}

func BenchmarkV514HashGetParallel(b *testing.B) {
	BenchmarkV512HashGetParallel(b)
}

func BenchmarkV514HashUpdateParallel(b *testing.B) {
	BenchmarkV512HashUpdateParallel(b)
}

func BenchmarkV514ZSet(b *testing.B) {
	BenchmarkV512ZSetSingle(b)
}

func BenchmarkV514ZScore(b *testing.B) {
	BenchmarkV512ZScoreSingle(b)
}

func BenchmarkV514ZScan(b *testing.B) {
	BenchmarkV512ZScanRange(b)
}

func BenchmarkV514ZScanParallel(b *testing.B) {
	BenchmarkV512ZScanRangeParallel(b)
}

func BenchmarkV514Compact(b *testing.B) {
	BenchmarkV512CompactAndReplace(b)
}

func BenchmarkV514CheckIntegrity(b *testing.B) {
	BenchmarkV512CheckIntegrity(b)
}

func BenchmarkV514RepairIntegrity(b *testing.B) {
	BenchmarkV512RepairIntegrity(b)
}
