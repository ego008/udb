package udb

import "testing"

func BenchmarkV525HGetBatch10(b *testing.B)   { benchmarkV525HBatch(b, 10) }
func BenchmarkV525HGetBatch100(b *testing.B)  { benchmarkV525HBatch(b, 100) }
func BenchmarkV525HGetBatch1000(b *testing.B) { benchmarkV525HBatch(b, 1000) }
func benchmarkV525HBatch(b *testing.B, n int) {
	db := openV57TestDB(b)
	defer db.Close()
	keys := make([][]byte, n)
	for i := range keys {
		keys[i] = []byte("k")
	}
	_ = db.HSet("h", []byte("k"), []byte("v"))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r := db.HGetBatch("h", keys)
		if r.Err != nil {
			b.Fatal(r.Err)
		}
	}
}

func BenchmarkV525ZScoreBatch10(b *testing.B)   { benchmarkV525ZScoreBatch(b, 10) }
func BenchmarkV525ZScoreBatch100(b *testing.B)  { benchmarkV525ZScoreBatch(b, 100) }
func BenchmarkV525ZScoreBatch1000(b *testing.B) { benchmarkV525ZScoreBatch(b, 1000) }
func benchmarkV525ZScoreBatch(b *testing.B, n int) {
	db := openV57TestDB(b)
	defer db.Close()
	keys := make([][]byte, n)
	for i := range keys {
		keys[i] = []byte("k")
	}
	_ = db.ZSet("z", []byte("k"), 1)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r := db.ZGetBatch("z", keys)
		if r.Err != nil {
			b.Fatal(r.Err)
		}
	}
}

func BenchmarkV525ZScan100(b *testing.B)  { benchmarkV525ZScan(b, 100) }
func BenchmarkV525ZScan1000(b *testing.B) { benchmarkV525ZScan(b, 1000) }
func benchmarkV525ZScan(b *testing.B, n int) {
	db := openV57TestDB(b)
	defer db.Close()
	for i := 0; i < n; i++ {
		_ = db.ZSet("z", []byte{byte(i >> 8), byte(i)}, uint64(i))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r := db.View(func(tx *Tx) error { rr := db.zscan(tx, "z", nil, nil, n, false); return rr.ErrOrNil() })
		if r != nil {
			b.Fatal(r)
		}
	}
}

func BenchmarkV525ZScanEach100(b *testing.B)  { benchmarkV525ZScanEach(b, 100) }
func BenchmarkV525ZScanEach1000(b *testing.B) { benchmarkV525ZScanEach(b, 1000) }
func benchmarkV525ZScanEach(b *testing.B, n int) {
	db := openV57TestDB(b)
	defer db.Close()
	for i := 0; i < n; i++ {
		_ = db.ZSet("z", []byte{byte(i >> 8), byte(i)}, uint64(i))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r := db.View(func(tx *Tx) error {
			return db.zscanEach(tx, "z", nil, nil, n, false, func([]byte, uint64) error { return nil })
		})
		if r != nil {
			b.Fatal(r)
		}
	}
}

func BenchmarkV525ZIterator100(b *testing.B)  { benchmarkV525ZIterator(b, 100) }
func BenchmarkV525ZIterator1000(b *testing.B) { benchmarkV525ZIterator(b, 1000) }
func benchmarkV525ZIterator(b *testing.B, n int) {
	db := openV57TestDB(b)
	defer db.Close()
	for i := 0; i < n; i++ {
		_ = db.ZSet("z", []byte{byte(i >> 8), byte(i)}, uint64(i))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		it, err := db.NewZIterator("z", nil, nil, n)
		if err != nil {
			b.Fatal(err)
		}
		for it.Next() {
			_ = it.Score()
		}
		if err := it.Err(); err != nil {
			b.Fatal(err)
		}
		_ = it.Close()
	}
}

func BenchmarkV525HIterator100(b *testing.B)  { benchmarkV525HIterator(b, 100) }
func BenchmarkV525HIterator1000(b *testing.B) { benchmarkV525HIterator(b, 1000) }
func benchmarkV525HIterator(b *testing.B, n int) {
	db := openV57TestDB(b)
	defer db.Close()
	for i := 0; i < n; i++ {
		_ = db.HSet("h", []byte{byte(i >> 8), byte(i)}, []byte("v"))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		it, err := db.NewHIterator("h", nil, n)
		if err != nil {
			b.Fatal(err)
		}
		for it.Next() {
			_ = it.Value()
		}
		if err := it.Err(); err != nil {
			b.Fatal(err)
		}
		_ = it.Close()
	}
}

func BenchmarkV525ReadSession100(b *testing.B) {
	db := openV57TestDB(b)
	defer db.Close()
	_ = db.HSet("h", []byte("k"), []byte("v"))
	s, err := db.NewReadSession()
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		err := s.Read(func(r *ReadEngine) error {
			for j := 0; j < 100; j++ {
				if x := r.HGet("h", []byte("k")); x.Err != nil {
					return x.Err
				}
			}
			return nil
		})
		if err != nil {
			b.Fatal(err)
		}
	}
}
