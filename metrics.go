package udb

import (
	"sync/atomic"
	"time"
)

// DBMetrics is a point-in-time snapshot of managed database activity. It
// measures transaction-level activity rather than individual HSet/HGet calls,
// because those calls are implemented on top of View/Update transactions.
type DBMetrics struct {
	ViewTransactions    uint64
	UpdateTransactions  uint64
	ViewErrors          uint64
	UpdateErrors        uint64
	ViewLatency         time.Duration
	UpdateLatency       time.Duration
	SnapshotsOpened     uint64
	SnapshotsClosed     uint64
	SnapshotsActive     uint64
	BatchCommits        uint64
	BatchItems          uint64
	SnapshotBytes       uint64
	SnapshotDuration    time.Duration
	SnapshotMaxBytes    uint64
	AvgSnapshotBytes    float64
	AvgSnapshotDuration time.Duration
}

type dbMetricsState struct {
	viewTx, updateTx         atomic.Uint64
	viewErr, updateErr       atomic.Uint64
	viewNanos, updateNanos   atomic.Uint64
	snapOpen, snapClose      atomic.Uint64
	snapActive               atomic.Int64
	batchCommits, batchItems atomic.Uint64
	snapBytes, snapNanos     atomic.Uint64
	snapMaxBytes             atomic.Uint64
}

func (m *dbMetricsState) snapshot() DBMetrics {
	viewNanos := m.viewNanos.Load()
	updateNanos := m.updateNanos.Load()
	snapOpen := m.snapOpen.Load()
	snapBytes := m.snapBytes.Load()
	snapNanos := m.snapNanos.Load()
	avgSnapBytes := 0.0
	avgSnapNanos := time.Duration(0)
	if snapOpen > 0 {
		avgSnapBytes = float64(snapBytes) / float64(snapOpen)
		avgSnapNanos = time.Duration(snapNanos / snapOpen)
	}
	return DBMetrics{
		ViewTransactions: m.viewTx.Load(), UpdateTransactions: m.updateTx.Load(),
		ViewErrors: m.viewErr.Load(), UpdateErrors: m.updateErr.Load(),
		ViewLatency: time.Duration(viewNanos), UpdateLatency: time.Duration(updateNanos),
		SnapshotsOpened: snapOpen, SnapshotsClosed: m.snapClose.Load(),
		SnapshotsActive: uint64(maxInt64(m.snapActive.Load(), 0)),
		BatchCommits:    m.batchCommits.Load(), BatchItems: m.batchItems.Load(),
		SnapshotBytes: snapBytes, SnapshotDuration: time.Duration(snapNanos),
		SnapshotMaxBytes: m.snapMaxBytes.Load(), AvgSnapshotBytes: avgSnapBytes,
		AvgSnapshotDuration: avgSnapNanos,
	}
}

func maxInt64(v, min int64) int64 {
	if v < min {
		return min
	}
	return v
}

// Metrics returns a point-in-time snapshot of database-level transaction and
// snapshot activity.
func (db *DB) Metrics() DBMetrics {
	if db == nil || db.metrics == nil {
		return DBMetrics{}
	}
	return db.metrics.snapshot()
}

// ResetMetrics resets UDB's database-level counters. It does not affect
// persisted data, pipeline metrics, integrity state, or recovery metadata.
func (db *DB) ResetMetrics() {
	if db == nil || db.metrics == nil {
		return
	}
	db.metrics.viewTx.Store(0)
	db.metrics.updateTx.Store(0)
	db.metrics.viewErr.Store(0)
	db.metrics.updateErr.Store(0)
	db.metrics.viewNanos.Store(0)
	db.metrics.updateNanos.Store(0)
	db.metrics.snapOpen.Store(0)
	db.metrics.snapClose.Store(0)
	db.metrics.batchCommits.Store(0)
	db.metrics.batchItems.Store(0)
}

func (db *DB) recordView(start time.Time, err error) {
	if db == nil || db.metrics == nil {
		return
	}
	db.metrics.viewTx.Add(1)
	db.metrics.viewNanos.Add(uint64(time.Since(start)))
	if err != nil {
		db.metrics.viewErr.Add(1)
	}
}

func (db *DB) recordUpdate(start time.Time, err error) {
	if db == nil || db.metrics == nil {
		return
	}
	db.metrics.updateTx.Add(1)
	db.metrics.updateNanos.Add(uint64(time.Since(start)))
	if err != nil {
		db.metrics.updateErr.Add(1)
	}
}

func (db *DB) recordSnapshotMetrics(duration time.Duration, bytes uint64) {
	if db == nil || db.metrics == nil {
		return
	}
	db.metrics.snapBytes.Add(bytes)
	db.metrics.snapNanos.Add(uint64(duration))
	for {
		old := db.metrics.snapMaxBytes.Load()
		if old >= bytes {
			return
		}
		if db.metrics.snapMaxBytes.CompareAndSwap(old, bytes) {
			return
		}
	}
}
