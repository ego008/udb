package udb

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	bolt "go.etcd.io/bbolt"
)

type CompactFaultPoint string

const (
	FaultNone          CompactFaultPoint = ""
	FaultAfterCompact  CompactFaultPoint = "after_compact"
	FaultBeforeBackup  CompactFaultPoint = "before_backup"
	FaultAfterBackup   CompactFaultPoint = "after_backup"
	FaultBeforeReplace CompactFaultPoint = "before_replace"
	FaultAfterReplace  CompactFaultPoint = "after_replace"
	FaultBeforeReopen  CompactFaultPoint = "before_reopen"
	FaultAfterReopen   CompactFaultPoint = "after_reopen"
	FaultAfterCheck    CompactFaultPoint = "after_check"
)

// CompactFaultInjector is an opt-in test/reliability hook. Production code
// should leave it nil. Returning an error aborts the current compaction stage.
type CompactFaultInjector func(CompactFaultPoint) error

type MaintenanceConfig struct {
	Enabled            bool
	Interval           time.Duration
	MinDBSize          int64
	FreeRatio          float64
	PendingRatio       float64
	TxMaxSize          int
	CheckBeforeCompact bool
	CheckAfterCompact  bool
	KeepBackup         bool
	BackupSuffix       string
	CompactCooldown    time.Duration
	MaxFailures        int
	FaultInjector      CompactFaultInjector
}

func DefaultMaintenanceConfig() MaintenanceConfig {
	return MaintenanceConfig{
		Enabled: true, Interval: 30 * time.Minute, MinDBSize: 512 << 20,
		FreeRatio: 0.30, PendingRatio: 0.10, TxMaxSize: 64 << 10,
		CheckAfterCompact: true, KeepBackup: true,
		BackupSuffix: ".backup-20060102-150405.000000000", CompactCooldown: 6 * time.Hour,
		MaxFailures: 3,
	}
}

type DBStats struct {
	Path           string
	FileSize       int64
	PageSize       int
	FreePageN      int
	PendingPageN   int
	FreeAlloc      int64
	FreelistInuse  int64
	OpenTxN        int
	Reclaimable    int64
	ReclaimRatio   float64
	PendingRatio   float64
	NeedCompaction bool
}

func (db *DB) MaintenanceStats(cfg MaintenanceConfig) (DBStats, error) {
	if db == nil {
		return DBStats{}, ErrDatabaseClosed
	}
	d, err := db.lifecycle.begin(db)
	if err != nil {
		return DBStats{}, err
	}
	defer db.lifecycle.end()
	return db.maintenanceStatsWithDB(d, cfg)
}

// maintenanceStatsWithDB reads bbolt statistics while the caller owns a
// managed DB reference. It must not be called after that reference is ended.
func (db *DB) maintenanceStatsWithDB(d *bolt.DB, cfg MaintenanceConfig) (DBStats, error) {
	if d == nil {
		return DBStats{}, ErrDatabaseClosed
	}
	path := d.Path()
	st, err := os.Stat(path)
	if err != nil {
		return DBStats{}, err
	}
	bs := d.Stats()
	info := d.Info()
	pageSize := info.PageSize
	if pageSize <= 0 {
		pageSize = 4096
	}
	reclaimable := int64(bs.FreePageN) * int64(pageSize)
	ratio, pendingRatio := 0.0, 0.0
	if st.Size() > 0 {
		ratio = float64(reclaimable) / float64(st.Size())
		pendingRatio = float64(int64(bs.PendingPageN)*int64(pageSize)) / float64(st.Size())
	}
	stats := DBStats{
		Path: path, FileSize: st.Size(), PageSize: pageSize,
		FreePageN: bs.FreePageN, PendingPageN: bs.PendingPageN,
		FreeAlloc: int64(bs.FreeAlloc), FreelistInuse: int64(bs.FreelistInuse),
		OpenTxN: bs.OpenTxN, Reclaimable: reclaimable,
		ReclaimRatio: ratio, PendingRatio: pendingRatio,
	}
	stats.NeedCompaction = ShouldCompact(stats, cfg)
	return stats, nil
}

func ShouldCompact(stats DBStats, cfg MaintenanceConfig) bool {
	if stats.FileSize < cfg.MinDBSize || stats.ReclaimRatio < cfg.FreeRatio || stats.OpenTxN != 0 {
		return false
	}
	if cfg.PendingRatio > 0 && stats.PendingRatio > cfg.PendingRatio {
		return false
	}
	return true
}

type CompactResult struct {
	SourcePath, DestPath              string
	BeforeSize, AfterSize, SavedBytes int64
	Duration                          time.Duration
}

type HealthStatus struct {
	OK                 bool
	Closed             bool
	ReadOnly           bool
	Stats              DBStats
	LastCompact        time.Time
	LastCompactResult  CompactResult
	LastError          string
	FailureCount       int
	MaintenanceRunning bool
}

func (db *DB) Check() error {
	if db == nil {
		return ErrDatabaseClosed
	}
	return db.View(func(tx *Tx) error {
		for err := range tx.check() {
			if err != nil {
				return err
			}
		}
		return nil
	})
}

func checkBoltFile(path string, o *bolt.Options) error {
	x := *o
	x.ReadOnly = true
	d, err := bolt.Open(path, 0600, &x)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.View(func(tx *bolt.Tx) error {
		for e := range tx.Check() {
			if e != nil {
				return e
			}
		}
		return nil
	})
}

func (db *DB) CompactTo(dstPath string, cfg MaintenanceConfig) (CompactResult, error) {
	if db == nil {
		return CompactResult{}, ErrDatabaseClosed
	}
	if dstPath == "" {
		return CompactResult{}, errors.New("udb: destination path is empty")
	}
	d, err := db.lifecycle.begin(db)
	if err != nil {
		return CompactResult{}, err
	}
	defer db.lifecycle.end()
	st, err := db.maintenanceStatsWithDB(d, cfg)
	if err != nil {
		return CompactResult{}, err
	}
	if !ShouldCompact(st, cfg) {
		return CompactResult{}, fmt.Errorf("udb: compaction not needed: size=%d reclaim_ratio=%.2f%% pending_ratio=%.2f%% open_tx=%d", st.FileSize, st.ReclaimRatio*100, st.PendingRatio*100, st.OpenTxN)
	}
	return db.compactTo(dstPath, cfg, st, d)
}

func (db *DB) compactTo(dstPath string, cfg MaintenanceConfig, st DBStats, src *bolt.DB) (CompactResult, error) {
	if cfg.CheckBeforeCompact {
		if err := checkBoltDB(src); err != nil {
			return CompactResult{}, fmt.Errorf("udb: source check failed: %w", err)
		}
	}
	if err := ensureSameDir(dstPath); err != nil {
		return CompactResult{}, err
	}
	if info, err := os.Stat(dstPath); err == nil && info.IsDir() {
		return CompactResult{}, fmt.Errorf("udb: destination is a directory: %s", dstPath)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return CompactResult{}, err
	}
	_ = os.Remove(dstPath)
	start := time.Now()
	dst, err := bolt.Open(dstPath, 0600, db.opts.boltOptions())
	if err != nil {
		return CompactResult{}, err
	}
	if err = bolt.Compact(dst, src, int64(cfg.TxMaxSize)); err != nil {
		_ = dst.Close()
		_ = os.Remove(dstPath)
		return CompactResult{}, fmt.Errorf("udb: compact failed: %w", err)
	}
	if err = dst.Close(); err != nil {
		_ = os.Remove(dstPath)
		return CompactResult{}, err
	}
	if err := syncFile(dstPath); err != nil {
		_ = os.Remove(dstPath)
		return CompactResult{}, fmt.Errorf("udb: sync compacted db: %w", err)
	}
	if err := injectCompactFault(cfg, FaultAfterCompact); err != nil {
		_ = os.Remove(dstPath)
		return CompactResult{}, err
	}
	if cfg.CheckAfterCompact {
		if err = checkBoltFile(dstPath, db.opts.boltOptions()); err != nil {
			_ = os.Remove(dstPath)
			return CompactResult{}, fmt.Errorf("udb: compacted db check failed: %w", err)
		}
	}
	after, err := os.Stat(dstPath)
	if err != nil {
		return CompactResult{}, err
	}
	return CompactResult{SourcePath: st.Path, DestPath: dstPath, BeforeSize: st.FileSize, AfterSize: after.Size(), SavedBytes: st.FileSize - after.Size(), Duration: time.Since(start)}, nil
}

func uniqueBackupPath(path, suffix string) string {
	if suffix == "" {
		suffix = ".backup-20060102-150405.000000000"
	}
	base := path + time.Now().Format(suffix)
	p := base
	for i := 1; ; i++ {
		if _, err := os.Stat(p); errors.Is(err, os.ErrNotExist) {
			return p
		}
		p = fmt.Sprintf("%s.%d", base, i)
	}
}

// CompactAndReplace enters the lifecycle quiescent state. New managed
// operations wait for admission to reopen, while currently running managed
// operations are allowed to finish. No global mutex is held while bbolt compaction runs. Lifecycle
// operations themselves are serialized, so Close and another replacement
// cannot run concurrently.
func (db *DB) CompactAndReplace(cfg MaintenanceConfig) (CompactResult, string, error) {
	return db.CompactAndReplaceContext(context.Background(), cfg)
}

// CompactAndReplaceContext performs an exclusive compact-and-replace after
// draining all currently admitted managed operations. If ctx is cancelled
// while waiting for the drain, the lifecycle admission gate is restored and
// the database remains available; the compact operation is not performed.
func (db *DB) CompactAndReplaceContext(ctx context.Context, cfg MaintenanceConfig) (CompactResult, string, error) {
	if db == nil {
		return CompactResult{}, "", ErrDatabaseClosed
	}
	if err := db.lifecycle.quiesce(ctx, db); err != nil {
		return CompactResult{}, "", err
	}
	defer db.lifecycle.resume()

	src := db.lifecycle.currentDB(db)
	if src == nil {
		return CompactResult{}, "", ErrDatabaseClosed
	}
	st, err := db.maintenanceStatsWithDB(src, cfg)
	if err != nil {
		return CompactResult{}, "", err
	}
	if !ShouldCompact(st, cfg) {
		return CompactResult{}, "", fmt.Errorf("udb: compaction not needed: size=%d reclaim_ratio=%.2f%% pending_ratio=%.2f%% open_tx=%d", st.FileSize, st.ReclaimRatio*100, st.PendingRatio*100, st.OpenTxN)
	}
	if cfg.CheckBeforeCompact {
		if err := checkBoltDB(src); err != nil {
			return CompactResult{}, "", fmt.Errorf("udb: source check failed: %w", err)
		}
	}

	path := src.Path()
	tmp := path + fmt.Sprintf(".compact.%d.tmp", time.Now().UnixNano())
	_ = os.Remove(tmp)
	journalPath := recoveryJournalPath(path)
	journal := recoveryJournal{Version: 1, SourcePath: path, TempPath: tmp, KeepBackup: cfg.KeepBackup, Stage: "compacting"}
	if err := writeRecoveryJournal(journalPath, journal); err != nil {
		return CompactResult{}, "", fmt.Errorf("udb: write recovery journal: %w", err)
	}

	compactCfg := cfg
	compactCfg.CheckBeforeCompact = false
	result, err := db.compactTo(tmp, compactCfg, st, src)
	if err != nil {
		cleanupRecoveryArtifacts(path, tmp, "", false)
		return CompactResult{}, "", err
	}

	if err := injectCompactFault(cfg, FaultBeforeBackup); err != nil {
		return db.rollbackCompactFailure(path, tmp, "", false, err)
	}
	if err := src.Close(); err != nil {
		return db.rollbackCompactFailure(path, tmp, "", false, fmt.Errorf("udb: close source: %w", err))
	}
	db.lifecycle.setDB(db, nil)

	backup := ""
	if cfg.KeepBackup {
		backup = uniqueBackupPath(path, cfg.BackupSuffix)
		journal.BackupPath = backup
	}
	journal.Stage = "source_closed"
	if err := writeRecoveryJournal(journalPath, journal); err != nil {
		return db.rollbackCompactFailure(path, tmp, backup, cfg.KeepBackup, fmt.Errorf("udb: update recovery journal: %w", err))
	}

	if cfg.KeepBackup {
		if err := os.Rename(path, backup); err != nil {
			return db.rollbackCompactFailure(path, tmp, backup, true, fmt.Errorf("udb: backup original: %w", err))
		}
		journal.Stage = "backup_created"
		if err := writeRecoveryJournal(journalPath, journal); err != nil {
			return db.rollbackCompactFailure(path, tmp, backup, true, fmt.Errorf("udb: update recovery journal: %w", err))
		}
		if err := syncDir(filepath.Dir(path)); err != nil {
			return db.rollbackCompactFailure(path, tmp, backup, true, fmt.Errorf("udb: sync backup rename: %w", err))
		}
	} else {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return db.rollbackCompactFailure(path, tmp, "", false, fmt.Errorf("udb: remove original: %w", err))
		}
		if err := syncDir(filepath.Dir(path)); err != nil {
			return db.rollbackCompactFailure(path, tmp, "", false, fmt.Errorf("udb: sync original removal: %w", err))
		}
	}
	if err := injectCompactFault(cfg, FaultAfterBackup); err != nil {
		return db.rollbackCompactFailure(path, tmp, backup, cfg.KeepBackup, err)
	}

	if err := injectCompactFault(cfg, FaultBeforeReplace); err != nil {
		return db.rollbackCompactFailure(path, tmp, backup, cfg.KeepBackup, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return db.rollbackCompactFailure(path, tmp, backup, cfg.KeepBackup, fmt.Errorf("udb: install compacted db: %w", err))
	}
	if err := syncDir(filepath.Dir(path)); err != nil {
		return db.rollbackCompactFailure(path, "", backup, cfg.KeepBackup, fmt.Errorf("udb: sync compacted db rename: %w", err))
	}
	journal.Stage = "replaced"
	if err := writeRecoveryJournal(journalPath, journal); err != nil {
		return db.rollbackCompactFailure(path, "", backup, cfg.KeepBackup, fmt.Errorf("udb: update recovery journal: %w", err))
	}
	if err := injectCompactFault(cfg, FaultAfterReplace); err != nil {
		return db.rollbackCompactFailure(path, "", backup, cfg.KeepBackup, err)
	}

	if err := injectCompactFault(cfg, FaultBeforeReopen); err != nil {
		return db.rollbackCompactFailure(path, "", backup, cfg.KeepBackup, err)
	}
	if err := db.reopenLocked(path); err != nil {
		return db.rollbackCompactFailure(path, "", backup, cfg.KeepBackup, fmt.Errorf("udb: reopen compacted db: %w", err))
	}
	journal.Stage = "reopened"
	if err := writeRecoveryJournal(journalPath, journal); err != nil {
		return db.rollbackCompactFailure(path, "", backup, cfg.KeepBackup, fmt.Errorf("udb: update recovery journal: %w", err))
	}
	if err := injectCompactFault(cfg, FaultAfterReopen); err != nil {
		return db.rollbackCompactFailure(path, "", backup, cfg.KeepBackup, err)
	}

	if cfg.CheckAfterCompact {
		if err := checkBoltDB(db.lifecycle.currentDB(db)); err != nil {
			return db.rollbackCompactFailure(path, "", backup, cfg.KeepBackup, fmt.Errorf("udb: installed compacted db check failed: %w", err))
		}
	}
	if err := injectCompactFault(cfg, FaultAfterCheck); err != nil {
		return db.rollbackCompactFailure(path, "", backup, cfg.KeepBackup, err)
	}

	cleanupRecoveryArtifacts(path, "", backup, cfg.KeepBackup)
	return result, backup, nil
}

func injectCompactFault(cfg MaintenanceConfig, point CompactFaultPoint) error {
	if cfg.FaultInjector == nil || point == FaultNone {
		return nil
	}
	if err := cfg.FaultInjector(point); err != nil {
		return fmt.Errorf("udb: injected compact failure at %s: %w", point, err)
	}
	return nil
}

func (db *DB) rollbackCompactFailure(path, tmp, backup string, keepBackup bool, cause error) (CompactResult, string, error) {
	// The lifecycle is quiesced here. Rollback therefore cannot race with a
	// managed View/Update. Prefer a valid backup, otherwise retain a valid
	// formal database. A valid compacted temp is also recoverable on next open.
	if d := db.lifecycle.currentDB(db); d != nil {
		_ = d.Close()
		db.lifecycle.setDB(db, nil)
	}
	if backup != "" {
		if _, err := os.Stat(path); err == nil {
			if validBoltFile(path, db.opts) {
				_ = os.Remove(path)
			}
		}
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			if err := os.Rename(backup, path); err == nil {
				_ = syncDir(filepath.Dir(path))
				backup = ""
			}
		}
	}
	if !validBoltFile(path, db.opts) && tmp != "" && validBoltFile(tmp, db.opts) {
		if err := os.Rename(tmp, path); err == nil {
			_ = syncDir(filepath.Dir(path))
		}
	}
	if validBoltFile(path, db.opts) {
		_ = db.reopenLocked(path)
	}
	// Keep the journal when rollback could not establish a valid formal file;
	// startup recovery can then make another conservative attempt.
	if validBoltFile(path, db.opts) {
		cleanupRecoveryArtifacts(path, tmp, backup, keepBackup)
	} else {
		return CompactResult{}, backup, fmt.Errorf("%w; rollback incomplete", cause)
	}
	return CompactResult{}, backup, cause
}

func (db *DB) reopenLocked(path string) error {
	d, err := bolt.Open(path, 0600, db.opts.boltOptions())
	if err != nil {
		return err
	}
	db.lifecycle.setDB(db, d)
	return nil
}

func checkBoltDB(d *bolt.DB) error {
	if d == nil {
		return ErrDatabaseClosed
	}
	return d.View(func(tx *bolt.Tx) error {
		for e := range tx.Check() {
			if e != nil {
				return e
			}
		}
		return nil
	})
}

func (db *DB) checkLocked() error {
	if db == nil {
		return ErrDatabaseClosed
	}
	d := db.lifecycle.currentDB(db)
	if d == nil {
		return ErrDatabaseClosed
	}
	return checkBoltDB(d)
}

func StatsFile(path string) (DBStats, error) {
	o := DefaultOptions()
	o.ReadOnly = true
	o.Maintenance.Enabled = false
	d, err := OpenWithOptions(path, &o)
	if err != nil {
		return DBStats{}, err
	}
	defer d.Close()
	return d.MaintenanceStats(o.Maintenance)
}
func CheckFile(path string) error {
	o := DefaultOptions()
	o.ReadOnly = true
	o.Maintenance.Enabled = false
	d, err := OpenWithOptions(path, &o)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Check()
}

// Maintenance returns the manager associated with the DB.
func (db *DB) Maintenance() *MaintenanceManager { return db.maintenance }

// StartMaintenance starts the background manager. It is safe to call once; later calls return ErrMaintenanceBusy.
func (db *DB) StartMaintenance(ctx context.Context) error {
	if db == nil {
		return ErrDatabaseClosed
	}
	return db.maintenance.Start(ctx)
}
func (db *DB) StopMaintenance() {
	if db != nil && db.maintenance != nil {
		db.maintenance.Stop()
	}
}
