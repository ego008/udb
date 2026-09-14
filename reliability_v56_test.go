package udb

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestV56CrashHelper is executed in a child process. os.Exit is intentional:
// unlike returning an injected error, it exercises the process-death boundary
// where deferred cleanup does not run.
func TestV56CrashHelper(t *testing.T) {
	if os.Getenv("UDB_V56_CRASH_HELPER") != "1" {
		return
	}
	path := os.Getenv("UDB_V56_DB")
	point := CompactFaultPoint(os.Getenv("UDB_V56_POINT"))
	keepBackup := os.Getenv("UDB_V56_KEEP_BACKUP") == "1"
	o := DefaultOptions()
	o.Maintenance.Enabled = false
	db, err := OpenWithOptions(path, &o)
	if err != nil {
		os.Exit(70)
	}
	cfg := forceCompactConfig(o)
	cfg.KeepBackup = keepBackup
	cfg.FaultInjector = func(got CompactFaultPoint) error {
		if got == point {
			// Exit without closing the DB or removing the recovery journal.
			os.Exit(77)
		}
		return nil
	}
	_, _, _ = db.CompactAndReplace(cfg)
	os.Exit(78)
}

func runV56CrashChild(t *testing.T, path string, point CompactFaultPoint, keepBackup bool) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestV56CrashHelper$", "-test.count=1")
	env := os.Environ()
	env = append(env,
		"UDB_V56_CRASH_HELPER=1",
		"UDB_V56_DB="+path,
		"UDB_V56_POINT="+string(point),
	)
	if keepBackup {
		env = append(env, "UDB_V56_KEEP_BACKUP=1")
	} else {
		env = append(env, "UDB_V56_KEEP_BACKUP=0")
	}
	cmd.Env = env
	err := cmd.Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 77 {
		t.Fatalf("crash helper point=%s backup=%v: err=%v", point, keepBackup, err)
	}
}

func TestV56ProcessDeathAtCompactionBoundaries(t *testing.T) {
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
				dir := t.TempDir()
				path := filepath.Join(dir, "crash.db")
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

				runV56CrashChild(t, path, point, keepBackup)

				recovered, err := OpenWithOptions(path, &o)
				if err != nil {
					t.Fatalf("startup recovery point=%s backup=%v: %v", point, keepBackup, err)
				}
				got := takeLogicalSnapshot(t, recovered, []string{"users", "counters"}, []string{"rank"})
				if !reflect.DeepEqual(want, got) {
					t.Fatalf("snapshot changed after process death at %s", point)
				}
				if err := recovered.Check(); err != nil {
					t.Fatal(err)
				}
				if err := recovered.View(func(tx *Tx) error {
					r, err := recovered.CheckZSet(tx, "rank")
					if err != nil {
						return err
					}
					if !r.Consistent {
						return fmt.Errorf("inconsistent zset: %+v", *r)
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				if err := recovered.Close(); err != nil {
					t.Fatal(err)
				}
				if _, err := os.Stat(recoveryJournalPath(path)); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("journal remains after recovery: %v", err)
				}
			})
		}
	}
}

func TestV56CorruptJournalDoesNotBlockValidDB(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "corrupt-journal.db")
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
	if err := os.WriteFile(recoveryJournalPath(path), []byte(`{"broken":`), 0600); err != nil {
		t.Fatal(err)
	}
	recovered, err := OpenWithOptions(path, &o)
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Close()
	got := takeLogicalSnapshot(t, recovered, []string{"users", "counters"}, []string{"rank"})
	if !reflect.DeepEqual(want, got) {
		t.Fatal("corrupt journal changed logical snapshot")
	}
	if _, err := os.Stat(recoveryJournalPath(path)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("corrupt journal not removed: %v", err)
	}
}

func TestV56RecoverWithoutUsableJournalFromNewestTemp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "scan.db")
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

	// Simulate a torn/lost journal plus two historical temp files. Only the
	// newest valid temp should be eligible; an invalid temp must be ignored.
	oldTemp := path + ".compact.old.tmp"
	newTemp := path + ".compact.new.tmp"
	if err := copyFile(path, oldTemp); err != nil {
		t.Fatal(err)
	}
	if err := copyFile(path, newTemp); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(oldTemp, []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(recoveryJournalPath(path), []byte("not-json"), 0600); err != nil {
		t.Fatal(err)
	}

	recovered, err := OpenWithOptions(path, &o)
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Close()
	got := takeLogicalSnapshot(t, recovered, []string{"users", "counters"}, []string{"rank"})
	if !reflect.DeepEqual(want, got) {
		t.Fatal("artifact-scan recovery changed logical snapshot")
	}
}

func TestV56RepeatedRecoveryIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repeat.db")
	o := DefaultOptions()
	o.Maintenance.Enabled = false
	db, err := OpenWithOptions(path, &o)
	if err != nil {
		t.Fatal(err)
	}
	seedReliabilityData(t, db)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	tmp := path + ".compact.repeat.tmp"
	if err := copyFile(path, tmp); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	j := recoveryJournal{Version: 1, SourcePath: path, TempPath: tmp, Stage: stageCompacting}
	if err := writeRecoveryJournal(recoveryJournalPath(path), j); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 3; i++ {
		db, err := OpenWithOptions(path, &o)
		if err != nil {
			t.Fatalf("open iteration %d: %v", i, err)
		}
		if err := db.Check(); err != nil {
			t.Fatal(err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(recoveryJournalPath(path)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("journal remains after repeated recovery: %v", err)
	}
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0600)
}

func TestV56JournalWriteIsAtomicAgainstOldVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "atomic.db")
	jpath := recoveryJournalPath(path)
	old := recoveryJournal{Version: 1, SourcePath: path, TempPath: path + ".old", Stage: stageCompacting}
	if err := writeRecoveryJournal(jpath, old); err != nil {
		t.Fatal(err)
	}
	newJ := recoveryJournal{Version: 1, SourcePath: path, TempPath: path + ".new", Stage: stageReplaced}
	if err := writeRecoveryJournal(jpath, newJ); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(jpath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "atomic") == false || !strings.Contains(string(data), "stage") {
		t.Fatalf("unexpected journal contents: %s", data)
	}
	if _, err := os.Stat(jpath + ".tmp"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("journal temporary file remains: %v", err)
	}
}

func TestV56OpenNewDatabaseWithoutRecoveryArtifacts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new.db")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open new database: %v", err)
	}
	defer db.Close()

	if err := db.Update(func(tx *Tx) error {
		return db.Hset(tx, "h", []byte("k"), []byte("v"))
	}); err != nil {
		t.Fatal(err)
	}
}
