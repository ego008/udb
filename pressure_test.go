package udb

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestLifecycleDrainBlocksNewOperations verifies the intended semantics:
// normal operations remain concurrent, but once Compact/Close starts draining,
// new managed operations wait instead of racing with the lifecycle operation.
func TestLifecycleDrainBlocksNewOperations(t *testing.T) {
	o := DefaultOptions()
	o.Maintenance.Enabled = false
	db, err := OpenWithOptions(t.TempDir()+"/drain.db", &o)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	started := make(chan struct{})
	release := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- db.View(func(tx *Tx) error {
			close(started)
			<-release
			return nil
		})
	}()
	<-started

	// Start a lifecycle operation in the background. It must wait for the
	// already-running View rather than closing the database underneath it.
	closeDone := make(chan error, 1)
	go func() { closeDone <- db.Close() }()

	// Give Close a chance to enter quiescing.
	time.Sleep(20 * time.Millisecond)

	// A new operation must not enter while Close is draining.
	newStarted := make(chan struct{})
	newDone := make(chan error, 1)
	go func() {
		newDone <- db.View(func(tx *Tx) error {
			close(newStarted)
			return nil
		})
	}()

	select {
	case <-newStarted:
		t.Fatal("new View entered while Close was draining")
	case <-time.After(30 * time.Millisecond):
	}

	close(release)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	if err := <-closeDone; err != nil {
		t.Fatal(err)
	}
	if err := <-newDone; !errors.Is(err, ErrDatabaseClosed) {
		t.Fatalf("new View after Close = %v, want ErrDatabaseClosed", err)
	}
}

func TestCompactContextCancellationRestoresAdmission(t *testing.T) {
	o := DefaultOptions()
	o.Maintenance.Enabled = false
	db, err := OpenWithOptions(t.TempDir()+"/compact-cancel.db", &o)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	started := make(chan struct{})
	release := make(chan struct{})
	viewDone := make(chan error, 1)
	go func() {
		viewDone <- db.View(func(tx *Tx) error {
			close(started)
			<-release
			return nil
		})
	}()
	<-started

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	cfg := o.Maintenance
	cfg.MinDBSize = 0
	cfg.FreeRatio = 0
	cfg.PendingRatio = 1
	cfg.CheckBeforeCompact = false
	cfg.CheckAfterCompact = false
	cfg.KeepBackup = false

	_, _, err = db.CompactAndReplaceContext(ctx, cfg)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("CompactAndReplaceContext error = %v, want deadline exceeded", err)
	}

	// Admission must be restored after cancellation even though the original
	// View is still active.
	quickDone := make(chan error, 1)
	go func() { quickDone <- db.View(func(tx *Tx) error { return nil }) }()
	select {
	case err := <-quickDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("admission was not restored after cancelled compaction")
	}

	close(release)
	if err := <-viewDone; err != nil {
		t.Fatal(err)
	}
}

// TestConcurrentMixedPressure runs a moderate concurrent workload. It is
// intentionally a regular test (rather than a benchmark) so -race can catch
// lifecycle/data races in CI and during development.
func TestConcurrentMixedPressure(t *testing.T) {
	o := DefaultOptions()
	o.Maintenance.Enabled = false
	db, err := OpenWithOptions(t.TempDir()+"/pressure.db", &o)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	const workers = 32
	const opsPerWorker = 300
	var operations atomic.Int64
	var failures atomic.Int64
	var wg sync.WaitGroup
	start := time.Now()

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < opsPerWorker; i++ {
				key := []byte(fmt.Sprintf("w%d-k%d", worker, i%32))
				if i%3 == 0 {
					if err := db.Update(func(tx *Tx) error {
						if err := db.Hset(tx, "pressure-h", key, []byte("value")); err != nil {
							return err
						}
						_, err := db.Zincr(tx, "pressure-z", key, 1)
						return err
					}); err != nil {
						failures.Add(1)
					}
				} else {
					if err := db.View(func(tx *Tx) error {
						_ = db.Hget(tx, "pressure-h", key)
						_ = db.Zget(tx, "pressure-z", key)
						return nil
					}); err != nil {
						failures.Add(1)
					}
				}
				operations.Add(1)
			}
		}(w)
	}
	wg.Wait()

	if failures.Load() != 0 {
		t.Fatalf("pressure workload had %d failed operations", failures.Load())
	}
	want := int64(workers * opsPerWorker)
	if got := operations.Load(); got != want {
		t.Fatalf("operations=%d want=%d", got, want)
	}
	t.Logf("mixed pressure: operations=%d duration=%s ops/s=%.0f", operations.Load(), time.Since(start), float64(operations.Load())/time.Since(start).Seconds())
}

func TestConcurrentOperationsAndRepeatedClose(t *testing.T) {
	const rounds = 10
	for round := 0; round < rounds; round++ {
		o := DefaultOptions()
		o.Maintenance.Enabled = false
		db, err := OpenWithOptions(t.TempDir()+fmt.Sprintf("/round-%d.db", round), &o)
		if err != nil {
			t.Fatal(err)
		}

		var wg sync.WaitGroup
		for i := 0; i < 16; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				for j := 0; j < 50; j++ {
					if j%2 == 0 {
						_ = db.HSet("h", []byte(fmt.Sprintf("%d-%d", id, j)), []byte("v"))
					} else {
						_ = db.View(func(tx *Tx) error { return nil })
					}
				}
			}(i)
		}

		// Close concurrently with the workload. It must wait for admitted
		// operations and never panic or race with bbolt DB replacement/close.
		closeErr := make(chan error, 1)
		go func() { closeErr <- db.Close() }()
		wg.Wait()
		if err := <-closeErr; err != nil {
			t.Fatalf("round %d Close: %v", round, err)
		}
	}
}
