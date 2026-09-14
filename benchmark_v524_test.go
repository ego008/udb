package udb

import (
	"fmt"
	"testing"
)

func BenchmarkV524HGetSingleTransaction(b *testing.B) {
	db := openV57TestDB(b)
	defer db.Close()
	for i := 0; i < 100; i++ {
		if err := db.HSet("h", []byte(fmt.Sprintf("k-%d", i)), []byte("value")); err != nil {
			b.Fatal(err)
		}
	}
	key := []byte("k-50")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := db.View(func(tx *Tx) error {
			r, err := NewReadEngine(tx)
			if err != nil {
				return err
			}
			reply := r.HGet("h", key)
			if reply.Err != nil {
				return reply.Err
			}
			return nil
		}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkV524HGet100ReadTransaction(b *testing.B) {
	db := openV57TestDB(b)
	defer db.Close()
	keys := make([][]byte, 100)
	for i := range keys {
		keys[i] = []byte(fmt.Sprintf("k-%d", i))
		if err := db.HSet("h", keys[i], []byte("value")); err != nil {
			b.Fatal(err)
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := db.ReadTransaction(func(tx *Tx) error {
			r, err := NewReadEngine(tx)
			if err != nil {
				return err
			}
			for _, key := range keys {
				reply := r.HGet("h", key)
				if reply.Err != nil {
					return reply.Err
				}
			}
			return nil
		}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkV524HGetBatch100(b *testing.B) {
	db := openV57TestDB(b)
	defer db.Close()
	keys := make([][]byte, 100)
	for i := range keys {
		keys[i] = []byte(fmt.Sprintf("k-%d", i))
		if err := db.HSet("h", keys[i], []byte("value")); err != nil {
			b.Fatal(err)
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		reply := db.HGetBatch("h", keys)
		if reply.Err != nil {
			b.Fatal(reply.Err)
		}
	}
}

func BenchmarkV524ZScoreSingleTransaction(b *testing.B) {
	db := openV57TestDB(b)
	defer db.Close()
	for i := 0; i < 100; i++ {
		if err := db.ZSet("z", []byte(fmt.Sprintf("k-%d", i)), uint64(i)); err != nil {
			b.Fatal(err)
		}
	}
	key := []byte("k-50")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := db.ZScore("z", key); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkV524ZScore100ReadTransaction(b *testing.B) {
	db := openV57TestDB(b)
	defer db.Close()
	keys := make([][]byte, 100)
	for i := range keys {
		keys[i] = []byte(fmt.Sprintf("k-%d", i))
		if err := db.ZSet("z", keys[i], uint64(i)); err != nil {
			b.Fatal(err)
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := db.ReadTransaction(func(tx *Tx) error {
			r, err := NewReadEngine(tx)
			if err != nil {
				return err
			}
			for _, key := range keys {
				if _, err := r.ZScore("z", key); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkV524ZGetBatch100(b *testing.B) {
	db := openV57TestDB(b)
	defer db.Close()
	keys := make([][]byte, 100)
	for i := range keys {
		keys[i] = []byte(fmt.Sprintf("k-%d", i))
		if err := db.ZSet("z", keys[i], uint64(i)); err != nil {
			b.Fatal(err)
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		reply := db.ZGetBatch("z", keys)
		if reply.Err != nil {
			b.Fatal(reply.Err)
		}
	}
}

func BenchmarkV524ZScan100(b *testing.B) {
	db := openV57TestDB(b)
	defer db.Close()
	entries := make([]ZEntry, 100)
	for i := range entries {
		entries[i] = ZEntry{Member: []byte(fmt.Sprintf("k-%03d", i)), Score: uint64(i)}
	}
	if err := db.ZSetBatch("z", entries); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := db.View(func(tx *Tx) error {
			r, err := NewReadEngine(tx)
			if err != nil {
				return err
			}
			reply := r.ZScan("z", nil, nil, 100)
			return reply.Err
		}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkV524ZScanEach100(b *testing.B) {
	db := openV57TestDB(b)
	defer db.Close()
	entries := make([]ZEntry, 100)
	for i := range entries {
		entries[i] = ZEntry{Member: []byte(fmt.Sprintf("k-%03d", i)), Score: uint64(i)}
	}
	if err := db.ZSetBatch("z", entries); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := db.View(func(tx *Tx) error {
			r, err := NewReadEngine(tx)
			if err != nil {
				return err
			}
			return r.ZScanEach("z", nil, nil, 100, false, func(_ []byte, _ uint64) error { return nil })
		}); err != nil {
			b.Fatal(err)
		}
	}
}
