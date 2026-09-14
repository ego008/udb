package udb

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func openV519TestDB(t *testing.T) *DB {
	t.Helper()
	o := DefaultOptions()
	o.Maintenance.Enabled = false
	db, err := OpenWithOptions(filepath.Join(t.TempDir(), "test.db"), &o)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func TestV519PipelineBasicBatching(t *testing.T) {
	db := openV519TestDB(t)
	defer db.Close()

	p, err := db.NewWritePipeline(WritePipelineOptions{MaxBatchSize: 4, MaxWait: time.Millisecond, QueueSize: 16})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.HSet("h", []byte("a"), []byte("1")); err != nil {
		t.Fatal(err)
	}
	if err := p.ZSet("z", []byte("m"), 42); err != nil {
		t.Fatal(err)
	}
	if err := p.HDel("h", []byte("a")); err != nil {
		t.Fatal(err)
	}
	if err := p.Flush(); err != nil {
		t.Fatal(err)
	}
	st := p.Stats()
	if st.Submitted != 3 || st.Committed != 3 || st.Failed != 0 {
		t.Fatalf("unexpected stats: %+v", st)
	}
	if st.Transactions < 1 || st.Transactions > 3 {
		t.Fatalf("unexpected transaction count: %+v", st)
	}
	if got := db.HGet("h", []byte("a")); got.State != keyNotFound {
		t.Fatalf("HDel did not take effect: %+v", got)
	}
	got := db.ZGet("z", []byte("m"))
	if got.State != replyOK || len(got.Data) != 1 || len(got.Data[0]) != 8 || binaryScore(got.Data[0]) != 42 {
		t.Fatalf("unexpected ZGet: %+v", got)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if err := p.HSet("h", []byte("b"), []byte("2")); !errors.Is(err, ErrWritePipelineClosed) {
		t.Fatalf("expected closed error, got %v", err)
	}
}

func TestV519PipelineStrictOrder(t *testing.T) {
	db := openV519TestDB(t)
	defer db.Close()
	p, err := db.NewWritePipeline(WritePipelineOptions{MaxBatchSize: 64, MaxWait: 10 * time.Millisecond, QueueSize: 64})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	if err := p.HSet("order", []byte("k"), []byte("v1")); err != nil {
		t.Fatal(err)
	}
	if err := p.HDel("order", []byte("k")); err != nil {
		t.Fatal(err)
	}
	if err := p.HSet("order", []byte("k"), []byte("v2")); err != nil {
		t.Fatal(err)
	}
	if err := p.Flush(); err != nil {
		t.Fatal(err)
	}
	got := db.HGet("order", []byte("k"))
	if got.State != replyOK || string(got.Data[0]) != "v2" {
		t.Fatalf("order was not preserved: %+v", got)
	}
}

func TestV519PipelineCopiesInputBuffers(t *testing.T) {
	db := openV519TestDB(t)
	defer db.Close()
	p, err := db.NewWritePipeline(WritePipelineOptions{MaxBatchSize: 100, MaxWait: 20 * time.Millisecond, QueueSize: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	key := []byte("key")
	value := []byte("value")
	if err := p.HSet("copy", key, value); err != nil {
		t.Fatal(err)
	}
	key[0] = 'X'
	value[0] = 'X'
	got := db.HGet("copy", []byte("key"))
	if got.State != replyOK || string(got.Data[0]) != "value" {
		t.Fatalf("input buffer was not copied: %+v", got)
	}
}

func TestV519PipelineConcurrentProducers(t *testing.T) {
	db := openV519TestDB(t)
	defer db.Close()
	p, err := db.NewWritePipeline(WritePipelineOptions{MaxBatchSize: 32, MaxWait: 2 * time.Millisecond, QueueSize: 32})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	const workers = 8
	const perWorker = 25
	var wg sync.WaitGroup
	errCh := make(chan error, workers)
	for w := 0; w < workers; w++ {
		w := w
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				if err := p.HSet("concurrent", []byte(testKey(w, i)), []byte("ok")); err != nil {
					errCh <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}
	if err := p.Flush(); err != nil {
		t.Fatal(err)
	}
	st := p.Stats()
	want := uint64(workers * perWorker)
	if st.Submitted != want || st.Committed != want || st.Failed != 0 {
		t.Fatalf("unexpected stats: %+v want=%d", st, want)
	}
}

func TestV519PipelineContextCancellationBeforeSubmit(t *testing.T) {
	db := openV519TestDB(t)
	defer db.Close()
	p, err := db.NewWritePipeline(DefaultWritePipelineOptions())
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := p.HSetContext(ctx, "cancel", []byte("k"), []byte("v")); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
	if got := p.Stats().Submitted; got != 0 {
		t.Fatalf("canceled request was submitted: %d", got)
	}
}

func TestV519PipelineFlushBarrier(t *testing.T) {
	db := openV519TestDB(t)
	defer db.Close()
	p, err := db.NewWritePipeline(WritePipelineOptions{MaxBatchSize: 100, MaxWait: 100 * time.Millisecond, QueueSize: 16})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	if err := p.HSet("flush", []byte("k"), []byte("v")); err != nil {
		t.Fatal(err)
	}
	if err := p.Flush(); err != nil {
		t.Fatal(err)
	}
	got := db.HGet("flush", []byte("k"))
	if got.State != replyOK || string(got.Data[0]) != "v" {
		t.Fatalf("flush returned before write committed: %+v", got)
	}
}

func TestV519PipelineCloseDrains(t *testing.T) {
	db := openV519TestDB(t)
	defer db.Close()
	p, err := db.NewWritePipeline(WritePipelineOptions{MaxBatchSize: 50, MaxWait: 20 * time.Millisecond, QueueSize: 128})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		if err := p.HSet("close", []byte(testKey(i, 0)), []byte("ok")); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	st := p.Stats()
	if st.Committed != 100 || st.Failed != 0 {
		t.Fatalf("Close did not drain all requests: %+v", st)
	}
}

func TestV519PipelineConcurrentFinalState(t *testing.T) {
	db := openV519TestDB(t)
	defer db.Close()
	p, err := db.NewWritePipeline(WritePipelineOptions{MaxBatchSize: 16, MaxWait: time.Millisecond, QueueSize: 64})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	const workers = 4
	const rounds = 10
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		w := w
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				if err := p.ZSet("state", []byte(fmt.Sprintf("m-%d-%d", w, i)), uint64(i)); err != nil {
					t.Errorf("ZSet: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
	if err := p.Flush(); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.View(func(tx *Tx) error {
		var err error
		count, err = db.ZCard(tx, "state")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if count != workers*rounds {
		t.Fatalf("unexpected ZSet count: got=%d want=%d", count, workers*rounds)
	}
}

func testKey(a, b int) string {
	return fmt.Sprintf("%d-%d", a, b)
}
