package udb

import (
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"
)

func v519OpenBench(b *testing.B) *DB {
	b.Helper()
	o := DefaultOptions()
	o.Maintenance.Enabled = false
	db, err := OpenWithOptions(filepath.Join(b.TempDir(), "bench.db"), &o)
	if err != nil {
		b.Fatal(err)
	}
	return db
}

func v519ReportItems(b *testing.B, items int, started time.Time) {
	b.Helper()
	elapsed := time.Since(started).Seconds()
	if items > 0 && elapsed > 0 {
		b.ReportMetric(float64(items), "items/op")
		b.ReportMetric(float64(items*b.N)/elapsed, "items/s")
	}
}

func BenchmarkV519Direct100Concurrent(b *testing.B) {
	db := v519OpenBench(b)
	defer db.Close()
	const items = 100
	const workers = 8
	b.ReportAllocs()
	b.ResetTimer()
	started := time.Now()
	for n := 0; n < b.N; n++ {
		var wg sync.WaitGroup
		var firstErr error
		var errMu sync.Mutex
		wg.Add(workers)
		for w := 0; w < workers; w++ {
			w := w
			go func() {
				defer wg.Done()
				for i := w; i < items; i += workers {
					if err := db.HSet("direct", []byte("k-"+strconv.Itoa(i)), []byte("v")); err != nil {
						errMu.Lock()
						if firstErr == nil {
							firstErr = err
						}
						errMu.Unlock()
						return
					}
				}
			}()
		}
		wg.Wait()
		if firstErr != nil {
			b.Fatal(firstErr)
		}
	}
	b.StopTimer()
	v519ReportItems(b, items, started)
}

func BenchmarkV519Batch100(b *testing.B) {
	db := v519OpenBench(b)
	defer db.Close()
	entries := make([]Entry, 100)
	for i := range entries {
		entries[i] = Entry{Key: []byte("k-" + strconv.Itoa(i)), Value: []byte("v")}
	}
	b.ReportAllocs()
	b.ResetTimer()
	started := time.Now()
	for n := 0; n < b.N; n++ {
		if err := db.HSetBatch("batch", entries); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	v519ReportItems(b, len(entries), started)
}

func BenchmarkV519Pipeline100Concurrent(b *testing.B) {
	db := v519OpenBench(b)
	defer db.Close()
	p, err := db.NewWritePipeline(WritePipelineOptions{MaxBatchSize: 100, MaxWait: 5 * time.Millisecond, QueueSize: 256})
	if err != nil {
		b.Fatal(err)
	}
	defer p.Close()

	const items = 100
	const workers = 8
	b.ReportAllocs()
	b.ResetTimer()
	started := time.Now()
	for n := 0; n < b.N; n++ {
		var wg sync.WaitGroup
		var firstErr error
		var errMu sync.Mutex
		wg.Add(workers)
		for w := 0; w < workers; w++ {
			w := w
			go func() {
				defer wg.Done()
				for i := w; i < items; i += workers {
					if err := p.HSet("pipeline", []byte("k-"+strconv.Itoa(i)), []byte("v")); err != nil {
						errMu.Lock()
						if firstErr == nil {
							firstErr = err
						}
						errMu.Unlock()
						return
					}
				}
			}()
		}
		wg.Wait()
		if firstErr != nil {
			b.Fatal(firstErr)
		}
		if err := p.Flush(); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	v519ReportItems(b, items, started)
}

func BenchmarkV519PipelineBatchSize(b *testing.B) {
	for _, size := range []int{10, 50, 100, 500, 1000} {
		b.Run(strconv.Itoa(size), func(b *testing.B) {
			db := v519OpenBench(b)
			defer db.Close()
			p, err := db.NewWritePipeline(WritePipelineOptions{MaxBatchSize: size, MaxWait: 5 * time.Millisecond, QueueSize: size * 2})
			if err != nil {
				b.Fatal(err)
			}
			defer p.Close()
			workers := 8
			b.ReportAllocs()
			b.ResetTimer()
			started := time.Now()
			for n := 0; n < b.N; n++ {
				var wg sync.WaitGroup
				var firstErr error
				var errMu sync.Mutex
				wg.Add(workers)
				for w := 0; w < workers; w++ {
					w := w
					go func() {
						defer wg.Done()
						for i := w; i < size; i += workers {
							if err := p.HSet("pipeline-size", []byte("k-"+strconv.Itoa(i)), []byte("v")); err != nil {
								errMu.Lock()
								if firstErr == nil {
									firstErr = err
								}
								errMu.Unlock()
								return
							}
						}
					}()
				}
				wg.Wait()
				if firstErr != nil {
					b.Fatal(firstErr)
				}
			}
			b.StopTimer()
			v519ReportItems(b, size, started)
		})
	}
}

func BenchmarkV519PipelineParallelProducers(b *testing.B) {
	for _, workers := range []int{1, 2, 4, 8, 16, 32} {
		b.Run(strconv.Itoa(workers), func(b *testing.B) {
			db := v519OpenBench(b)
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
			for w := 0; w < workers; w++ {
				w := w
				go func() {
					defer wg.Done()
					for i := w; i < b.N; i += workers {
						if err := p.HSet("parallel", []byte("k-"+strconv.Itoa(i)), []byte("v")); err != nil {
							b.Errorf("HSet: %v", err)
							return
						}
					}
				}()
			}
			wg.Wait()
			b.StopTimer()
			elapsed := time.Since(started).Seconds()
			if elapsed > 0 {
				b.ReportMetric(float64(b.N)/elapsed, "ops/s")
			}
		})
	}
}
