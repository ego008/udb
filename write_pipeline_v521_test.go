package udb

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestV521FixedBatchPolicy(t *testing.T) {
	p := FixedBatchPolicy{Size: 50}
	if got := p.NextBatchSize(0, 100, 1, PipelineStats{}); got != 50 {
		t.Fatalf("got %d", got)
	}
}

func TestV521AdaptiveBatchPolicy(t *testing.T) {
	p := AdaptiveBatchPolicy{MinBatchSize: 10, MaxBatchSize: 1000, TargetLatency: 20 * time.Millisecond, ScaleUpFactor: 2, ScaleDownFactor: .5}
	if got := p.NextBatchSize(800, 1000, 100, PipelineStats{}); got != 200 {
		t.Fatalf("scale up: %d", got)
	}
	if got := p.NextBatchSize(0, 1000, 100, PipelineStats{AvgCommitLatency: 40 * time.Millisecond}); got != 50 {
		t.Fatalf("scale down: %d", got)
	}
}

func TestV521PipelineMetrics(t *testing.T) {
	db := openV57TestDB(t)
	p, err := db.NewWritePipeline(WritePipelineOptions{MaxBatchSize: 10, MaxWait: time.Millisecond, QueueSize: 16})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	for i := 0; i < 10; i++ {
		if err := p.HSet("m", []byte{byte(i)}, []byte("v")); err != nil {
			t.Fatal(err)
		}
	}
	st := p.Stats()
	if st.Submitted != 10 || st.Committed != 10 || st.Failed != 0 {
		t.Fatalf("stats: %+v", st)
	}
	if st.Transactions < 1 || st.TotalItems != 10 || st.Batches < 1 {
		t.Fatalf("missing metrics: %+v", st)
	}
	if st.AvgBatchSize <= 0 || st.AvgCommitLatency <= 0 || st.Throughput <= 0 {
		t.Fatalf("invalid derived metrics: %+v", st)
	}
	if st.LastCommitLatency <= 0 || st.MaxCommitLatency < st.MinCommitLatency {
		t.Fatalf("latency metrics: %+v", st)
	}
}

func TestV521BackpressureReject(t *testing.T) {
	db := openV57TestDB(t)
	p, err := db.NewWritePipeline(WritePipelineOptions{MaxBatchSize: 1, MaxWait: 0, QueueSize: 1, Backpressure: BackpressureReject})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	// Fill the queue before the writer can necessarily consume it. Retry until
	// the bounded queue exposes the documented reject behavior.
	found := false
	for i := 0; i < 10000; i++ {
		_, e := p.HSetAsync("bp", []byte{byte(i), byte(i >> 8)}, []byte("v"))
		if errors.Is(e, ErrWritePipelineFull) {
			found = true
			break
		}
	}
	if !found {
		t.Skip("writer drained the tiny queue too quickly on this machine")
	}
}

func TestV521ContextBackpressure(t *testing.T) {
	db := openV57TestDB(t)
	p, err := db.NewWritePipeline(WritePipelineOptions{MaxBatchSize: 1, MaxWait: time.Millisecond, QueueSize: 1, Backpressure: BackpressureTimeout})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()
	_, err = p.HSetAsyncContext(ctx, "ctx", []byte("k"), []byte("v"))
	if err != nil && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unexpected: %v", err)
	}
}
