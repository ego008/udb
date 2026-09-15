package udb

import (
	"fmt"
	"testing"
)

func BenchmarkV526ReadBatch100(b *testing.B) {
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
			_ = rb.HGet("h", k)
		}
		if err := rb.Execute(func(r ReadBatchResult) error { return nil }); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkV526ReadBatch1000(b *testing.B) {
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
			_ = rb.HGet("h", k)
		}
		if err := rb.Execute(func(ReadBatchResult) error { return nil }); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkV526HIterator100(b *testing.B) {
	db := openV57TestDB(b)
	defer db.Close()
	for i := 0; i < 100; i++ {
		_ = db.HSet("h", []byte(fmt.Sprintf("k-%03d", i)), []byte("value"))
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		it, _ := db.NewHIterator("h", nil, 100)
		for it.Next() {
			_ = it.Value()
		}
		_ = it.Close()
	}
}

func BenchmarkV526ZIterator1000(b *testing.B) {
	db := openV57TestDB(b)
	defer db.Close()
	for i := 0; i < 1000; i++ {
		_ = db.ZSet("z", []byte(fmt.Sprintf("k-%04d", i)), uint64(i))
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		it, _ := db.NewZIterator("z", nil, nil, 1000)
		for it.Next() {
			_ = it.Member()
		}
		_ = it.Close()
	}
}

func BenchmarkV526ZReadBatch100(b *testing.B) {
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
		rb, _ := db.NewReadBatch()
		for _, k := range keys {
			_ = rb.ZScore("z", k)
		}
		if err := rb.Execute(func(r ReadBatchResult) error { return nil }); err != nil {
			b.Fatal(err)
		}
	}
}
