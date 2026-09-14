package udb

import (
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"
)

func v520OpenBench(b *testing.B) *DB {
	b.Helper()
	o := DefaultOptions()
	o.Maintenance.Enabled = false
	db, err := OpenWithOptions(filepath.Join(b.TempDir(), "bench.db"), &o)
	if err != nil {
		b.Fatal(err)
	}
	return db
}

func v520ReportItems(b *testing.B, items int, started time.Time) {
	b.Helper()
	elapsed := time.Since(started).Seconds()
	if elapsed > 0 {
		b.ReportMetric(float64(items), "items/op")
		b.ReportMetric(float64(items*b.N)/elapsed, "items/s")
	}
}

// Async100Sequential submits all requests first and waits afterwards. This is
// the direct comparison with Batch100 and isolates the benefit of decoupling
// producer submission from commit completion.
func BenchmarkV520Async100Sequential(b *testing.B) {
	db := v520OpenBench(b)
	defer db.Close()
	p, err := db.NewWritePipeline(WritePipelineOptions{MaxBatchSize: 100, MaxWait: 5 * time.Millisecond, QueueSize: 256})
	if err != nil {
		b.Fatal(err)
	}
	defer p.Close()
	b.ReportAllocs()
	b.ResetTimer()
	started := time.Now()
	for n := 0; n < b.N; n++ {
		fs := make([]*WriteFuture, 0, 100)
		for i := 0; i < 100; i++ {
			f, err := p.HSetAsync("async-seq", []byte("k-"+strconv.Itoa(i)), []byte("v"))
			if err != nil {
				b.Fatal(err)
			}
			fs = append(fs, f)
		}
		for _, f := range fs {
			if err := f.Wait(); err != nil {
				b.Fatal(err)
			}
		}
	}
	b.StopTimer()
	v520ReportItems(b, 100, started)
}

func BenchmarkV520Async100Concurrent(b *testing.B) {
	db := v520OpenBench(b)
	defer db.Close()
	p, err := db.NewWritePipeline(WritePipelineOptions{MaxBatchSize: 100, MaxWait: 5 * time.Millisecond, QueueSize: 256})
	if err != nil {
		b.Fatal(err)
	}
	defer p.Close()
	const items, workers = 100, 8
	b.ReportAllocs()
	b.ResetTimer()
	started := time.Now()
	for n := 0; n < b.N; n++ {
		var wg sync.WaitGroup
		wg.Add(workers)
		errCh := make(chan error, workers)
		for w := 0; w < workers; w++ {
			w := w
			go func() {
				defer wg.Done()
				for i := w; i < items; i += workers {
					if _, err := p.HSetAsync("async-concurrent", []byte("k-"+strconv.Itoa(i)), []byte("v")); err != nil {
						errCh <- err
						return
					}
				}
			}()
		}
		wg.Wait()
		close(errCh)
		for err := range errCh {
			if err != nil {
				b.Fatal(err)
			}
		}
		if err := p.Flush(); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	v520ReportItems(b, items, started)
}

func BenchmarkV520AsyncBatchSize(b *testing.B) {
	for _, size := range []int{10, 50, 100, 500, 1000} {
		b.Run(strconv.Itoa(size), func(b *testing.B) {
			db := v520OpenBench(b)
			defer db.Close()
			p, err := db.NewWritePipeline(WritePipelineOptions{MaxBatchSize: size, MaxWait: 5 * time.Millisecond, QueueSize: size * 2})
			if err != nil {
				b.Fatal(err)
			}
			defer p.Close()
			b.ReportAllocs()
			b.ResetTimer()
			started := time.Now()
			for n := 0; n < b.N; n++ {
				fs := make([]*WriteFuture, 0, size)
				for i := 0; i < size; i++ {
					f, err := p.HSetAsync("async-size", []byte("k-"+strconv.Itoa(i)), []byte("v"))
					if err != nil {
						b.Fatal(err)
					}
					fs = append(fs, f)
				}
				for _, f := range fs {
					if err := f.Wait(); err != nil {
						b.Fatal(err)
					}
				}
			}
			b.StopTimer()
			v520ReportItems(b, size, started)
		})
	}
}

func BenchmarkV520AsyncParallelProducers(b *testing.B) {
	for _, workers := range []int{1, 2, 4, 8, 16, 32} {
		b.Run(strconv.Itoa(workers), func(b *testing.B) {
			db := v520OpenBench(b)
			defer db.Close()
			p, err := db.NewWritePipeline(WritePipelineOptions{MaxBatchSize: 100, MaxWait: 5 * time.Millisecond, QueueSize: 1024})
			if err != nil {
				b.Fatal(err)
			}
			defer p.Close()
			b.ReportAllocs()
			b.ResetTimer()
			started := time.Now()
			var wg sync.WaitGroup
			wg.Add(workers)
			errCh := make(chan error, workers)
			for w := 0; w < workers; w++ {
				w := w
				go func() {
					defer wg.Done()
					for i := w; i < b.N; i += workers {
						if _, err := p.HSetAsync("async-parallel", []byte("k-"+strconv.Itoa(i)), []byte("v")); err != nil {
							errCh <- err
							return
						}
					}
				}()
			}
			wg.Wait()
			close(errCh)
			for err := range errCh {
				if err != nil {
					b.Fatal(err)
				}
			}
			if err := p.Flush(); err != nil {
				b.Fatal(err)
			}
			b.StopTimer()
			elapsed := time.Since(started).Seconds()
			if elapsed > 0 {
				b.ReportMetric(float64(b.N)/elapsed, "ops/s")
			}
		})
	}
}
