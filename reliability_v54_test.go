package udb

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
)

// logicalSnapshot captures user-visible Hash/ZSet data. It intentionally does
// not inspect bbolt page layout, freelists, or file offsets: compaction is
// allowed to change those implementation details while preserving data.
type logicalSnapshot struct {
	Hash map[string]map[string][]byte
	ZSet map[string][]Entry
}

func takeLogicalSnapshot(t *testing.T, db *DB, hashes, zsets []string) logicalSnapshot {
	t.Helper()
	s := logicalSnapshot{
		Hash: make(map[string]map[string][]byte, len(hashes)),
		ZSet: make(map[string][]Entry, len(zsets)),
	}
	if err := db.View(func(tx *Tx) error {
		for _, name := range hashes {
			keys, err := db.HKeys(tx, name, 0)
			if err != nil {
				return err
			}
			m := make(map[string][]byte, len(keys))
			for _, key := range keys {
				r := db.Hget(tx, name, key)
				if err := r.ErrOrNil(); err != nil {
					return err
				}
				if len(r.Data) != 1 {
					return fmt.Errorf("hash %q key %q returned %d values", name, key, len(r.Data))
				}
				m[string(key)] = append([]byte(nil), r.Data[0]...)
			}
			s.Hash[name] = m
		}
		for _, name := range zsets {
			entries, err := db.ZRange(tx, name, scoreMin, scoreMax, 0)
			if err != nil {
				return err
			}
			copied := make([]Entry, len(entries))
			for i, e := range entries {
				copied[i] = Entry{Key: cloneBytes(e.Key), Value: cloneBytes(e.Value)}
			}
			s.ZSet[name] = copied
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return s
}

func seedReliabilityData(t *testing.T, db *DB) {
	t.Helper()
	if err := db.Update(func(tx *Tx) error {
		for i := 0; i < 300; i++ {
			key := []byte(fmt.Sprintf("k%04d", i))
			value := []byte(fmt.Sprintf("value-%04d", i))
			if err := db.Hset(tx, "users", key, value); err != nil {
				return err
			}
			if err := db.Zset(tx, "rank", key, uint64(1_000_000+i)); err != nil {
				return err
			}
		}
		for i := 0; i < 80; i++ {
			if _, err := db.Hincr(tx, "counters", []byte(fmt.Sprintf("c%03d", i)), int64(i+1)); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func forceCompactConfig(o Options) MaintenanceConfig {
	cfg := o.Maintenance
	cfg.MinDBSize = 0
	cfg.FreeRatio = 0
	cfg.PendingRatio = 1
	cfg.TxMaxSize = 64 << 10
	cfg.CheckBeforeCompact = true
	cfg.CheckAfterCompact = true
	return cfg
}

// TestV54CompactPreservesFullLogicalSnapshot verifies the strongest useful
// compaction invariant: all user-visible Hash and ZSet data is identical before
// and after compact-and-replace.
func TestV54CompactPreservesFullLogicalSnapshot(t *testing.T) {
	o := DefaultOptions()
	o.Maintenance.Enabled = false
	db, err := OpenWithOptions(filepath.Join(t.TempDir(), "snapshot.db"), &o)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	seedReliabilityData(t, db)
	hashes := []string{"users", "counters"}
	zsets := []string{"rank"}
	before := takeLogicalSnapshot(t, db, hashes, zsets)

	cfg := forceCompactConfig(o)
	if _, _, err := db.CompactAndReplace(cfg); err != nil {
		t.Fatal(err)
	}
	after := takeLogicalSnapshot(t, db, hashes, zsets)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("logical snapshot changed after compact:\nbefore=%+v\nafter=%+v", before, after)
	}
	if err := db.Check(); err != nil {
		t.Fatalf("bbolt check after compact: %v", err)
	}
}

// TestV54RepeatedCompactIsIdempotent checks that repeatedly compacting an
// already compacted database does not change logical data or make the database
// unusable. File size is intentionally not asserted because allocator details
// are implementation-dependent.
func TestV54RepeatedCompactIsIdempotent(t *testing.T) {
	o := DefaultOptions()
	o.Maintenance.Enabled = false
	db, err := OpenWithOptions(filepath.Join(t.TempDir(), "repeat.db"), &o)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	seedReliabilityData(t, db)
	hashes := []string{"users", "counters"}
	zsets := []string{"rank"}
	want := takeLogicalSnapshot(t, db, hashes, zsets)
	cfg := forceCompactConfig(o)
	cfg.KeepBackup = false

	for i := 0; i < 3; i++ {
		if _, _, err := db.CompactAndReplace(cfg); err != nil {
			t.Fatalf("compact round %d: %v", i+1, err)
		}
		got := takeLogicalSnapshot(t, db, hashes, zsets)
		if !reflect.DeepEqual(want, got) {
			t.Fatalf("snapshot changed after compact round %d", i+1)
		}
	}
	if err := db.Check(); err != nil {
		t.Fatal(err)
	}
}

// TestV54FailedCompactLeavesSourceUsable validates failure containment. An
// existing directory is rejected explicitly by CompactTo, and the source
// database must remain intact.
func TestV54FailedCompactLeavesSourceUsable(t *testing.T) {
	o := DefaultOptions()
	o.Maintenance.Enabled = false
	db, err := OpenWithOptions(filepath.Join(t.TempDir(), "failed.db"), &o)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	seedReliabilityData(t, db)

	before := takeLogicalSnapshot(t, db, []string{"users", "counters"}, []string{"rank"})
	dst := filepath.Join(t.TempDir(), "destination-dir")
	if err := os.Mkdir(dst, 0755); err != nil {
		t.Fatal(err)
	}
	_, err = db.CompactTo(dst, forceCompactConfig(o))
	if err == nil {
		t.Fatal("CompactTo unexpectedly succeeded using a directory as destination")
	}

	after := takeLogicalSnapshot(t, db, []string{"users", "counters"}, []string{"rank"})
	if !reflect.DeepEqual(before, after) {
		t.Fatal("source logical data changed after failed CompactTo")
	}
	if err := db.Check(); err != nil {
		t.Fatalf("source database failed check after failed CompactTo: %v", err)
	}
}

// TestV54BackupAndRollback validates that the backup produced by
// CompactAndReplace is independently readable and can be used to restore the
// original database after the compacted file is closed.
func TestV54BackupAndRollback(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rollback.db")
	o := DefaultOptions()
	o.Maintenance.Enabled = false
	db, err := OpenWithOptions(path, &o)
	if err != nil {
		t.Fatal(err)
	}
	seedReliabilityData(t, db)
	want := takeLogicalSnapshot(t, db, []string{"users", "counters"}, []string{"rank"})

	cfg := forceCompactConfig(o)
	cfg.KeepBackup = true
	_, backup, err := db.CompactAndReplace(cfg)
	if err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if backup == "" {
		_ = db.Close()
		t.Fatal("CompactAndReplace did not return backup path")
	}
	if err := CheckFile(backup); err != nil {
		db.Close()
		t.Fatalf("backup CheckFile: %v", err)
	}

	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	moved := path + ".rollback-test-current"
	if err := os.Rename(path, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(backup, path); err != nil {
		_ = os.Rename(moved, path)
		t.Fatal(err)
	}

	restored, err := OpenWithOptions(path, &o)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	got := takeLogicalSnapshot(t, restored, []string{"users", "counters"}, []string{"rank"})
	if !reflect.DeepEqual(want, got) {
		t.Fatal("restored backup does not match original logical snapshot")
	}
	if err := restored.Check(); err != nil {
		t.Fatalf("restored backup check: %v", err)
	}
	_ = os.Remove(moved)
}

// TestV54RepairZSetAfterInjectedIndexCorruption intentionally removes one
// secondary index entry. CheckZSet must detect it and RepairZSet must rebuild
// the index from the authoritative member->score index.
func TestV54RepairZSetAfterInjectedIndexCorruption(t *testing.T) {
	o := DefaultOptions()
	o.Maintenance.Enabled = false
	db, err := OpenWithOptions(filepath.Join(t.TempDir(), "repair.db"), &o)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	seedReliabilityData(t, db)

	if err := db.Update(func(tx *Tx) error {
		kb := tx.bucket(bucketName(zetKeyPrefix, "rank"))
		if kb == nil {
			return errors.New("rank key index bucket missing")
		}
		return kb.Delete(Bconcat(I2b(1_000_100), []byte("k0100")))
	}); err != nil {
		t.Fatal(err)
	}

	if err := db.View(func(tx *Tx) error {
		r, err := db.CheckZSet(tx, "rank")
		if err != nil {
			return err
		}
		if r.Consistent || r.MissingKeyIndex != 1 {
			return fmt.Errorf("corruption was not detected: %+v", *r)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if err := db.Update(func(tx *Tx) error { return db.RepairZSet(tx, "rank") }); err != nil {
		t.Fatal(err)
	}
	if err := db.View(func(tx *Tx) error {
		r, err := db.CheckZSet(tx, "rank")
		if err != nil {
			return err
		}
		if !r.Consistent || r.KeyIndexEntries != 300 || r.ScoreIndexEntries != 300 {
			return fmt.Errorf("repair failed: %+v", *r)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// TestV54MixedHashZSetPressure verifies that Hash and ZSet mutations in the
// same transaction remain consistent under concurrent application traffic.
func TestV54MixedHashZSetPressure(t *testing.T) {
	o := DefaultOptions()
	o.Maintenance.Enabled = false
	o.NoSync = true
	db, err := OpenWithOptions(filepath.Join(t.TempDir(), "mixed.db"), &o)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	const workers = 12
	const rounds = 250
	var wg sync.WaitGroup
	var failures int
	var mu sync.Mutex
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				key := []byte(fmt.Sprintf("w%02d-k%03d", worker, i%50))
				err := db.Update(func(tx *Tx) error {
					if err := db.Hset(tx, "mixed-h", key, []byte("v")); err != nil {
						return err
					}
					_, err := db.Zincr(tx, "mixed-z", key, 1)
					return err
				})
				if err != nil {
					mu.Lock()
					failures++
					mu.Unlock()
					return
				}
			}
		}(w)
	}
	wg.Wait()
	if failures != 0 {
		t.Fatalf("mixed pressure failures=%d", failures)
	}

	if err := db.View(func(tx *Tx) error {
		for _, name := range []string{"mixed-z"} {
			r, err := db.CheckZSet(tx, name)
			if err != nil {
				return err
			}
			if !r.Consistent {
				return fmt.Errorf("zset %q inconsistent: %+v", name, *r)
			}
		}
		if n, err := db.HLen(tx, "mixed-h"); err != nil || n != workers*50 {
			return fmt.Errorf("hash length=%d err=%v", n, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// TestV54CompactAndCloseRace validates that lifecycle operations are serialized
// safely. Either operation may win: compact may finish before Close, or Close
// may close the DB before compaction gets admission. Neither outcome may panic,
// corrupt the file, or leave a successful Close followed by a usable View.
func TestV54CompactAndCloseRace(t *testing.T) {
	for round := 0; round < 10; round++ {
		o := DefaultOptions()
		o.Maintenance.Enabled = false
		db, err := OpenWithOptions(filepath.Join(t.TempDir(), fmt.Sprintf("race-%02d.db", round)), &o)
		if err != nil {
			t.Fatal(err)
		}
		seedReliabilityData(t, db)
		cfg := forceCompactConfig(o)
		cfg.KeepBackup = false

		start := make(chan struct{})
		compactDone := make(chan error, 1)
		closeDone := make(chan error, 1)
		go func() {
			<-start
			_, _, err := db.CompactAndReplaceContext(nil, cfg)
			compactDone <- err
		}()
		go func() {
			<-start
			closeDone <- db.Close()
		}()
		close(start)

		compactErr := <-compactDone
		closeErr := <-closeDone
		if closeErr != nil {
			t.Fatalf("round %d Close: %v", round, closeErr)
		}
		if compactErr != nil && !errors.Is(compactErr, ErrDatabaseClosed) && !errors.Is(compactErr, context.Canceled) && !errors.Is(compactErr, context.DeadlineExceeded) {
			t.Fatalf("round %d unexpected compact error: %v", round, compactErr)
		}
		if err := db.View(func(tx *Tx) error { return nil }); !errors.Is(err, ErrDatabaseClosed) {
			t.Fatalf("round %d View after Close=%v", round, err)
		}
	}
}

// TestV54CloseReopenPersistence exercises the normal durability path with the
// default NoSync=false option: write, close, reopen, and verify the complete
// logical snapshot.
func TestV54CloseReopenPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "persist.db")
	o := DefaultOptions()
	o.Maintenance.Enabled = false
	db, err := OpenWithOptions(path, &o)
	if err != nil {
		t.Fatal(err)
	}
	seedReliabilityData(t, db)
	want := takeLogicalSnapshot(t, db, []string{"users", "counters"}, []string{"rank"})
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = OpenWithOptions(path, &o)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	got := takeLogicalSnapshot(t, db, []string{"users", "counters"}, []string{"rank"})
	if !reflect.DeepEqual(want, got) {
		t.Fatal("logical snapshot changed across close/reopen")
	}
	if err := db.Check(); err != nil {
		t.Fatal(err)
	}
}
