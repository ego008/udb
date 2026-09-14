package udb

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestV59RecoveryDiagnosticsFreshState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fresh.db")
	o := DefaultOptions()
	o.Maintenance.Enabled = false

	r, err := InspectRecovery(path, o)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Recoverable || r.FailClosed || r.Decision != RecoveryDecisionFresh {
		t.Fatalf("unexpected fresh recovery report: %+v", r)
	}
}

func TestV59RecoveryDiagnosticsFormalDatabaseWins(t *testing.T) {
	path := filepath.Join(t.TempDir(), "formal.db")
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

	// Add a valid but stale-looking artifact. The formal DB must remain
	// authoritative and the audit must report a clean state.
	tmp := path + ".compact-stale.tmp"
	if err := copyFile(path, tmp); err != nil {
		t.Fatal(err)
	}

	r, err := InspectRecovery(path, o)
	if err != nil {
		t.Fatal(err)
	}
	if !r.FormalValid || r.Decision != RecoveryDecisionClean || r.FailClosed {
		t.Fatalf("formal DB did not win diagnostics: %+v", r)
	}
}

func TestV59RecoveryDiagnosticsAmbiguousArtifacts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ambiguous.db")
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

	for _, suffix := range []string{".compact-a.tmp", ".compact-b.tmp"} {
		if err := copyFile(path, path+suffix); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	r, err := InspectRecovery(path, o)
	if err != nil {
		t.Fatal(err)
	}
	if !r.FailClosed || r.Decision != RecoveryDecisionFailClosed {
		t.Fatalf("ambiguous state did not fail closed: %+v", r)
	}
}

func TestV59RecoveryDiagnosticsManifestChecksum(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manifest.db")
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

	tmp := path + ".compact-manifest.tmp"
	if err := copyFile(path, tmp); err != nil {
		t.Fatal(err)
	}
	m := recoveryManifest{
		Version: recoveryManifestVersion, Operation: "v59-diagnostics",
		SourcePath: path, TempPath: tmp, TempSHA256: "bad-checksum", Stage: stageCompacting,
	}
	if err := writeRecoveryManifest(recoveryManifestPath(path), m); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	r, err := InspectRecovery(path, o)
	if err != nil {
		t.Fatal(err)
	}
	if !r.FailClosed || r.Decision != RecoveryDecisionFailClosed {
		t.Fatalf("checksum mismatch did not fail closed: %+v", r)
	}
	if _, err := os.Stat(tmp); errors.Is(err, os.ErrNotExist) {
		t.Fatal("diagnostic inspection removed recovery artifact")
	}
}
