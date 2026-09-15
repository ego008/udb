package udb

import (
	"fmt"
	"testing"
)

func benchmarkV529HGetMany(b *testing.B, n int, sorted bool) {
	db := openV57TestDB(b)
	defer db.Close()
	for i := 0; i < n*2; i++ {
		_ = db.HSet("h", []byte(fmt.Sprintf("k-%06d", i)), []byte("value"))
	}
	keys := make([][]byte, n)
	for i := range keys {
		idx := i
		if !sorted {
			idx = n - 1 - i
		}
		keys[i] = []byte(fmt.Sprintf("k-%06d", idx))
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := db.HGetMany("h", keys, func(int, []byte, []byte, bool) error { return nil }); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkV529HGetMany10(b *testing.B)         { benchmarkV529HGetMany(b, 10, true) }
func BenchmarkV529HGetMany32(b *testing.B)         { benchmarkV529HGetMany(b, 32, true) }
func BenchmarkV529HGetMany100Sorted(b *testing.B)  { benchmarkV529HGetMany(b, 100, true) }
func BenchmarkV529HGetMany100Random(b *testing.B)  { benchmarkV529HGetMany(b, 100, false) }
func BenchmarkV529HGetMany1000Sorted(b *testing.B) { benchmarkV529HGetMany(b, 1000, true) }

func BenchmarkV529PlanReadKeys100(b *testing.B) {
	keys := make([][]byte, 100)
	for i := range keys {
		keys[i] = []byte(fmt.Sprintf("user:%06d", i))
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = PlanReadKeys(keys)
	}
}

func BenchmarkV529PlanReadKeysFast100(b *testing.B) {
	keys := make([][]byte, 100)
	for i := range keys {
		keys[i] = []byte(fmt.Sprintf("user:%06d", i))
	}
	opts := defaultReadPlannerOptions()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = planReadKeysFast(keys, opts)
	}
}
