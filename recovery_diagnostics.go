package udb

import (
	"encoding/json"
	"errors"
	"os"
)

// RecoveryArtifactKind identifies a recovery file discovered next to a UDB file.
type RecoveryArtifactKind string

const (
	RecoveryArtifactTemp   RecoveryArtifactKind = "temp"
	RecoveryArtifactBackup RecoveryArtifactKind = "backup"
)

// RecoveryArtifactReport describes one recovery artifact without modifying it.
type RecoveryArtifactReport struct {
	Path   string
	Kind   RecoveryArtifactKind
	Exists bool
	Valid  bool
	SHA256 string
}

// RecoveryReport is a read-only diagnostic snapshot of the recovery state.
// It is intentionally independent from Open(): callers can inspect a damaged
// database path before deciding whether to repair, restore, or alert.
type RecoveryReport struct {
	SourcePath     string
	FormalExists   bool
	FormalValid    bool
	JournalExists  bool
	JournalValid   bool
	ManifestExists bool
	ManifestValid  bool
	Artifacts      []RecoveryArtifactReport
	Recoverable    bool
	FailClosed     bool
	Decision       string
	Reason         string
}

const (
	RecoveryDecisionClean      = "clean"
	RecoveryDecisionManifest   = "manifest"
	RecoveryDecisionJournal    = "journal"
	RecoveryDecisionSingle     = "single_artifact"
	RecoveryDecisionFresh      = "fresh_database"
	RecoveryDecisionFailClosed = "fail_closed"
)

// InspectRecovery performs a non-destructive recovery-state audit.
//
// It never promotes, removes, renames, or opens the database for writing. All
// candidate bbolt files are checked read-only, and manifest checksums are
// verified when present. The result is intended for operational diagnostics
// and for deterministic recovery-state testing.
func InspectRecovery(path string, opts Options) (RecoveryReport, error) {
	if path == "" {
		return RecoveryReport{}, errors.New("udb: empty recovery path")
	}

	r := RecoveryReport{SourcePath: path}
	if _, err := os.Stat(path); err == nil {
		r.FormalExists = true
		r.FormalValid = validBoltFile(path, opts)
	} else if !errors.Is(err, os.ErrNotExist) {
		return r, err
	}

	jpath := recoveryJournalPath(path)
	if _, err := os.Stat(jpath); err == nil {
		r.JournalExists = true
		data, readErr := os.ReadFile(jpath)
		if readErr == nil {
			var j recoveryJournal
			if unmarshalRecoveryJournal(data, &j) && j.SourcePath == path {
				r.JournalValid = true
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return r, err
	}

	mpath := recoveryManifestPath(path)
	if _, err := os.Stat(mpath); err == nil {
		r.ManifestExists = true
		if _, err := readRecoveryManifest(path); err == nil {
			r.ManifestValid = true
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return r, err
	}

	temps, backups := recoveryArtifacts(path)
	for _, c := range temps {
		r.Artifacts = append(r.Artifacts, inspectRecoveryArtifact(c.path, RecoveryArtifactTemp, opts))
	}
	for _, c := range backups {
		r.Artifacts = append(r.Artifacts, inspectRecoveryArtifact(c.path, RecoveryArtifactBackup, opts))
	}

	// A valid formal database is always authoritative.
	if r.FormalValid {
		r.Recoverable = true
		r.Decision = RecoveryDecisionClean
		r.Reason = "formal database is valid and authoritative"
		return r, nil
	}

	if r.ManifestValid {
		m, _ := readRecoveryManifest(path)
		if (m.TempPath != "" && artifactMatchesReport(r.Artifacts, m.TempPath, m.TempSHA256)) ||
			(m.BackupPath != "" && artifactMatchesReport(r.Artifacts, m.BackupPath, m.BackupSHA256)) {
			r.Recoverable = true
			r.Decision = RecoveryDecisionManifest
			r.Reason = "valid recovery manifest identifies a checksum-verified artifact"
		} else {
			r.FailClosed = true
			r.Decision = RecoveryDecisionFailClosed
			r.Reason = "valid recovery manifest has no checksum-verified artifact"
		}
		return r, nil
	}

	if r.JournalValid {
		data, _ := os.ReadFile(jpath)
		var j recoveryJournal
		if unmarshalRecoveryJournal(data, &j) {
			if (j.TempPath != "" && artifactValidReport(r.Artifacts, j.TempPath)) ||
				(j.BackupPath != "" && artifactValidReport(r.Artifacts, j.BackupPath)) {
				r.Recoverable = true
				r.Decision = RecoveryDecisionJournal
				r.Reason = "valid recovery journal identifies a structurally valid artifact"
				return r, nil
			}
		}
		r.FailClosed = true
		r.Decision = RecoveryDecisionFailClosed
		r.Reason = "valid recovery journal has no valid artifact"
		return r, nil
	}

	validCount := 0
	for _, a := range r.Artifacts {
		if a.Valid {
			validCount++
		}
	}
	if validCount == 1 {
		r.Recoverable = true
		r.Decision = RecoveryDecisionSingle
		r.Reason = "exactly one valid recovery artifact exists"
		return r, nil
	}
	if validCount > 1 {
		r.FailClosed = true
		r.Decision = RecoveryDecisionFailClosed
		r.Reason = "multiple valid recovery artifacts exist without durable recovery metadata"
		return r, nil
	}

	if !r.FormalExists && !r.JournalExists && !r.ManifestExists && len(r.Artifacts) == 0 {
		r.Recoverable = true
		r.Decision = RecoveryDecisionFresh
		r.Reason = "no database or recovery artifacts exist; this is a fresh-open state"
		return r, nil
	}

	r.FailClosed = true
	r.Decision = RecoveryDecisionFailClosed
	r.Reason = "database is unavailable and no unambiguous recovery artifact exists"
	return r, nil
}

func unmarshalRecoveryJournal(data []byte, j *recoveryJournal) bool {
	if j == nil {
		return false
	}
	// Keep journal parsing in one place while preserving the legacy format.
	if err := json.Unmarshal(data, j); err != nil {
		return false
	}
	return j.Version == recoveryJournalVersion && j.SourcePath != "" && j.Stage != ""
}

func inspectRecoveryArtifact(path string, kind RecoveryArtifactKind, opts Options) RecoveryArtifactReport {
	a := RecoveryArtifactReport{Path: path, Kind: kind}
	if _, err := os.Stat(path); err != nil {
		return a
	}
	a.Exists = true
	a.Valid = validBoltFile(path, opts)
	if a.Valid {
		if sha, err := fileSHA256(path); err == nil {
			a.SHA256 = sha
		}
	}
	return a
}

func artifactValidReport(artifacts []RecoveryArtifactReport, path string) bool {
	for _, a := range artifacts {
		if a.Path == path {
			return a.Valid
		}
	}
	return false
}

func artifactMatchesReport(artifacts []RecoveryArtifactReport, path, expected string) bool {
	for _, a := range artifacts {
		if a.Path != path || !a.Valid {
			continue
		}
		if expected == "" || a.SHA256 == expected {
			return true
		}
	}
	return false
}
