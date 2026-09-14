package udb

import (
	"context"
	"fmt"
	"sync"
	"testing"
)

func TestZincrZeroStepPreservesIndex(t *testing.T) {
	o := DefaultOptions()
	o.Maintenance.Enabled = false
	db, err := OpenWithOptions(t.TempDir()+"/zero.db", &o)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := db.Update(func(tx *Tx) error {
		return db.Zset(tx, "z", []byte("m"), 100)
	}); err != nil {
		t.Fatal(err)
	}

	if err := db.Update(func(tx *Tx) error {
		score, err := db.Zincr(tx, "z", []byte("m"), 0)
		if err != nil {
			return err
		}
		if score != 100 {
			return fmt.Errorf("score=%d, want 100", score)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if err := db.View(func(tx *Tx) error {
		r, err := db.CheckZSet(tx, "z")
		if err != nil {
			return err
		}
		if !r.Consistent || r.KeyIndexEntries != 1 || r.ScoreIndexEntries != 1 {
			return fmt.Errorf("inconsistent zset: %+v", *r)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestZSetConcurrentUpdatesWithoutCompact(t *testing.T) {
	o := DefaultOptions()
	o.Maintenance.Enabled = false
	// This test verifies concurrent ZSet index consistency, not durability.
	// bbolt serializes RW transactions, and forcing an fsync for every tiny
	// transaction makes repeated -race runs unnecessarily slow. Durability is
	// covered by the normal persistence tests.
	o.NoSync = true
	db, err := OpenWithOptions(t.TempDir()+"/nocmp.db", &o)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	const members = 2000
	const workers = 8
	const rounds = 500
	const base = uint64(1_000_000_000)
	name := "z"
	if err := db.Update(func(tx *Tx) error {
		for i := 0; i < members; i++ {
			if err := db.Zset(tx, name, []byte(fmt.Sprintf("m%04d", i)), base+uint64(i)); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for n := 0; n < rounds; n++ {
				idx := (id*rounds + n*17) % members
				step := int64((n+id)%201) - 100
				key := []byte(fmt.Sprintf("m%04d", idx))
				if err := db.Update(func(tx *Tx) error { _, err := db.Zincr(tx, name, key, step); return err }); err != nil {
					t.Errorf("worker=%d round=%d: %v", id, n, err)
					return
				}
			}
		}(w)
	}
	wg.Wait()

	if err := db.View(func(tx *Tx) error {
		r, err := db.CheckZSet(tx, name)
		if err != nil {
			return err
		}
		if !r.Consistent || r.KeyIndexEntries != members || r.ScoreIndexEntries != members {
			return fmt.Errorf("inconsistent: %+v", *r)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestZSetCompactPreservesIndexes(t *testing.T) {
	o := DefaultOptions()
	o.Maintenance.Enabled = false
	o.Maintenance.MinDBSize = 0
	o.Maintenance.FreeRatio = 0
	o.Maintenance.PendingRatio = 1.0
	o.Maintenance.CheckBeforeCompact = true
	o.Maintenance.CheckAfterCompact = true
	o.Maintenance.KeepBackup = false
	o.Maintenance.TxMaxSize = 64 << 10
	db, err := OpenWithOptions(t.TempDir()+"/compact.db", &o)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	const members = 2000
	const base = uint64(1_000_000_000)
	name := "z"
	if err := db.Update(func(tx *Tx) error {
		for i := 0; i < members; i++ {
			if err := db.Zset(tx, name, []byte(fmt.Sprintf("m%04d", i)), base+uint64(i)); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for r := 0; r < 20; r++ {
		if err := db.Update(func(tx *Tx) error {
			for i := 0; i < members; i++ {
				if _, err := db.Zincr(tx, name, []byte(fmt.Sprintf("m%04d", i)), int64((i+r)%101)-50); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	before := 0
	if err := db.View(func(tx *Tx) error {
		r, err := db.CheckZSet(tx, name)
		if err != nil {
			return err
		}
		before = r.KeyIndexEntries
		if !r.Consistent {
			return fmt.Errorf("before compact: %+v", *r)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if before != members {
		t.Fatalf("before=%d", before)
	}
	cfg := o.Maintenance
	ctx := context.Background()
	if _, _, err := db.CompactAndReplaceContext(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if err := db.View(func(tx *Tx) error {
		r, err := db.CheckZSet(tx, name)
		if err != nil {
			return err
		}
		if !r.Consistent || r.KeyIndexEntries != members || r.ScoreIndexEntries != members {
			return fmt.Errorf("after compact: %+v", *r)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
