package udb

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// Every injected failure must leave a valid, usable database and must never
// lose the logical Hash/ZSet snapshot. This is intentionally run with both
// backup enabled and disabled because the rollback paths differ.
func TestV55CompactFailureInjection(t *testing.T) {
	points := []CompactFaultPoint{
		FaultAfterCompact,
		FaultBeforeBackup,
		FaultAfterBackup,
		FaultBeforeReplace,
		FaultAfterReplace,
		FaultBeforeReopen,
		FaultAfterReopen,
		FaultAfterCheck,
	}
	for _, keepBackup := range []bool{false, true} {
		for _, point := range points {
			t.Run(fmt.Sprintf("backup=%v/%s", keepBackup, point), func(t *testing.T) {
				o := DefaultOptions()
				o.Maintenance.Enabled = false
				db, err := OpenWithOptions(filepath.Join(t.TempDir(), "fault.db"), &o)
				if err != nil {
					t.Fatal(err)
				}
				seedReliabilityData(t, db)
				want := takeLogicalSnapshot(t, db, []string{"users", "counters"}, []string{"rank"})

				cfg := forceCompactConfig(o)
				cfg.KeepBackup = keepBackup
				cfg.FaultInjector = func(got CompactFaultPoint) error {
					if got == point {
						return errors.New("synthetic failure")
					}
					return nil
				}
				_, _, err = db.CompactAndReplace(cfg)
				if err == nil {
					t.Fatalf("fault %s was not injected", point)
				}

				got := takeLogicalSnapshot(t, db, []string{"users", "counters"}, []string{"rank"})
				if !reflect.DeepEqual(want, got) {
					t.Fatalf("logical snapshot changed after injected failure at %s", point)
				}
				if err := db.Check(); err != nil {
					t.Fatalf("db.Check after %s: %v", point, err)
				}
				if err := db.View(func(tx *Tx) error {
					r, err := db.CheckZSet(tx, "rank")
					if err != nil {
						return err
					}
					if !r.Consistent {
						return fmt.Errorf("zset inconsistent: %+v", *r)
					}
					return nil
				}); err != nil {
					t.Fatalf("CheckZSet after %s: %v", point, err)
				}

				// A failed compact must leave the service usable, not merely readable.
				if err := db.Update(func(tx *Tx) error {
					return db.Hset(tx, "users", []byte("post-failure"), []byte("ok"))
				}); err != nil {
					t.Fatalf("Update after %s: %v", point, err)
				}
				_ = db.Close()
			})
		}
	}
}

// TestV55StartupRecoveryFromTemp simulates a process crash after the compacted
// temp file has been created but before it can be installed as the formal DB.
func TestV55StartupRecoveryFromTemp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "startup.db")
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

	tmp := path + ".compact.crash.tmp"
	if err := os.Rename(path, tmp); err != nil {
		t.Fatal(err)
	}
	journal := recoveryJournal{Version: 1, SourcePath: path, TempPath: tmp, KeepBackup: false, Stage: "compacting"}
	if err := writeRecoveryJournal(recoveryJournalPath(path), journal); err != nil {
		t.Fatal(err)
	}

	recovered, err := OpenWithOptions(path, &o)
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Close()
	got := takeLogicalSnapshot(t, recovered, []string{"users", "counters"}, []string{"rank"})
	if !reflect.DeepEqual(want, got) {
		t.Fatal("startup recovery from temp changed logical data")
	}
	if err := recovered.Check(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(recoveryJournalPath(path)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("recovery journal remains: %v", err)
	}
}

// TestV55StartupRecoveryFromBackup simulates a crash after the original file
// has been renamed to backup but before the compacted temp reaches the formal
// path. The original backup is preferred when no valid temp exists.
func TestV55StartupRecoveryFromBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "startup-backup.db")
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

	backup := path + ".backup-crash"
	if err := os.Rename(path, backup); err != nil {
		t.Fatal(err)
	}
	tmp := path + ".compact.crash.tmp"
	journal := recoveryJournal{Version: 1, SourcePath: path, TempPath: tmp, BackupPath: backup, KeepBackup: true, Stage: "backup_created"}
	if err := writeRecoveryJournal(recoveryJournalPath(path), journal); err != nil {
		t.Fatal(err)
	}

	recovered, err := OpenWithOptions(path, &o)
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Close()
	got := takeLogicalSnapshot(t, recovered, []string{"users", "counters"}, []string{"rank"})
	if !reflect.DeepEqual(want, got) {
		t.Fatal("startup recovery from backup changed logical data")
	}
	if err := recovered.Check(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(recoveryJournalPath(path)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("recovery journal remains: %v", err)
	}
}
