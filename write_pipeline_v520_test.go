package udb

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestV520AsyncSubmitDoesNotWaitForCommit(t *testing.T) {
	db := openV519TestDB(t)
	defer db.Close()

	p, err := db.NewWritePipeline(WritePipelineOptions{MaxBatchSize: 100, MaxWait: 20 * time.Millisecond, QueueSize: 128})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	f, err := p.HSetAsync("async", []byte("k"), []byte("v"))
	if err != nil {
		t.Fatal(err)
	}
	if f == nil || f.Seq() == 0 {
		t.Fatalf("invalid future: %#v", f)
	}
	if st := p.Stats(); st.Submitted != 1 {
		t.Fatalf("unexpected submitted count: %+v", st)
	}
	if err := f.Wait(); err != nil {
		t.Fatal(err)
	}
	if got := db.HGet("async", []byte("k")); got.State != replyOK || string(got.Data[0]) != "v" {
		t.Fatalf("write not committed: %+v", got)
	}
}

func TestV520AsyncStrictOrder(t *testing.T) {
	db := openV519TestDB(t)
	defer db.Close()
	p, err := db.NewWritePipeline(WritePipelineOptions{MaxBatchSize: 100, MaxWait: 10 * time.Millisecond, QueueSize: 128})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	fs := make([]*WriteFuture, 0, 3)
	f, err := p.HSetAsync("async-order", []byte("k"), []byte("v1"))
	if err != nil {
		t.Fatal(err)
	}
	fs = append(fs, f)
	f, err = p.HDelAsync("async-order", []byte("k"))
	if err != nil {
		t.Fatal(err)
	}
	fs = append(fs, f)
	f, err = p.HSetAsync("async-order", []byte("k"), []byte("v2"))
	if err != nil {
		t.Fatal(err)
	}
	fs = append(fs, f)
	if fs[0].Seq() >= fs[1].Seq() || fs[1].Seq() >= fs[2].Seq() {
		t.Fatalf("sequence order broken: %d %d %d", fs[0].Seq(), fs[1].Seq(), fs[2].Seq())
	}
	if err := p.Flush(); err != nil {
		t.Fatal(err)
	}
	for _, f := range fs {
		if err := f.Wait(); err != nil {
			t.Fatal(err)
		}
	}
	got := db.HGet("async-order", []byte("k"))
	if got.State != replyOK || string(got.Data[0]) != "v2" {
		t.Fatalf("unexpected final state: %+v", got)
	}
}

func TestV520AsyncContextWaitCancellation(t *testing.T) {
	db := openV519TestDB(t)
	defer db.Close()
	p, err := db.NewWritePipeline(WritePipelineOptions{MaxBatchSize: 1, MaxWait: 0, QueueSize: 16})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	f, err := p.HSetAsync("async-cancel", []byte("k"), []byte("v"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := f.WaitContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
	if err := f.Wait(); err != nil {
		t.Fatal(err)
	}
}

func TestV520AsyncInputCopy(t *testing.T) {
	db := openV519TestDB(t)
	defer db.Close()
	p, err := db.NewWritePipeline(WritePipelineOptions{MaxBatchSize: 100, MaxWait: 10 * time.Millisecond, QueueSize: 16})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	key, value := []byte("key"), []byte("value")
	f, err := p.HSetAsync("async-copy", key, value)
	if err != nil {
		t.Fatal(err)
	}
	key[0], value[0] = 'X', 'X'
	if err := f.Wait(); err != nil {
		t.Fatal(err)
	}
	got := db.HGet("async-copy", []byte("key"))
	if got.State != replyOK || string(got.Data[0]) != "value" {
		t.Fatalf("input buffers were not copied: %+v", got)
	}
}

func TestV520AsyncConcurrentProducers(t *testing.T) {
	db := openV519TestDB(t)
	defer db.Close()
	p, err := db.NewWritePipeline(WritePipelineOptions{MaxBatchSize: 100, MaxWait: 5 * time.Millisecond, QueueSize: 1024})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	const workers, perWorker = 16, 50
	futures := make(chan *WriteFuture, workers*perWorker)
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		w := w
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				f, err := p.HSetAsync("async-concurrent", []byte(testKey(w, i)), []byte("ok"))
				if err != nil {
					errs <- err
					return
				}
				futures <- f
			}
		}()
	}
	wg.Wait()
	close(errs)
	close(futures)
	for err := range errs {
		t.Fatal(err)
	}
	for f := range futures {
		if err := f.Wait(); err != nil {
			t.Fatal(err)
		}
	}
	st := p.Stats()
	want := uint64(workers * perWorker)
	if st.Submitted != want || st.Committed != want || st.Failed != 0 {
		t.Fatalf("unexpected stats: %+v want=%d", st, want)
	}
}

func TestV520AsyncBatchAtomicFailure(t *testing.T) {
	db := openV519TestDB(t)
	defer db.Close()
	p, err := db.NewWritePipeline(WritePipelineOptions{MaxBatchSize: 16, MaxWait: 10 * time.Millisecond, QueueSize: 32})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	// Inject an invalid internal operation after a valid write so the writer
	// mutates the transaction and then returns an error. bbolt must roll back
	// the entire batch, and every future in that batch receives the same error.
	f1, err := p.submitAsync(context.Background(), &pipelineRequest{
		op: pipelineHSet, name: "atomic", key: []byte("a"), value: []byte("1"), result: make(chan error, 1),
	})
	if err != nil {
		t.Fatal(err)
	}
	f2, err := p.submitAsync(context.Background(), &pipelineRequest{
		op: pipelineOp(255), name: "atomic", key: []byte("bad"), result: make(chan error, 1),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Flush(); err != nil {
		t.Fatal(err)
	}
	err1 := f1.Wait()
	err2 := f2.Wait()
	if err1 == nil || err2 == nil || err1.Error() != err2.Error() {
		t.Fatalf("expected identical batch errors: err1=%v err2=%v", err1, err2)
	}
	got := db.HGet("atomic", []byte("a"))
	if got.State != bucketNotFound && got.State != keyNotFound {
		t.Fatalf("partial write survived failed batch: %+v", got)
	}
	st := p.Stats()
	if st.Submitted != 2 || st.Committed != 0 || st.Failed != 2 || st.Transactions != 1 {
		t.Fatalf("unexpected failed-batch stats: %+v", st)
	}
}
