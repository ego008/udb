package udb

import (
	"bytes"
	"fmt"
	"testing"
)

func makeV533Keys(n, start int) [][]byte {
	keys := make([][]byte, n)
	for i := range keys {
		keys[i] = []byte(fmt.Sprintf("k-%08d", start+i))
	}
	return keys
}

func setupV533BenchDB(b *testing.B, n int) *DB {
	b.Helper()
	db := openV57TestDB(b)
	for i := 0; i < n; i++ {
		if err := db.HSet("h", []byte(fmt.Sprintf("k-%08d", i)), []byte(fmt.Sprintf("v-%08d", i))); err != nil {
			b.Fatal(err)
		}
	}
	return db
}

func BenchmarkV533PlanCostModel(b *testing.B) {
	opts := defaultReadPlannerOptions()
	keys := makeV533Keys(100, 450)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = planReadKeysFast(keys, opts)
	}
}

func BenchmarkV533PlanCostThresholds(b *testing.B) {
	for _, n := range []int{1, 2, 4, 8, 10, 16, 32, 64, 100, 256, 1000} {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			keys := makeV533Keys(n, 450)
			opts := defaultReadPlannerOptions()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = planReadKeysFast(keys, opts)
			}
		})
	}
}

func BenchmarkV533PointVsSeek(b *testing.B) {
	for _, n := range []int{4, 8, 10, 16, 32, 64, 100, 256, 1000} {
		b.Run(fmt.Sprintf("Point/n=%d", n), func(b *testing.B) {
			db := setupV533BenchDB(b, 2000)
			defer db.Close()
			keys := makeV533Keys(n, 450)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := db.View(func(tx *Tx) error {
					bucket := tx.bucket(bucketName(hashPrefix, "h"))
					for _, key := range keys {
						_ = bucket.Get(key)
					}
					return nil
				}); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(fmt.Sprintf("Seek/n=%d", n), func(b *testing.B) {
			db := setupV533BenchDB(b, 2000)
			defer db.Close()
			keys := makeV533Keys(n, 450)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := db.View(func(tx *Tx) error {
					bucket := tx.bucket(bucketName(hashPrefix, "h"))
					c := bucket.Cursor()
					k, v := c.Seek(keys[0])
					for _, key := range keys {
						for k != nil && bytes.Compare(k, key) < 0 {
							k, v = c.Next()
						}
						if k != nil && bytes.Equal(k, key) {
							_ = v
						}
					}
					return nil
				}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkV533HGetManySorted(b *testing.B) {
	for _, n := range []int{10, 16, 32, 64, 100, 256, 1000} {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			db := setupV533BenchDB(b, 2000)
			defer db.Close()
			keys := makeV533Keys(n, 450)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := db.HGetMany("h", keys, func(_ int, _ []byte, _ []byte, _ bool) error { return nil }); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
