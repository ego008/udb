package udb

import (
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"
)

const (
	v518DefaultBatchN = 100
	v518FixtureN      = 10000
)

func v518Open(b *testing.B, noSync bool) *DB {
	b.Helper()
	o := DefaultOptions()
	o.Maintenance.Enabled = false
	o.NoSync = noSync
	db, err := OpenWithOptions(filepath.Join(b.TempDir(), "bench.db"), &o)
	if err != nil {
		b.Fatal(err)
	}
	return db
}

func v518Entries(n int) []Entry {
	entries := make([]Entry, n)
	for i := range entries {
		entries[i] = Entry{Key: v512Key(i % v518FixtureN), Value: v512Value(i)}
	}
	return entries
}

func v518ZEntries(n int) []ZEntry {
	entries := make([]ZEntry, n)
	for i := range entries {
		entries[i] = ZEntry{Member: v512Key(i % v518FixtureN), Score: uint64(i)}
	}
	return entries
}

func v518KVs(n int) [][]byte {
	kvs := make([][]byte, 0, n*2)
	for i := 0; i < n; i++ {
		kvs = append(kvs, v512Key(i%v518FixtureN), v512Value(i))
	}
	return kvs
}

func v518ZKVs(n int) [][]byte {
	kvs := make([][]byte, 0, n*2)
	for i := 0; i < n; i++ {
		kvs = append(kvs, v512Key(i%v518FixtureN), I2b(uint64(i)))
	}
	return kvs
}

func v518ReportItems(b *testing.B, items int, started time.Time) {
	b.Helper()
	if items <= 0 || b.N <= 0 {
		return
	}
	b.ReportMetric(float64(items), "items/op")
	b.ReportMetric(float64(items*b.N)/time.Since(started).Seconds(), "items/s")
}

// BenchmarkV518Hash100IndividualTx is the correct transaction-amortization
// baseline: one benchmark iteration performs 100 independent write
// transactions. It should be compared with Hash100Batch and Hash100Tx.
func BenchmarkV518Hash100IndividualTx(b *testing.B) {
	db := v518Open(b, false)
	defer db.Close()
	keys := make([][]byte, v518DefaultBatchN)
	values := make([][]byte, v518DefaultBatchN)
	for i := range keys {
		keys[i], values[i] = v512Key(i), v512Value(i)
	}
	b.ReportAllocs()
	b.ResetTimer()
	started := time.Now()
	for i := 0; i < b.N; i++ {
		for j := range keys {
			if err := db.HSet(v512FixtureHash, keys[j], values[j]); err != nil {
				b.Fatal(err)
			}
		}
	}
	b.StopTimer()
	v518ReportItems(b, v518DefaultBatchN, started)
}

func BenchmarkV518Hash100Batch(b *testing.B) {
	db := v518Open(b, false)
	defer db.Close()
	entries := v518Entries(v518DefaultBatchN)
	b.ReportAllocs()
	b.ResetTimer()
	started := time.Now()
	for i := 0; i < b.N; i++ {
		if err := db.HSetBatch(v512FixtureHash, entries); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	v518ReportItems(b, v518DefaultBatchN, started)
}

func BenchmarkV518Hash100Tx(b *testing.B) {
	db := v518Open(b, false)
	defer db.Close()
	kvs := v518KVs(v518DefaultBatchN)
	b.ReportAllocs()
	b.ResetTimer()
	started := time.Now()
	for i := 0; i < b.N; i++ {
		if err := db.Update(func(tx *Tx) error {
			return db.Hmset(tx, v512FixtureHash, kvs...)
		}); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	v518ReportItems(b, v518DefaultBatchN, started)
}

func BenchmarkV518Z100IndividualTx(b *testing.B) {
	db := v518Open(b, false)
	defer db.Close()
	members := make([][]byte, v518DefaultBatchN)
	for i := range members {
		members[i] = v512Key(i)
	}
	b.ReportAllocs()
	b.ResetTimer()
	started := time.Now()
	for i := 0; i < b.N; i++ {
		for j, member := range members {
			if err := db.ZSet(v512FixtureZSet, member, uint64(j)); err != nil {
				b.Fatal(err)
			}
		}
	}
	b.StopTimer()
	v518ReportItems(b, v518DefaultBatchN, started)
}

func BenchmarkV518Z100Batch(b *testing.B) {
	db := v518Open(b, false)
	defer db.Close()
	entries := v518ZEntries(v518DefaultBatchN)
	b.ReportAllocs()
	b.ResetTimer()
	started := time.Now()
	for i := 0; i < b.N; i++ {
		if err := db.ZSetBatch(v512FixtureZSet, entries); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	v518ReportItems(b, v518DefaultBatchN, started)
}

func BenchmarkV518Z100Tx(b *testing.B) {
	db := v518Open(b, false)
	defer db.Close()
	kvs := v518ZKVs(v518DefaultBatchN)
	b.ReportAllocs()
	b.ResetTimer()
	started := time.Now()
	for i := 0; i < b.N; i++ {
		if err := db.Update(func(tx *Tx) error {
			return db.Zmset(tx, v512FixtureZSet, kvs...)
		}); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	v518ReportItems(b, v518DefaultBatchN, started)
}

func BenchmarkV518BatchSizeHash(b *testing.B) {
	for _, size := range []int{1, 10, 50, 100, 500, 1000, 5000} {
		b.Run(strconv.Itoa(size), func(b *testing.B) {
			db := v518Open(b, false)
			defer db.Close()
			entries := v518Entries(size)
			b.ReportAllocs()
			b.ResetTimer()
			started := time.Now()
			for i := 0; i < b.N; i++ {
				if err := db.HSetBatch(v512FixtureHash, entries); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			v518ReportItems(b, size, started)
		})
	}
}

func BenchmarkV518BatchSizeZSet(b *testing.B) {
	for _, size := range []int{1, 10, 50, 100, 500, 1000, 5000} {
		b.Run(strconv.Itoa(size), func(b *testing.B) {
			db := v518Open(b, false)
			defer db.Close()
			entries := v518ZEntries(size)
			b.ReportAllocs()
			b.ResetTimer()
			started := time.Now()
			for i := 0; i < b.N; i++ {
				if err := db.ZSetBatch(v512FixtureZSet, entries); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			v518ReportItems(b, size, started)
		})
	}
}

func BenchmarkV518EmptyUpdate(b *testing.B) {
	db := v518Open(b, false)
	defer db.Close()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := db.Update(func(*Tx) error { return nil }); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkV518WriteDurability is deliberately explicit: NoSync changes
// durability guarantees and is not a recommended production default. This
// benchmark exists to quantify how much of write latency is durability cost.
func BenchmarkV518WriteDurability(b *testing.B) {
	for _, noSync := range []bool{false, true} {
		name := "Sync"
		if noSync {
			name = "NoSync"
		}
		b.Run(name, func(b *testing.B) {
			db := v518Open(b, noSync)
			defer db.Close()
			key := []byte("durability-key")
			value := []byte("durability-value")
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := db.HSet(v512FixtureHash, key, value); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func v518ParallelWriters(b *testing.B, workers int, zset bool) {
	db := v518Open(b, false)
	defer db.Close()
	b.ReportAllocs()
	b.ResetTimer()
	var wg sync.WaitGroup
	wg.Add(workers)
	started := time.Now()
	for w := 0; w < workers; w++ {
		count := b.N / workers
		if w < b.N%workers {
			count++
		}
		start := w * (b.N / workers)
		if w < b.N%workers {
			start += w
		} else {
			start += b.N % workers
		}
		go func(start, count int) {
			defer wg.Done()
			for n := 0; n < count; n++ {
				i := start + n + 1
				key := v512Key(i % v518FixtureN)
				var err error
				if zset {
					err = db.ZSet(v512FixtureZSet, key, uint64(i))
				} else {
					err = db.HSet(v512FixtureHash, key, v512Value(i))
				}
				if err != nil {
					b.Error(err)
					return
				}
			}
		}(start, count)
	}
	wg.Wait()
	b.StopTimer()
	elapsed := time.Since(started)
	if elapsed > 0 {
		b.ReportMetric(float64(b.N)/elapsed.Seconds(), "ops/s")
	}
}

func BenchmarkV518ParallelWritersHash(b *testing.B) {
	for _, workers := range []int{1, 2, 4, 8, 16, 32} {
		b.Run(strconv.Itoa(workers), func(b *testing.B) { v518ParallelWriters(b, workers, false) })
	}
}

func BenchmarkV518ParallelWritersZSet(b *testing.B) {
	for _, workers := range []int{1, 2, 4, 8, 16, 32} {
		b.Run(strconv.Itoa(workers), func(b *testing.B) { v518ParallelWriters(b, workers, true) })
	}
}
