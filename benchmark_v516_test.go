package udb

import "testing"

// V5.16 benchmarks keep the V5.12 fixture and workload stable so the effect
// of the integrity-memory optimization can be compared directly with V5.15.
func BenchmarkV516ZScan(b *testing.B) {
	BenchmarkV512ZScanRange(b)
}

func BenchmarkV516ZScanParallel(b *testing.B) {
	BenchmarkV512ZScanRangeParallel(b)
}

func BenchmarkV516CheckIntegrity(b *testing.B) {
	BenchmarkV512CheckIntegrity(b)
}

func BenchmarkV516RepairIntegrity(b *testing.B) {
	BenchmarkV512RepairIntegrity(b)
}

func BenchmarkV516ZScanEach(b *testing.B) {
	BenchmarkV515ZScanEach(b)
}
