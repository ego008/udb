package udb

import (
	"context"
	"errors"
	"time"

	bolt "go.etcd.io/bbolt"
)

// DB is the UDB database handle.
//
// bbolt owns transaction-level concurrency. UDB adds only lifecycle admission
// control so Close/CompactAndReplace can wait for managed operations and then
// safely replace the underlying bbolt database. The raw *bbolt.DB is no longer
// embedded or exposed by UDB V5.
type DB struct {
	boltDB      *bolt.DB
	opts        Options
	lifecycle   lifecycle
	maintenance *MaintenanceManager
	metrics     *dbMetricsState
}

func Open(path string) (*DB, error) { return OpenWithOptions(path, nil) }

func OpenWithOptions(path string, opts *Options) (*DB, error) {
	o := normalizeOptions(opts)
	if path == "" {
		return nil, errors.New("udb: empty database path")
	}
	if err := recoverInterruptedCompaction(path, o); err != nil {
		return nil, err
	}
	d, err := bolt.Open(path, 0600, o.boltOptions())
	if err != nil {
		return nil, err
	}
	db := &DB{boltDB: d, opts: o, metrics: &dbMetricsState{}}
	db.maintenance = newMaintenanceManager(db)
	if o.Maintenance.Enabled && !o.ReadOnly {
		if err := db.StartMaintenance(context.Background()); err != nil {
			_ = d.Close()
			db.lifecycle.setDB(db, nil)
			db.lifecycle.markClosed()
			return nil, err
		}
	}
	return db, nil
}

func (db *DB) Options() Options {
	if db == nil {
		return Options{}
	}
	return db.opts
}

// View runs a managed read transaction. The callback receives UDB's Tx rather
// than *bbolt.Tx, so the underlying transaction cannot escape UDB lifecycle
// tracking.
func (db *DB) View(fn func(*Tx) error) error {
	if db == nil {
		return ErrDatabaseClosed
	}
	if fn == nil {
		return ErrNilTransactionFunc
	}
	d, err := db.lifecycle.begin(db)
	if err != nil {
		return err
	}
	defer db.lifecycle.end()
	start := time.Now()
	err = d.View(func(tx *bolt.Tx) error {
		return fn(newTx(tx))
	})
	db.recordView(start, err)
	return err
}

// Update runs a managed write transaction. bbolt serializes write
// transactions; UDB does not add another global write lock.
func (db *DB) Update(fn func(*Tx) error) error {
	if db == nil {
		return ErrDatabaseClosed
	}
	if fn == nil {
		return ErrNilTransactionFunc
	}
	if db.opts.ReadOnly {
		return bolt.ErrDatabaseReadOnly
	}
	d, err := db.lifecycle.begin(db)
	if err != nil {
		return err
	}
	defer db.lifecycle.end()
	start := time.Now()
	err = d.Update(func(tx *bolt.Tx) error {
		return fn(newTx(tx))
	})
	db.recordUpdate(start, err)
	return err
}

// Close stops background maintenance, closes the admission gate, waits for
// every already-admitted managed operation to finish, and only then closes
// bbolt. Normal View/Update calls are not globally serialized while the DB is
// open; the gate is closed only for the final drain window. The lifecycle WaitGroup is deliberately used together with an
// admission mutex; calling WaitGroup.Wait without first blocking new Add calls
// would be unsafe.
func (db *DB) Close() error {
	if db == nil {
		return nil
	}
	db.StopMaintenance()
	if err := db.lifecycle.quiesce(context.Background(), db); err != nil {
		return nilIfClosed(err)
	}
	defer db.lifecycle.resume()

	d := db.lifecycle.currentDB(db)
	if d == nil {
		db.lifecycle.markClosed()
		return nil
	}
	err := d.Close()
	if err == nil {
		db.lifecycle.setDB(db, nil)
		db.lifecycle.markClosed()
	}
	return err
}

func nilIfClosed(err error) error {
	if err == ErrDatabaseClosed {
		return nil
	}
	return err
}
