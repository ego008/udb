package udb

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestZSetAtomicChurn exercises the two-index ZSet implementation under a
// write-heavy workload and verifies that the key index and score index remain
// consistent after many score replacements.
func TestZSetAtomicChurn(t *testing.T) {
	o := DefaultOptions()
	o.Maintenance.Enabled = false
	db, err := OpenWithOptions(t.TempDir()+"/zset-churn.db", &o)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	const baseScore = uint64(1_000_000_000)
	const members = 1000
	const rounds = 20
	name := "hot"

	if err := db.Update(func(tx *Tx) error {
		for i := 0; i < members; i++ {
			if err := db.Zset(tx, name, []byte(fmt.Sprintf("m%04d", i)), baseScore+uint64(i)); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	for round := 0; round < rounds; round++ {
		if err := db.Update(func(tx *Tx) error {
			for i := 0; i < members; i++ {
				score := uint64((round+1)*100000 + (members - i))
				if err := db.Zset(tx, name, []byte(fmt.Sprintf("m%04d", i)), score); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}

	if err := db.Update(func(tx *Tx) error {
		for i := 0; i < members; i++ {
			if err := db.Zset(tx, name, []byte(fmt.Sprintf("m%04d", i)), uint64(100000+i)); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if err := db.View(func(tx *Tx) error {
		r, err := db.CheckZSet(tx, name)
		if err != nil {
			return err
		}
		if !r.Consistent || r.KeyIndexEntries != members || r.ScoreIndexEntries != members {
			return fmt.Errorf("inconsistent zset: %+v", *r)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// TestZSetConcurrentPressure is an opt-in long-running test. Enable it with
// UDB_STRESS=1. It combines concurrent ZSet updates, readers, lifecycle
// quiescence, and compact-and-replace. Run it with -race for the strongest
// validation.
func TestZSetConcurrentPressure(t *testing.T) {
	if os.Getenv("UDB_STRESS") != "1" {
		t.Skip("set UDB_STRESS=1 to run the long-running pressure test")
	}

	o := DefaultOptions()
	o.Maintenance.Enabled = false
	o.Maintenance.MinDBSize = 0
	o.Maintenance.FreeRatio = 0.01
	o.Maintenance.PendingRatio = 0
	o.Maintenance.TxMaxSize = 64 << 10
	o.Maintenance.CheckBeforeCompact = true
	o.Maintenance.CheckAfterCompact = true
	o.Maintenance.KeepBackup = false

	db, err := OpenWithOptions(t.TempDir()+"/pressure.db", &o)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	const members = 2000
	const workers = 8
	const duration = 10 * time.Second
	const baseScore = uint64(1_000_000_000)
	name := "pressure"

	if err := db.Update(func(tx *Tx) error {
		for i := 0; i < members; i++ {
			if err := db.Zset(tx, name, []byte(fmt.Sprintf("m%04d", i)), baseScore+uint64(i)); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()

	var ops atomic.Int64
	var failures atomic.Int64
	var wg sync.WaitGroup

	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			r := rand.New(rand.NewSource(int64(id + 1)))
			for ctx.Err() == nil {
				idx := r.Intn(members)
				key := []byte(fmt.Sprintf("m%04d", idx))
				step := int64(r.Intn(200)) - 100
				if err := db.Update(func(tx *Tx) error {
					_, err := db.Zincr(tx, name, key, step)
					return err
				}); err != nil {
					if ctx.Err() == nil {
						if failures.Add(1) <= 10 {
							t.Logf("writer %d: key=%s step=%d err=%v", id, key, step, err)
						}
					}
					continue
				}
				ops.Add(1)
			}
		}(worker)
	}

	// Readers deliberately overlap writers. They must either complete before
	// quiescence or be admitted again after compaction resumes.
	for worker := 0; worker < 4; worker++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			r := rand.New(rand.NewSource(int64(100 + id)))
			for ctx.Err() == nil {
				key := []byte(fmt.Sprintf("m%04d", r.Intn(members)))
				if err := db.View(func(tx *Tx) error {
					_, _ = db.Zscore(tx, name, key)
					return nil
				}); err != nil && ctx.Err() == nil {
					failures.Add(1)
				}
			}
		}(worker)
	}

	// Compactors intentionally race with continuous traffic. The lifecycle
	// layer must drain admitted operations, block new admission briefly, then
	// reopen the database without losing data.
	compactDone := make(chan struct{})
	go func() {
		defer close(compactDone)
		ticker := time.NewTicker(1500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				cfg := o.Maintenance
				if _, _, err := db.CompactAndReplaceContext(ctx, cfg); err != nil && ctx.Err() == nil {
					// A compaction may legitimately decide that there is not
					// enough reclaimable space. Record only unexpected errors.
					if !isCompactionNotNeeded(err) {
						failures.Add(1)
					}
				}
			}
		}
	}()

	<-ctx.Done()
	wg.Wait()
	<-compactDone

	if failures.Load() != 0 {
		t.Fatalf("pressure failures=%d ops=%d; unexpected operation errors were observed", failures.Load(), ops.Load())
	}
	if ops.Load() == 0 {
		t.Fatal("no write operations completed")
	}

	if err := db.View(func(tx *Tx) error {
		r, err := db.CheckZSet(tx, name)
		if err != nil {
			return err
		}
		if !r.Consistent || r.KeyIndexEntries != members || r.ScoreIndexEntries != members {
			return fmt.Errorf("final zset inconsistency: %+v", *r)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if err := db.Check(); err != nil {
		t.Fatalf("final bolt check failed: %v", err)
	}
}

func isCompactionNotNeeded(err error) bool {
	if err == nil {
		return false
	}
	const prefix = "udb: compaction not needed:"
	return len(err.Error()) >= len(prefix) && err.Error()[:len(prefix)] == prefix
}
