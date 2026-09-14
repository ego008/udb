package udb

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

// TestV510CrashHelper exits exactly at a requested compaction boundary. The
// purpose is to model process death, not merely an injected returned error.
func TestV510CrashHelper(t *testing.T) {
	if os.Getenv("UDB_V510_CRASH_HELPER") != "1" {
		return
	}
	path := os.Getenv("UDB_V510_DB")
	point := CompactFaultPoint(os.Getenv("UDB_V510_POINT"))
	keepBackup := os.Getenv("UDB_V510_KEEP_BACKUP") == "1"

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
			// Deliberately bypass deferred cleanup and Close().
			os.Exit(77)
		}
		return nil
	}
	_, _, _ = db.CompactAndReplace(cfg)
	os.Exit(78)
}

func runV510CrashChild(t *testing.T, path string, point CompactFaultPoint, keepBackup bool) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestV510CrashHelper$", "-test.count=1")
	env := os.Environ()
	env = append(env,
		"UDB_V510_CRASH_HELPER=1",
		"UDB_V510_DB="+path,
		"UDB_V510_POINT="+string(point),
	)
	if keepBackup {
		env = append(env, "UDB_V510_KEEP_BACKUP=1")
	} else {
		env = append(env, "UDB_V510_KEEP_BACKUP=0")
	}
	cmd.Env = env
	err := cmd.Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 77 {
		t.Fatalf("crash helper point=%s backup=%v: err=%v", point, keepBackup, err)
	}
}

func TestV510CompactionCrashRecoveryMatrix(t *testing.T) {
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
				path := filepath.Join(dir, "matrix.db")
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

				runV510CrashChild(t, path, point, keepBackup)

				// Audit the crash state before Open(). This verifies that the
				// persisted evidence is deterministic at every destructive
				// boundary and that diagnostics themselves remain read-only.
				report, err := InspectRecovery(path, o)
				if err != nil {
					t.Fatal(err)
				}
				expectedDecision := RecoveryDecisionClean
				if point == FaultAfterBackup || point == FaultBeforeReplace {
					expectedDecision = RecoveryDecisionManifest
				}
				if report.Decision != expectedDecision || report.FailClosed {
					t.Fatalf("unexpected pre-open recovery state point=%s backup=%v: decision=%s reason=%s report=%+v", point, keepBackup, report.Decision, report.Reason, report)
				}

				recovered, err := OpenWithOptions(path, &o)
				if err != nil {
					t.Fatalf("startup recovery failed point=%s backup=%v: %v", point, keepBackup, err)
				}
				got := takeLogicalSnapshot(t, recovered, []string{"users", "counters"}, []string{"rank"})
				if !reflect.DeepEqual(want, got) {
					t.Fatalf("logical snapshot changed after process death at %s", point)
				}
				if err := recovered.Check(); err != nil {
					t.Fatal(err)
				}
				if _, err := recovered.CheckIntegrity(); err != nil {
					t.Fatal(err)
				}
				if err := recovered.Close(); err != nil {
					t.Fatal(err)
				}

				if _, err := os.Stat(recoveryJournalPath(path)); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("journal remains after recovery: %v", err)
				}
				if _, err := os.Stat(recoveryManifestPath(path)); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("manifest remains after recovery: %v", err)
				}
				if !keepBackup {
					_, backups := recoveryArtifacts(path)
					if len(backups) != 0 {
						t.Fatalf("unexpected backup remains with KeepBackup=false: %v", backups)
					}
				}
			})
		}
	}
}

// TestV510RecoveryMatrixReadOnlyAudit verifies that InspectRecovery does not
// mutate the crash artifacts even when the state is ambiguous.
func TestV510RecoveryMatrixReadOnlyAudit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "readonly.db")
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

	a := path + ".compact-a.tmp"
	b := path + ".compact-b.tmp"
	if err := copyFile(path, a); err != nil {
		t.Fatal(err)
	}
	if err := copyFile(path, b); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	beforeA, err := fileSHA256(a)
	if err != nil {
		t.Fatal(err)
	}
	beforeB, err := fileSHA256(b)
	if err != nil {
		t.Fatal(err)
	}

	r, err := InspectRecovery(path, o)
	if err != nil {
		t.Fatal(err)
	}
	if !r.FailClosed || r.Decision != RecoveryDecisionFailClosed {
		t.Fatalf("ambiguous state did not fail closed: %+v", r)
	}

	afterA, err := fileSHA256(a)
	if err != nil {
		t.Fatal(err)
	}
	afterB, err := fileSHA256(b)
	if err != nil {
		t.Fatal(err)
	}
	if beforeA != afterA || beforeB != afterB {
		t.Fatal("InspectRecovery modified recovery artifacts")
	}
}
