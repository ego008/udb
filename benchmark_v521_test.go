package udb

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"
)

func BenchmarkV521AsyncFixed100(b *testing.B) {
	db := openV57TestDB(b)
	p, err := db.NewWritePipeline(WritePipelineOptions{MaxBatchSize: 100, MaxWait: 5 * time.Millisecond, QueueSize: 1024})
	if err != nil {
		b.Fatal(err)
	}
	defer p.Close()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		futures := make([]*WriteFuture, 100)
		for j := range futures {
			key := []byte("k-" + strconv.Itoa(i) + "-" + strconv.Itoa(j))
			futures[j], err = p.HSetAsync("bench-v521", key, []byte("v"))
			if err != nil {
				b.Fatal(err)
			}
		}
		if err := p.Flush(); err != nil {
			b.Fatal(err)
		}
		for _, f := range futures {
			if err := f.Wait(); err != nil {
				b.Fatal(err)
			}
		}
	}
	b.ReportMetric(float64(b.N*100)/b.Elapsed().Seconds(), "items/s")
}

func BenchmarkV521AdaptivePipeline(b *testing.B) {
	db := openV57TestDB(b)
	policy := DefaultAdaptiveBatchPolicy()
	p, err := db.NewWritePipeline(WritePipelineOptions{MaxBatchSize: 1000, MaxWait: 5 * time.Millisecond, QueueSize: 4096, BatchPolicy: policy})
	if err != nil {
		b.Fatal(err)
	}
	defer p.Close()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		f, err := p.HSetAsync("adaptive", []byte(strconv.Itoa(i)), []byte("v"))
		if err != nil {
			b.Fatal(err)
		}
		if i%100 == 99 {
			if err := f.Wait(); err != nil {
				b.Fatal(err)
			}
		}
	}
	if err := p.Flush(); err != nil {
		b.Fatal(err)
	}
	b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "items/s")
}

func BenchmarkV521AsyncParallelProducers(b *testing.B) {
	for _, workers := range []int{1, 2, 4, 8, 16, 32} {
		b.Run(strconv.Itoa(workers), func(b *testing.B) {
			db := openV57TestDB(b)
			p, err := db.NewWritePipeline(WritePipelineOptions{MaxBatchSize: 100, MaxWait: 5 * time.Millisecond, QueueSize: 4096})
			if err != nil {
				b.Fatal(err)
			}
			defer p.Close()
			b.ResetTimer()
			var wg sync.WaitGroup
			for w := 0; w < workers; w++ {
				wg.Add(1)
				go func(worker int) {
					defer wg.Done()
					for i := worker; i < b.N; i += workers {
						_, err := p.HSetAsyncContext(context.Background(), "parallel", []byte(strconv.Itoa(i)), []byte("v"))
						if err != nil {
							b.Error(err)
							return
						}
					}
				}(w)
			}
			wg.Wait()
			if err := p.Flush(); err != nil {
				b.Fatal(err)
			}
			b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "items/s")
		})
	}
}
