package udb

import (
	"fmt"
	"testing"
)

func BenchmarkV527ReadBatch100Sorted(b *testing.B) {
	db := openV57TestDB(b)
	defer db.Close()
	for i := 0; i < 100; i++ {
		_ = db.HSet("h", []byte(fmt.Sprintf("k-%03d", i)), []byte("value"))
	}
	keys := make([][]byte, 100)
	for i := range keys {
		keys[i] = []byte(fmt.Sprintf("k-%03d", i))
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		rb, _ := db.NewReadBatch()
		for _, k := range keys {
			_ = rb.HGetBorrowed("h", k)
		}
		if err := rb.Execute(func(ReadBatchResult) error { return nil }); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkV527ReadBatch100Unsorted(b *testing.B) {
	db := openV57TestDB(b)
	defer db.Close()
	for i := 0; i < 100; i++ {
		_ = db.HSet("h", []byte(fmt.Sprintf("k-%03d", i)), []byte("value"))
	}
	keys := make([][]byte, 100)
	for i := range keys {
		keys[i] = []byte(fmt.Sprintf("k-%03d", 99-i))
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		rb, _ := db.NewReadBatch()
		for _, k := range keys {
			_ = rb.HGetBorrowed("h", k)
		}
		if err := rb.Execute(func(ReadBatchResult) error { return nil }); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkV527HGetMany100(b *testing.B) {
	db := openV57TestDB(b)
	defer db.Close()
	for i := 0; i < 100; i++ {
		_ = db.HSet("h", []byte(fmt.Sprintf("k-%03d", i)), []byte("value"))
	}
	keys := make([][]byte, 100)
	for i := range keys {
		keys[i] = []byte(fmt.Sprintf("k-%03d", i))
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if err := db.HGetMany("h", keys, func(int, []byte, []byte, bool) error { return nil }); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkV527ZScoreMany100(b *testing.B) {
	db := openV57TestDB(b)
	defer db.Close()
	for i := 0; i < 100; i++ {
		_ = db.ZSet("z", []byte(fmt.Sprintf("k-%03d", i)), uint64(i))
	}
	keys := make([][]byte, 100)
	for i := range keys {
		keys[i] = []byte(fmt.Sprintf("k-%03d", i))
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if err := db.ZScoreMany("z", keys, func(int, []byte, uint64, bool) error { return nil }); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkV527ReadBatch1000Sorted(b *testing.B) {
	db := openV57TestDB(b)
	defer db.Close()
	for i := 0; i < 1000; i++ {
		_ = db.HSet("h", []byte(fmt.Sprintf("k-%04d", i)), []byte("value"))
	}
	keys := make([][]byte, 1000)
	for i := range keys {
		keys[i] = []byte(fmt.Sprintf("k-%04d", i))
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		rb, _ := db.NewReadBatch()
		for _, k := range keys {
			_ = rb.HGetBorrowed("h", k)
		}
		if err := rb.Execute(func(ReadBatchResult) error { return nil }); err != nil {
			b.Fatal(err)
		}
	}
}
