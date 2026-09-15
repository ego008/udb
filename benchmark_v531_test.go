package udb

import (
	"bytes"
	"fmt"
	"testing"
)

func setupV531BenchDB(b *testing.B, n int) *DB {
	db := openV57TestDB(b)
	for i := 0; i < n; i++ {
		if err := db.HSet("h", []byte(fmt.Sprintf("k-%08d", i)), []byte("value")); err != nil {
			b.Fatal(err)
		}
	}
	return db
}

func makeV531Keys(n, start int) [][]byte {
	keys := make([][]byte, n)
	for i := range keys {
		keys[i] = []byte(fmt.Sprintf("k-%08d", start+i))
	}
	return keys
}

func benchmarkV531HGetMany(b *testing.B, bucketSize, batch, start int) {
	db := setupV531BenchDB(b, bucketSize)
	defer db.Close()
	keys := makeV531Keys(batch, start)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := db.HGetMany("h", keys, func(int, []byte, []byte, bool) error { return nil }); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkV531HGetMany10Head(b *testing.B)     { benchmarkV531HGetMany(b, 1000, 10, 0) }
func BenchmarkV531HGetMany32Head(b *testing.B)     { benchmarkV531HGetMany(b, 1000, 32, 0) }
func BenchmarkV531HGetMany100Head(b *testing.B)    { benchmarkV531HGetMany(b, 1000, 100, 0) }
func BenchmarkV531HGetMany100Middle(b *testing.B)  { benchmarkV531HGetMany(b, 1000, 100, 450) }
func BenchmarkV531HGetMany100Deep(b *testing.B)    { benchmarkV531HGetMany(b, 1000, 100, 850) }
func BenchmarkV531HGetMany1000Middle(b *testing.B) { benchmarkV531HGetMany(b, 10000, 1000, 4500) }

func benchmarkV531AccessPath(b *testing.B, bucketSize, batch, start int, path ReadCursorStart) {
	db := setupV531BenchDB(b, bucketSize)
	defer db.Close()
	keys := makeV531Keys(batch, start)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		err := db.View(func(tx *Tx) error {
			bucket := tx.bucket(bucketName(hashPrefix, "h"))
			if bucket == nil {
				return ErrBucketNotFound
			}
			c := bucket.Cursor()
			var k, v []byte
			switch path {
			case ReadCursorFirst:
				k, v = c.First()
			case ReadCursorSeek:
				k, v = c.Seek(keys[0])
			case ReadCursorAdaptive:
				k, v = adaptiveCursor(c, keys[0], defaultReadCursorOptions())
			}
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

func BenchmarkV531PathFirstHead100(b *testing.B) {
	benchmarkV531AccessPath(b, 1000, 100, 0, ReadCursorFirst)
}
func BenchmarkV531PathSeekHead100(b *testing.B) {
	benchmarkV531AccessPath(b, 1000, 100, 0, ReadCursorSeek)
}
func BenchmarkV531PathAdaptiveHead100(b *testing.B) {
	benchmarkV531AccessPath(b, 1000, 100, 0, ReadCursorAdaptive)
}
func BenchmarkV531PathFirstMiddle100(b *testing.B) {
	benchmarkV531AccessPath(b, 1000, 100, 450, ReadCursorFirst)
}
func BenchmarkV531PathSeekMiddle100(b *testing.B) {
	benchmarkV531AccessPath(b, 1000, 100, 450, ReadCursorSeek)
}
func BenchmarkV531PathAdaptiveMiddle100(b *testing.B) {
	benchmarkV531AccessPath(b, 1000, 100, 450, ReadCursorAdaptive)
}
