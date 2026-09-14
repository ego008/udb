package udb

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const recoveryJournalSuffix = ".compact-recovery.json"

type recoveryJournal struct {
	Version    int    `json:"version"`
	SourcePath string `json:"source_path"`
	TempPath   string `json:"temp_path"`
	BackupPath string `json:"backup_path,omitempty"`
	KeepBackup bool   `json:"keep_backup"`
	Stage      string `json:"stage"`
}

func recoveryJournalPath(sourcePath string) string { return sourcePath + recoveryJournalSuffix }

func writeRecoveryJournal(path string, j recoveryJournal) error {
	data, err := json.Marshal(j)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func removeRecoveryJournal(sourcePath string) {
	_ = os.Remove(recoveryJournalPath(sourcePath))
	_ = os.Remove(recoveryJournalPath(sourcePath) + ".tmp")
}

func validBoltFile(path string, opts Options) bool {
	if path == "" {
		return false
	}
	if _, err := os.Stat(path); err != nil {
		return false
	}
	return checkBoltFile(path, opts.boltOptions()) == nil
}

// recoverInterruptedCompaction makes startup recovery conservative:
// - a valid formal database always wins;
// - if the formal path is missing, prefer a valid compacted temp database;
// - otherwise restore a valid backup;
// - invalid leftovers are never promoted.
func recoverInterruptedCompaction(path string, opts Options) error {
	jpath := recoveryJournalPath(path)
	data, err := os.ReadFile(jpath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("udb: read recovery journal: %w", err)
	}
	var j recoveryJournal
	if err := json.Unmarshal(data, &j); err != nil {
		return fmt.Errorf("udb: invalid recovery journal: %w", err)
	}
	if j.Version != 1 || j.SourcePath != path {
		return fmt.Errorf("udb: unsupported recovery journal for %q", path)
	}

	formalValid := validBoltFile(path, opts)
	if formalValid {
		if j.TempPath != "" {
			_ = os.Remove(j.TempPath)
		}
		removeRecoveryJournal(path)
		return nil
	}

	if j.TempPath != "" && validBoltFile(j.TempPath, opts) {
		if err := os.Rename(j.TempPath, path); err == nil {
			if validBoltFile(path, opts) {
				removeRecoveryJournal(path)
				return nil
			}
			_ = os.Remove(path)
		}
	}
	if j.BackupPath != "" && validBoltFile(j.BackupPath, opts) {
		if err := os.Rename(j.BackupPath, path); err == nil {
			if validBoltFile(path, opts) {
				removeRecoveryJournal(path)
				return nil
			}
			_ = os.Remove(path)
		}
	}

	return fmt.Errorf("udb: cannot recover interrupted compaction for %q", path)
}

func cleanupRecoveryArtifacts(path, temp, backup string, keepBackup bool) {
	if temp != "" {
		_ = os.Remove(temp)
	}
	if !keepBackup && backup != "" {
		_ = os.Remove(backup)
	}
	removeRecoveryJournal(path)
}

func ensureSameDir(path string) error {
	dir := filepath.Dir(path)
	if dir == "" {
		return nil
	}
	return os.MkdirAll(dir, 0755)
}
