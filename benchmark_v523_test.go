package udb

import (
	"fmt"
	"testing"
	"time"
)

func BenchmarkV523BatchBuilder100(b *testing.B) {
	db := openV57TestDB(b)
	defer db.Close()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		batch, err := db.NewBatch()
		if err != nil {
			b.Fatal(err)
		}
		for j := 0; j < 100; j++ {
			if err := batch.HSet("v523-h", []byte(fmt.Sprintf("%d-%d", i, j)), []byte("value")); err != nil {
				b.Fatal(err)
			}
		}
		if err := batch.Commit(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkV523HSetBatch100(b *testing.B) {
	db := openV57TestDB(b)
	defer db.Close()
	entries := make([]Entry, 100)
	for i := range entries {
		entries[i] = Entry{Key: []byte(fmt.Sprintf("k-%d", i)), Value: []byte("value")}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := db.HSetBatch("v523-hbatch", entries); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkV523PipelineAsync100(b *testing.B) {
	db := openV57TestDB(b)
	defer db.Close()
	p, err := db.NewWritePipeline(WritePipelineOptions{MaxBatchSize: 100, MaxWait: 5 * time.Millisecond, QueueSize: 1024})
	if err != nil {
		b.Fatal(err)
	}
	defer p.Close()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		futures := make([]*WriteFuture, 0, 100)
		for j := 0; j < 100; j++ {
			f, err := p.HSetAsync("v523-p", []byte(fmt.Sprintf("%d-%d", i, j)), []byte("value"))
			if err != nil {
				b.Fatal(err)
			}
			futures = append(futures, f)
		}
		for _, f := range futures {
			if err := f.Wait(); err != nil {
				b.Fatal(err)
			}
		}
	}
}
