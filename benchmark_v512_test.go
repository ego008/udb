package udb

import (
	"fmt"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
)

// V5.12 benchmarks intentionally keep setup outside the timed region where
// possible. This makes the numbers useful for comparing UDB versions rather
// than measuring fixture construction.
const (
	v512FixtureHash = "bench-hash"
	v512FixtureZSet = "bench-zset"
	v512FixtureN    = 10000
)

func v512Open(b *testing.B) *DB {
	b.Helper()
	o := DefaultOptions()
	o.Maintenance.Enabled = false
	db, err := OpenWithOptions(filepath.Join(b.TempDir(), "bench.db"), &o)
	if err != nil {
		b.Fatal(err)
	}
	return db
}

func v512Key(i int) []byte   { return []byte("k-" + strconv.Itoa(i)) }
func v512Value(i int) []byte { return []byte("value-" + strconv.Itoa(i)) }

func v512Populate(b *testing.B, db *DB, n int) {
	b.Helper()
	err := db.Update(func(tx *Tx) error {
		for i := 0; i < n; i++ {
			if err := db.Hset(tx, v512FixtureHash, v512Key(i), v512Value(i)); err != nil {
				return err
			}
			if err := db.Zset(tx, v512FixtureZSet, v512Key(i), uint64(i)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		b.Fatal(err)
	}
}

func BenchmarkV512HashSetSingle(b *testing.B) {
	db := v512Open(b)
	defer db.Close()
	key := []byte("hot-key")
	value := []byte("hot-value")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := db.Update(func(tx *Tx) error { return db.Hset(tx, v512FixtureHash, key, value) }); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkV512HashGetSingle(b *testing.B) {
	db := v512Open(b)
	defer db.Close()
	if err := db.HSet(v512FixtureHash, []byte("hot-key"), []byte("hot-value")); err != nil {
		b.Fatal(err)
	}
	key := []byte("hot-key")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r := db.HGet(v512FixtureHash, key)
		if r.Err != nil || r.State != replyOK {
			b.Fatalf("unexpected HGet result: state=%s err=%v", r.State, r.Err)
		}
	}
}

func BenchmarkV512HashBatchSet(b *testing.B) {
	db := v512Open(b)
	defer db.Close()
	kvs := make([][]byte, 0, 200)
	for i := 0; i < 100; i++ {
		kvs = append(kvs, v512Key(i), v512Value(i))
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := db.Update(func(tx *Tx) error { return db.Hmset(tx, v512FixtureHash, kvs...) }); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkV512HashGetParallel(b *testing.B) {
	db := v512Open(b)
	defer db.Close()
	v512Populate(b, db, v512FixtureN)
	var seq atomic.Uint64
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			key := v512Key(int(seq.Add(1) % v512FixtureN))
			r := db.HGet(v512FixtureHash, key)
			if r.Err != nil && r.Err != ErrKeyNotFound {
				b.Fatalf("HGet: %v", r.Err)
			}
		}
	})
}

func BenchmarkV512HashUpdateParallel(b *testing.B) {
	db := v512Open(b)
	defer db.Close()
	var seq atomic.Uint64
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			i := int(seq.Add(1))
			if err := db.HSet(v512FixtureHash, v512Key(i%v512FixtureN), v512Value(i)); err != nil {
				b.Fatalf("HSet: %v", err)
			}
		}
	})
}

func BenchmarkV512ZSetSingle(b *testing.B) {
	db := v512Open(b)
	defer db.Close()
	key := []byte("hot-member")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := db.ZSet(v512FixtureZSet, key, uint64(i)); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkV512ZScoreSingle(b *testing.B) {
	db := v512Open(b)
	defer db.Close()
	if err := db.ZSet(v512FixtureZSet, []byte("hot-member"), 123); err != nil {
		b.Fatal(err)
	}
	key := []byte("hot-member")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := db.View(func(tx *Tx) error {
			_, err := db.Zscore(tx, v512FixtureZSet, key)
			return err
		}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkV512ZScanRange(b *testing.B) {
	db := v512Open(b)
	defer db.Close()
	v512Populate(b, db, v512FixtureN)
	startScore := I2b(5000)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := db.View(func(tx *Tx) error {
			r := db.Zscan(tx, v512FixtureZSet, nil, startScore, 100)
			if r.Err != nil {
				return r.Err
			}
			if len(r.Data) == 0 {
				return fmt.Errorf("empty Zscan result")
			}
			return nil
		}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkV512ZScanRangeParallel(b *testing.B) {
	db := v512Open(b)
	defer db.Close()
	v512Populate(b, db, v512FixtureN)
	startScore := I2b(5000)
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if err := db.View(func(tx *Tx) error {
				r := db.Zscan(tx, v512FixtureZSet, nil, startScore, 100)
				return r.Err
			}); err != nil {
				b.Fatalf("Zscan: %v", err)
			}
		}
	})
}

func BenchmarkV512CompactAndReplace(b *testing.B) {
	o := DefaultOptions()
	o.Maintenance.Enabled = false
	cfg := forceCompactConfig(o)
	for i := 0; i < b.N; i++ {
		db, err := OpenWithOptions(filepath.Join(b.TempDir(), fmt.Sprintf("compact-%d.db", i)), &o)
		if err != nil {
			b.Fatal(err)
		}
		v512Populate(b, db, 2000)
		b.StartTimer()
		if _, _, err := db.CompactAndReplace(cfg); err != nil {
			b.Fatal(err)
		}
		b.StopTimer()
		if err := db.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkV512CheckIntegrity(b *testing.B) {
	db := v512Open(b)
	defer db.Close()
	v512Populate(b, db, v512FixtureN)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := db.CheckIntegrity(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkV512RepairIntegrity(b *testing.B) {
	db := v512Open(b)
	defer db.Close()
	v512Populate(b, db, 5000)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := db.RepairIntegrity(RepairOptions{}); err != nil {
			b.Fatal(err)
		}
	}
}
