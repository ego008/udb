package udb

import (
	"bytes"
	"fmt"
	"testing"
)

func setupV532BenchDB(b *testing.B, n int) *DB {
	db := openV57TestDB(b)
	entries := make([]Entry, n)
	for i := 0; i < n; i++ {
		entries[i] = Entry{Key: []byte(fmt.Sprintf("k-%08d", i)), Value: []byte("value")}
	}
	if err := db.HSetBatch("h", entries); err != nil {
		b.Fatal(err)
	}
	return db
}

func makeV532Keys(n, start int) [][]byte {
	keys := make([][]byte, n)
	for i := range keys {
		keys[i] = []byte(fmt.Sprintf("k-%08d", start+i))
	}
	return keys
}

func benchmarkV532HGetMany(b *testing.B, bucketSize, batch, start int) {
	db := setupV532BenchDB(b, bucketSize)
	defer db.Close()
	keys := makeV532Keys(batch, start)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := db.HGetMany("h", keys, func(int, []byte, []byte, bool) error { return nil }); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkV532HGetMany10Point(b *testing.B) {
	db := setupV532BenchDB(b, 1000)
	defer db.Close()
	keys := makeV532Keys(10, 450)
	keys[1], keys[2] = keys[2], keys[1]
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := db.HGetMany("h", keys, func(int, []byte, []byte, bool) error { return nil }); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkV532HGetMany100Point(b *testing.B) {
	db := setupV532BenchDB(b, 1000)
	defer db.Close()
	keys := makeV532Keys(100, 450)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		err := db.View(func(tx *Tx) error {
			bucket := tx.bucket(bucketName(hashPrefix, "h"))
			if bucket == nil {
				return ErrBucketNotFound
			}
			for _, key := range keys {
				_ = bucket.Get(key)
			}
			return nil
		})
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkV532HGetMany100CursorSeek(b *testing.B) {
	benchmarkV532ForcedCursor(b, 1000, 100, 450, ReadCursorSeek)
}

func BenchmarkV532HGetMany100CursorFirst(b *testing.B) {
	benchmarkV532ForcedCursor(b, 1000, 100, 450, ReadCursorFirst)
}

func BenchmarkV532HGetMany100CursorAdaptive(b *testing.B) {
	benchmarkV532ForcedCursor(b, 1000, 100, 450, ReadCursorAdaptive)
}

func benchmarkV532ForcedCursor(b *testing.B, bucketSize, batch, start int, cursorStart ReadCursorStart) {
	db := setupV532BenchDB(b, bucketSize)
	defer db.Close()
	keys := makeV532Keys(batch, start)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		err := db.View(func(tx *Tx) error {
			bucket := tx.bucket(bucketName(hashPrefix, "h"))
			if bucket == nil {
				return ErrBucketNotFound
			}
			c := bucket.Cursor()
			k, v := cursorPosition(c, keys[0], cursorStart, 8)
			for _, key := range keys {
				for k != nil && bytes.Compare(k, key) < 0 {
					k, v = c.Next()
				}
				if k != nil && bytes.Equal(k, key) {
					_ = v
				}
			}
			return nil
		})
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkV532PlanReadKeys100(b *testing.B) {
	keys := makeV532Keys(100, 450)
	opts := defaultReadPlannerOptions()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = planReadKeysFast(keys, opts)
	}
}
