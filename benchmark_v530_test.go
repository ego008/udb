package udb

import (
	"fmt"
	"testing"
)

func benchmarkV530HGetMany(b *testing.B, n int, start int) {
	db := openV57TestDB(b)
	defer db.Close()
	for i := 0; i < 2000; i++ {
		if err := db.HSet("h", []byte(fmt.Sprintf("k-%06d", i)), []byte("value")); err != nil {
			b.Fatal(err)
		}
	}
	keys := make([][]byte, n)
	for i := range keys {
		keys[i] = []byte(fmt.Sprintf("k-%06d", start+i))
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := db.HGetMany("h", keys, func(int, []byte, []byte, bool) error { return nil }); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkV530HGetMany100SortedMiddle(b *testing.B) {
	benchmarkV530HGetMany(b, 100, 900)
}

func BenchmarkV530HGetMany100SortedHead(b *testing.B) {
	benchmarkV530HGetMany(b, 100, 0)
}

func BenchmarkV530HGetMany1000SortedMiddle(b *testing.B) {
	benchmarkV530HGetMany(b, 1000, 500)
}

func BenchmarkV530HGetMany100Random(b *testing.B) {
	db := openV57TestDB(b)
	defer db.Close()
	for i := 0; i < 2000; i++ {
		if err := db.HSet("h", []byte(fmt.Sprintf("k-%06d", i)), []byte("value")); err != nil {
			b.Fatal(err)
		}
	}
	keys := make([][]byte, 100)
	for i := range keys {
		keys[i] = []byte(fmt.Sprintf("k-%06d", 1999-i*19))
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := db.HGetMany("h", keys, func(int, []byte, []byte, bool) error { return nil }); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkV530NewReadEngine(b *testing.B) {
	db := openV57TestDB(b)
	defer db.Close()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := db.View(func(tx *Tx) error {
			r := newReadEngine(tx)
			_, err := r.HGetInt("missing", []byte("k"))
			return err
		}); err != ErrBucketNotFound {
			b.Fatalf("err=%v", err)
		}
	}
}
