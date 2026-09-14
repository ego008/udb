package udb

import (
	"fmt"
	"testing"
)

func BenchmarkV522BatchBuilder100(b *testing.B) {
	db := openV57TestDB(b)
	defer db.Close()
	for i := 0; i < b.N; i++ {
		batch, err := db.NewBatch()
		if err != nil {
			b.Fatal(err)
		}
		for j := 0; j < 100; j++ {
			if err := batch.HSet("bench-h", []byte(fmt.Sprintf("k-%d", j)), []byte("value")); err != nil {
				b.Fatal(err)
			}
		}
		if err := batch.Commit(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkV522SnapshotHGet(b *testing.B) {
	db := openV57TestDB(b)
	defer db.Close()
	if err := db.HSet("bench-s", []byte("key"), []byte("value")); err != nil {
		b.Fatal(err)
	}
	s, err := db.Snapshot()
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r := s.HGet("bench-s", []byte("key"))
		if r.State != replyOK {
			b.Fatal(r)
		}
	}
}

func BenchmarkV522Metrics(b *testing.B) {
	db := openV57TestDB(b)
	defer db.Close()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = db.Metrics()
	}
}
