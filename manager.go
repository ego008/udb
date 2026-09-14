package udb

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type MaintenanceManager struct {
	db           *DB
	mu           sync.RWMutex
	running      bool
	stop         chan struct{}
	done         chan struct{}
	lastCompact  time.Time
	lastResult   CompactResult
	lastError    error
	failureCount int
}

func newMaintenanceManager(db *DB) *MaintenanceManager { return &MaintenanceManager{db: db} }

func (m *MaintenanceManager) Start(ctx context.Context) error {
	if m == nil || m.db == nil || m.db.lifecycle.isClosed() {
		return ErrDatabaseClosed
	}
	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		return ErrMaintenanceBusy
	}
	if !m.db.opts.Maintenance.Enabled {
		m.mu.Unlock()
		return fmt.Errorf("udb: maintenance disabled")
	}
	m.running = true
	m.stop = make(chan struct{})
	m.done = make(chan struct{})
	stop, done := m.stop, m.done
	m.mu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	go m.loop(ctx, stop, done)
	return nil
}
func (m *MaintenanceManager) Stop() {
	m.mu.Lock()
	if !m.running {
		m.mu.Unlock()
		return
	}
	close(m.stop)
	done := m.done
	m.mu.Unlock()
	<-done
}
func (m *MaintenanceManager) loop(ctx context.Context, stop <-chan struct{}, done chan<- struct{}) {
	defer func() { m.mu.Lock(); m.running = false; m.mu.Unlock(); close(done) }()
	cfg := m.db.opts.Maintenance
	timer := time.NewTicker(cfg.Interval)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-stop:
			return
		case <-timer.C:
			m.runOnce(cfg)
		}
	}
}
func (m *MaintenanceManager) runOnce(cfg MaintenanceConfig) {
	m.mu.Lock()
	last := m.lastCompact
	m.mu.Unlock()
	if cfg.CompactCooldown > 0 && !last.IsZero() && time.Since(last) < cfg.CompactCooldown {
		return
	}
	m.mu.Lock()
	if cfg.MaxFailures > 0 && m.failureCount >= cfg.MaxFailures {
		m.mu.Unlock()
		return
	}
	m.mu.Unlock()
	st, err := m.db.MaintenanceStats(cfg)
	if err != nil {
		m.recordError(err)
		return
	}
	if !st.NeedCompaction {
		return
	}
	result, _, err := m.db.CompactAndReplace(cfg)
	if err != nil {
		m.recordError(err)
		return
	}
	m.mu.Lock()
	m.lastCompact = time.Now()
	m.lastResult = result
	m.lastError = nil
	m.failureCount = 0
	m.mu.Unlock()
}
func (m *MaintenanceManager) recordError(err error) {
	m.mu.Lock()
	m.lastError = err
	m.failureCount++
	m.mu.Unlock()
}
func (m *MaintenanceManager) Snapshot() (time.Time, CompactResult, error, int, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.lastCompact, m.lastResult, m.lastError, m.failureCount, m.running
}

func (db *DB) Health() HealthStatus {
	if db == nil {
		return HealthStatus{OK: false, Closed: true}
	}
	closed := db.lifecycle.isClosed()
	ro := db.opts.ReadOnly
	var st DBStats
	var err error
	if !closed {
		st, err = db.MaintenanceStats(db.opts.Maintenance)
	}
	lc, lr, le, fc, running := db.maintenance.Snapshot()
	return HealthStatus{OK: !closed && err == nil && le == nil, Closed: closed, ReadOnly: ro, Stats: st, LastCompact: lc, LastCompactResult: lr, FailureCount: fc, MaintenanceRunning: running, LastError: errorString(le)}
}
func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
