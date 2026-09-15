package udb

import (
	"fmt"
	"testing"
)

func benchmarkV528HGetMany(b *testing.B, n int, sorted bool, hitRate float64) {
	db := openV57TestDB(b)
	defer db.Close()
	for i := 0; i < n*2; i++ {
		_ = db.HSet("h", []byte(fmt.Sprintf("k-%06d", i)), []byte("value"))
	}
	keys := make([][]byte, n)
	hits := int(float64(n) * hitRate)
	for i := 0; i < n; i++ {
		idx := i
		if !sorted {
			idx = n - 1 - i
		}
		if i >= hits {
			idx += n * 2
		}
		keys[i] = []byte(fmt.Sprintf("k-%06d", idx))
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if err := db.HGetMany("h", keys, func(int, []byte, []byte, bool) error { return nil }); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkV528HGetMany10Sorted100Hit(b *testing.B)   { benchmarkV528HGetMany(b, 10, true, 1) }
func BenchmarkV528HGetMany100Sorted100Hit(b *testing.B)  { benchmarkV528HGetMany(b, 100, true, 1) }
func BenchmarkV528HGetMany1000Sorted100Hit(b *testing.B) { benchmarkV528HGetMany(b, 1000, true, 1) }
func BenchmarkV528HGetMany100Random100Hit(b *testing.B)  { benchmarkV528HGetMany(b, 100, false, 1) }
func BenchmarkV528HGetMany100Sorted50Hit(b *testing.B)   { benchmarkV528HGetMany(b, 100, true, 0.5) }
func BenchmarkV528HGetMany100Sorted10Hit(b *testing.B)   { benchmarkV528HGetMany(b, 100, true, 0.1) }

func BenchmarkV528ZScoreMany100Sorted100Hit(b *testing.B) {
	db := openV57TestDB(b)
	defer db.Close()
	for i := 0; i < 200; i++ {
		_ = db.ZSet("z", []byte(fmt.Sprintf("k-%06d", i)), uint64(i))
	}
	keys := make([][]byte, 100)
	for i := range keys {
		keys[i] = []byte(fmt.Sprintf("k-%06d", i))
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if err := db.ZScoreMany("z", keys, func(int, []byte, uint64, bool) error { return nil }); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkV528ReadBatch100Sorted(b *testing.B) {
	db := openV57TestDB(b)
	defer db.Close()
	for i := 0; i < 200; i++ {
		_ = db.HSet("h", []byte(fmt.Sprintf("k-%06d", i)), []byte("value"))
	}
	keys := make([][]byte, 100)
	for i := range keys {
		keys[i] = []byte(fmt.Sprintf("k-%06d", i))
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		rb, _ := db.NewReadBatch()
		for _, key := range keys {
			_ = rb.HGetBorrowed("h", key)
		}
		if err := rb.Execute(func(ReadBatchResult) error { return nil }); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkV528HGetMany100Parallel(b *testing.B) {
	db := openV57TestDB(b)
	defer db.Close()
	for i := 0; i < 1000; i++ {
		_ = db.HSet("h", []byte(fmt.Sprintf("k-%06d", i)), []byte("value"))
	}
	keys := make([][]byte, 100)
	for i := range keys {
		keys[i] = []byte(fmt.Sprintf("k-%06d", i))
	}
	b.ResetTimer()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if err := db.HGetMany("h", keys, func(int, []byte, []byte, bool) error { return nil }); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkV528ReadPlanner(b *testing.B) {
	keys := make([][]byte, 100)
	for i := range keys {
		keys[i] = []byte(fmt.Sprintf("user:%06d", i))
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = PlanReadKeys(keys)
	}
}
