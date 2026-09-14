package udb

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const recoveryJournalSuffix = ".compact-recovery.json"

const (
	recoveryJournalVersion = 1
	stageCompacting        = "compacting"
	stageSourceClosed      = "source_closed"
	stageBackupCreated     = "backup_created"
	stageReplaced          = "replaced"
	stageReopened          = "reopened"
)

type recoveryJournal struct {
	Version    int    `json:"version"`
	SourcePath string `json:"source_path"`
	TempPath   string `json:"temp_path"`
	BackupPath string `json:"backup_path,omitempty"`
	KeepBackup bool   `json:"keep_backup"`
	Stage      string `json:"stage"`
}

func recoveryJournalPath(sourcePath string) string { return sourcePath + recoveryJournalSuffix }

// writeRecoveryJournal uses write+fsync+atomic-rename+directory-fsync. The
// journal is deliberately small so this relatively expensive path is only used
// during compaction state transitions.
func writeRecoveryJournal(path string, j recoveryJournal) error {
	if j.Version == 0 {
		j.Version = recoveryJournalVersion
	}
	data, err := json.Marshal(j)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return closeErr
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := syncDir(dir); err != nil {
		return fmt.Errorf("udb: sync recovery journal directory: %w", err)
	}
	return nil
}

func removeRecoveryJournal(sourcePath string) {
	path := recoveryJournalPath(sourcePath)
	_ = os.Remove(path)
	_ = os.Remove(path + ".tmp")
	_ = syncDir(filepath.Dir(path))
	removeRecoveryManifest(sourcePath)
}

func validBoltFile(path string, opts Options) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() || info.Size() == 0 {
		return false
	}
	return checkBoltFile(path, opts.boltOptions()) == nil
}

// artifactCandidate is used only when the journal is unavailable/corrupt.
// Recovery is conservative: only files that pass a complete bbolt check are
// eligible for promotion.
type artifactCandidate struct {
	path    string
	modTime int64
}

func recoveryArtifacts(path string) (temps, backups []artifactCandidate) {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		// Accept both the production form (".compact.<timestamp>.tmp") and
		// older/test crash-artifact forms such as ".compact-crash.tmp".
		// Recovery must recognize artifacts by the stable namespace prefix, not
		// by one particular separator after "compact".
		isTemp := strings.HasPrefix(name, base+".compact.") ||
			strings.HasPrefix(name, base+".compact-")
		isBackup := strings.HasPrefix(name, base+".backup-")
		if !isTemp && !isBackup {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		c := artifactCandidate{path: filepath.Join(dir, name), modTime: info.ModTime().UnixNano()}
		if isTemp {
			temps = append(temps, c)
		} else {
			backups = append(backups, c)
		}
	}
	sort.Slice(temps, func(i, j int) bool { return temps[i].modTime > temps[j].modTime })
	sort.Slice(backups, func(i, j int) bool { return backups[i].modTime > backups[j].modTime })
	return temps, backups
}

func prepareRecoveryDestination(dst string) error {
	info, err := os.Stat(dst)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("udb: recovery destination is a directory: %s", dst)
	}
	if err := os.Remove(dst); err != nil {
		return err
	}
	return syncDir(filepath.Dir(dst))
}

func promoteRecoveryArtifact(src, dst string) error {
	if err := prepareRecoveryDestination(dst); err != nil {
		return err
	}
	if err := os.Rename(src, dst); err != nil {
		return err
	}
	return syncDir(filepath.Dir(dst))
}

// recoverInterruptedCompaction is deliberately safe under journal corruption:
// if the formal database is already valid, it wins and a stale/corrupt journal
// cannot prevent the database from opening. If the formal file is absent or
// invalid, the journal is authoritative when valid; if the journal itself is
// unavailable, recovery only promotes an unambiguous artifact class.
func recoverInterruptedCompaction(path string, opts Options) error {
	jpath := recoveryJournalPath(path)
	formalValid := validBoltFile(path, opts)

	// V5.8 adds a durable recovery manifest alongside the legacy journal.
	// The manifest carries an operation identity and SHA-256 digests for
	// recovery artifacts, so a valid manifest can distinguish artifacts even
	// when the legacy journal is torn or missing. A valid formal DB still wins.
	manifest, manifestErr := readRecoveryManifest(path)
	if manifestErr == nil {
		if formalValid {
			if manifest.TempPath != "" && manifest.TempPath != path {
				_ = os.Remove(manifest.TempPath)
			}
			removeRecoveryJournal(path)
			return nil
		}
		if manifest.TempPath != "" && validManifestArtifact(manifest.TempPath, manifest.TempSHA256, opts) {
			if err := promoteRecoveryArtifact(manifest.TempPath, path); err == nil && validManifestArtifact(path, manifest.TempSHA256, opts) {
				removeRecoveryJournal(path)
				return nil
			}
		}
		if manifest.BackupPath != "" && validManifestArtifact(manifest.BackupPath, manifest.BackupSHA256, opts) {
			if err := promoteRecoveryArtifact(manifest.BackupPath, path); err == nil && validManifestArtifact(path, manifest.BackupSHA256, opts) {
				removeRecoveryJournal(path)
				return nil
			}
		}
		// A structurally valid manifest is stronger evidence than the legacy
		// journal or directory scan. If its recorded artifact cannot be
		// validated by checksum, fail closed rather than silently promoting a
		// different or modified file.
		return fmt.Errorf("udb: recovery manifest has no valid artifact for %q", path)
	} else if !errors.Is(manifestErr, os.ErrNotExist) && formalValid {
		removeRecoveryJournal(path)
		return nil
	}

	data, readErr := os.ReadFile(jpath)
	if readErr == nil {
		var j recoveryJournal
		if err := json.Unmarshal(data, &j); err == nil && j.Version == recoveryJournalVersion && j.SourcePath == path {
			if formalValid {
				if j.TempPath != "" {
					_ = os.Remove(j.TempPath)
				}
				removeRecoveryJournal(path)
				return nil
			}
			if j.TempPath != "" && validBoltFile(j.TempPath, opts) {
				if err := promoteRecoveryArtifact(j.TempPath, path); err == nil && validBoltFile(path, opts) {
					removeRecoveryJournal(path)
					return nil
				}
			}
			if j.BackupPath != "" && validBoltFile(j.BackupPath, opts) {
				if err := promoteRecoveryArtifact(j.BackupPath, path); err == nil && validBoltFile(path, opts) {
					removeRecoveryJournal(path)
					return nil
				}
			}
		} else if formalValid {
			// Corrupt/partial/stale journal must not make an already-valid DB
			// unavailable. The formal DB is the authoritative state.
			removeRecoveryJournal(path)
			return nil
		}
	} else if !errors.Is(readErr, os.ErrNotExist) && formalValid {
		removeRecoveryJournal(path)
		return nil
	}

	if formalValid {
		return nil
	}

	// The journal may have been torn, lost, or never durably persisted. Scan
	// only compaction/backup naming patterns and promote only a fully checked
	// bbolt file. This makes startup recovery robust to arbitrary process death
	// around the journal rename boundary.
	//
	// IMPORTANT: when the journal is unavailable, a valid temp and a valid
	// backup are intentionally treated as ambiguous. Without the journal there
	// is no durable fact telling us which artifact belongs to the latest
	// compaction attempt. Choosing one merely because it is a temp (or because
	// its mtime is newer) can silently roll back committed user data. In that
	// situation fail closed instead of guessing.
	temps, backups := recoveryArtifacts(path)
	validTemps := make([]artifactCandidate, 0, len(temps))
	validBackups := make([]artifactCandidate, 0, len(backups))
	for _, c := range temps {
		if validBoltFile(c.path, opts) {
			validTemps = append(validTemps, c)
		}
	}
	for _, c := range backups {
		if validBoltFile(c.path, opts) {
			validBackups = append(validBackups, c)
		}
	}

	// Without a usable journal, every valid artifact is an independent
	// candidate. More than one valid candidate is ambiguous, including
	// multiple temps or multiple backups; mtime alone is not a durable
	// ordering guarantee. Only exactly one valid artifact may be promoted.
	if len(validTemps)+len(validBackups) > 1 {
		return fmt.Errorf(
			"udb: ambiguous recovery for %q: %d valid compact artifacts exist without a usable journal",
			path, len(validTemps)+len(validBackups),
		)
	}
	if len(validTemps) == 1 {
		if err := promoteRecoveryArtifact(validTemps[0].path, path); err == nil && validBoltFile(path, opts) {
			removeRecoveryJournal(path)
			return nil
		}
	}
	if len(validBackups) == 1 {
		if err := promoteRecoveryArtifact(validBackups[0].path, path); err == nil && validBoltFile(path, opts) {
			removeRecoveryJournal(path)
			return nil
		}
	}

	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return fmt.Errorf("udb: recovery journal unreadable and no valid recovery artifact: %w", readErr)
	}

	// A missing journal and no recovery artifacts is the normal state for a
	// brand-new database path. Do not mistake that state for an interrupted
	// compaction; bbolt must be allowed to create the database file.
	//
	// If a journal exists but is malformed, or recovery artifacts exist but
	// none of them pass validation, we still fail closed below. That distinction
	// prevents silent data loss while keeping Open() usable for new databases.
	if errors.Is(readErr, os.ErrNotExist) && len(temps) == 0 && len(backups) == 0 {
		return nil
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
