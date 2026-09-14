package udb

import (
	"strconv"
	"sync/atomic"
	"testing"
)

const v517BatchN = 100

func v517Entries(n int) []Entry {
	entries := make([]Entry, n)
	for i := range entries {
		entries[i] = Entry{Key: v512Key(i), Value: v512Value(i)}
	}
	return entries
}

func v517ZEntries(n int) []ZEntry {
	entries := make([]ZEntry, n)
	for i := range entries {
		entries[i] = ZEntry{Member: v512Key(i), Score: uint64(i)}
	}
	return entries
}

func BenchmarkV517HashSetSingle(b *testing.B) {
	BenchmarkV512HashSetSingle(b)
}

func BenchmarkV517HashSetBatch100(b *testing.B) {
	db := v512Open(b)
	defer db.Close()
	entries := v517Entries(v517BatchN)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := db.HSetBatch(v512FixtureHash, entries); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkV517HashHmset100Tx(b *testing.B) {
	db := v512Open(b)
	defer db.Close()
	kvs := make([][]byte, 0, v517BatchN*2)
	for i := 0; i < v517BatchN; i++ {
		kvs = append(kvs, v512Key(i), v512Value(i))
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := db.Update(func(tx *Tx) error {
			return db.Hmset(tx, v512FixtureHash, kvs...)
		}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkV517ZSetSingle(b *testing.B) {
	BenchmarkV512ZSetSingle(b)
}

func BenchmarkV517ZSetBatch100(b *testing.B) {
	db := v512Open(b)
	defer db.Close()
	entries := v517ZEntries(v517BatchN)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := db.ZSetBatch(v512FixtureZSet, entries); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkV517ZSetZmset100Tx(b *testing.B) {
	db := v512Open(b)
	defer db.Close()
	kvs := make([][]byte, 0, v517BatchN*2)
	for i := 0; i < v517BatchN; i++ {
		kvs = append(kvs, v512Key(i), I2b(uint64(i)))
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := db.Update(func(tx *Tx) error {
			return db.Zmset(tx, v512FixtureZSet, kvs...)
		}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkV517HashSetParallel(b *testing.B) {
	db := v512Open(b)
	defer db.Close()
	var seq atomic.Uint64
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			i := int(seq.Add(1))
			if err := db.HSet(v512FixtureHash, []byte("k-"+strconv.Itoa(i%v512FixtureN)), v512Value(i)); err != nil {
				b.Fatalf("HSet: %v", err)
			}
		}
	})
}

func BenchmarkV517ZSetParallel(b *testing.B) {
	db := v512Open(b)
	defer db.Close()
	var seq atomic.Uint64
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			i := int(seq.Add(1))
			if err := db.ZSet(v512FixtureZSet, []byte("k-"+strconv.Itoa(i%v512FixtureN)), uint64(i)); err != nil {
				b.Fatalf("ZSet: %v", err)
			}
		}
	})
}
