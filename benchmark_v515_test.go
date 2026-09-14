package udb

import "testing"

// V5.15 benchmarks keep stable names while exercising the optimized
// production paths. The underlying fixtures remain the same as V5.12/V5.14
// so results can be compared with the historical baseline.
func BenchmarkV515ZScan(b *testing.B) {
	BenchmarkV512ZScanRange(b)
}

func BenchmarkV515ZScanParallel(b *testing.B) {
	BenchmarkV512ZScanRangeParallel(b)
}

func BenchmarkV515CheckIntegrity(b *testing.B) {
	BenchmarkV512CheckIntegrity(b)
}

func BenchmarkV515RepairIntegrity(b *testing.B) {
	BenchmarkV512RepairIntegrity(b)
}

func BenchmarkV515ZScanEach(b *testing.B) {
	db := v512Open(b)
	defer db.Close()
	v512Populate(b, db, v512FixtureN)
	start := I2b(0)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := db.View(func(tx *Tx) error {
			return db.zscanEach(tx, v512FixtureZSet, nil, start, 100, false, func(member []byte, score uint64) error {
				if len(member) == 0 || score == ^uint64(0) {
					b.Fatal("unexpected zset entry")
				}
				return nil
			})
		}); err != nil {
			b.Fatal(err)
		}
	}
}
