package udb

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestV58MissingJournalUsesRecoveryManifest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.db")
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

	tmp := path + ".compact-manifest-test.tmp"
	if err := copyFile(path, tmp); err != nil {
		t.Fatal(err)
	}
	sha, err := fileSHA256(tmp)
	if err != nil {
		t.Fatal(err)
	}
	m := recoveryManifest{
		Version: recoveryManifestVersion, Operation: "test-operation", SourcePath: path,
		TempPath: tmp, TempSHA256: sha, Stage: stageCompacting,
	}
	if err := writeRecoveryManifest(recoveryManifestPath(path), m); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(recoveryJournalPath(path)); err == nil {
		// Expected: the journal was absent in this simulation.
	} else if !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}

	recovered, err := OpenWithOptions(path, &o)
	if err != nil {
		t.Fatalf("manifest recovery failed: %v", err)
	}
	defer recovered.Close()
	got := takeLogicalSnapshot(t, recovered, []string{"users", "counters"}, []string{"rank"})
	if !reflect.DeepEqual(want, got) {
		t.Fatal("manifest recovery changed logical snapshot")
	}
	if _, err := os.Stat(recoveryManifestPath(path)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("manifest remains after recovery: %v", err)
	}
}

func TestV58ManifestChecksumMismatchFailsClosed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "checksum.db")
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

	tmp := path + ".compact-checksum.tmp"
	if err := copyFile(path, tmp); err != nil {
		t.Fatal(err)
	}
	m := recoveryManifest{
		Version: recoveryManifestVersion, Operation: "checksum-test", SourcePath: path,
		TempPath: tmp, TempSHA256: "deadbeef", Stage: stageCompacting,
	}
	if err := writeRecoveryManifest(recoveryManifestPath(path), m); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(recoveryJournalPath(path)); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}

	if _, err := OpenWithOptions(path, &o); err == nil {
		t.Fatal("checksum mismatch unexpectedly recovered")
	}
	if _, err := os.Stat(tmp); err != nil {
		t.Fatalf("recovery artifact unexpectedly removed: %v", err)
	}
}

func TestV58CorruptManifestFallsBackToLegacyJournal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fallback.db")
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

	tmp := path + ".compact-fallback.tmp"
	if err := copyFile(path, tmp); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	j := recoveryJournal{Version: recoveryJournalVersion, SourcePath: path, TempPath: tmp, Stage: stageCompacting}
	if err := writeRecoveryJournal(recoveryJournalPath(path), j); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(recoveryManifestPath(path), []byte("{broken"), 0600); err != nil {
		t.Fatal(err)
	}

	recovered, err := OpenWithOptions(path, &o)
	if err != nil {
		t.Fatalf("legacy fallback recovery failed: %v", err)
	}
	defer recovered.Close()
	got := takeLogicalSnapshot(t, recovered, []string{"users", "counters"}, []string{"rank"})
	if !reflect.DeepEqual(want, got) {
		t.Fatal("legacy fallback changed logical snapshot")
	}
}
