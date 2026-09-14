package udb

import (
	"context"
	"sync"

	bolt "go.etcd.io/bbolt"
)

// lifecycle coordinates admission and draining of managed UDB operations.
//
// bbolt remains responsible for transaction-level concurrency. UDB does not
// serialize normal View/Update calls with a global RWMutex. A short mutex only
// protects the admission state; WaitGroup tracks operations already admitted.
//
// Normal state:
//   - View/Update can run concurrently.
//   - begin() holds mu only while checking state and calling Add(1).
//
// Quiescing state:
//   - quiescing=true closes the admission gate atomically.
//   - already admitted operations continue and call end()/Done().
//   - new operations wait until resume().
//   - once active reaches zero, the caller owns a fully drained lifecycle.
type lifecycle struct {
	mu sync.Mutex
	wg sync.WaitGroup

	cond      *sync.Cond
	active    int
	quiescing bool
	closed    bool
}

func (l *lifecycle) initLocked() {
	if l.cond == nil {
		l.cond = sync.NewCond(&l.mu)
	}
}

// begin admits one managed operation and returns the exact bbolt DB handle
// active at admission time. Returning the handle avoids accessing db.boltDB
// again after the admission lock is released.
func (l *lifecycle) begin(db *DB) (*bolt.DB, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.initLocked()

	for l.quiescing {
		if l.closed || db.boltDBUnsafe() == nil {
			return nil, ErrDatabaseClosed
		}
		l.cond.Wait()
	}
	if l.closed || db.boltDBUnsafe() == nil {
		return nil, ErrDatabaseClosed
	}

	// Add and the quiescing transition are protected by the same mutex. This
	// prevents the classic WaitGroup Add/Wait race.
	l.wg.Add(1)
	l.active++
	return db.boltDBUnsafe(), nil
}

func (l *lifecycle) end() {
	l.wg.Done()

	l.mu.Lock()
	if l.active > 0 {
		l.active--
	}
	if l.cond != nil {
		l.cond.Broadcast()
	}
	l.mu.Unlock()
}

// quiesce closes admission and waits for all operations that were already
// admitted. New operations wait in begin() until resume(). If ctx expires,
// the admission gate is restored and the caller gets ctx.Err().
func (l *lifecycle) quiesce(ctx context.Context, db *DB) error {
	if ctx == nil {
		ctx = context.Background()
	}

	l.mu.Lock()
	l.initLocked()

	for l.quiescing {
		if l.closed {
			l.mu.Unlock()
			return ErrDatabaseClosed
		}
		if err := ctx.Err(); err != nil {
			l.mu.Unlock()
			return err
		}
		l.cond.Wait()
	}

	if l.closed || db.boltDBUnsafe() == nil {
		l.mu.Unlock()
		return ErrDatabaseClosed
	}

	l.quiescing = true
	l.mu.Unlock()

	// sync.WaitGroup has no context-aware Wait. active+cond lets us wait with
	// cancellation while the WaitGroup remains the authoritative operation
	// counter. A tiny watcher only wakes cond when the context is cancelled.
	wake := make(chan struct{})
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		select {
		case <-ctx.Done():
			l.mu.Lock()
			if l.cond != nil {
				l.cond.Broadcast()
			}
			l.mu.Unlock()
		case <-wake:
		}
	}()
	defer func() {
		close(wake)
		<-watchDone
	}()

	l.mu.Lock()
	for l.active > 0 && ctx.Err() == nil {
		l.cond.Wait()
	}
	active := l.active
	l.mu.Unlock()

	if err := ctx.Err(); err != nil && active > 0 {
		l.resume()
		return err
	}

	// active==0 means every admitted operation has called Done(). Wait is
	// therefore immediate, while retaining the explicit WaitGroup guarantee.
	l.wg.Wait()
	return nil
}

func (l *lifecycle) resume() {
	l.mu.Lock()
	l.quiescing = false
	l.initLocked()
	l.cond.Broadcast()
	l.mu.Unlock()
}

func (l *lifecycle) markClosed() {
	l.mu.Lock()
	l.closed = true
	l.quiescing = false
	l.initLocked()
	l.cond.Broadcast()
	l.mu.Unlock()
}

func (l *lifecycle) isClosed() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.closed
}

// boltDBUnsafe must only be called while lifecycle.mu is held.
func (db *DB) boltDBUnsafe() *bolt.DB {
	return db.boltDB
}

// currentDB returns the current bbolt handle while synchronizing access to the
// handle pointer. It does not serialize operations performed on that handle.
func (l *lifecycle) currentDB(db *DB) *bolt.DB {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	return db.boltDB
}

// setDB changes the bbolt handle pointer under the lifecycle mutex. The caller
// must ensure the lifecycle is quiesced and that no managed operation can
// concurrently use the old handle.
func (l *lifecycle) setDB(db *DB, d *bolt.DB) {
	l.mu.Lock()
	db.boltDB = d
	l.mu.Unlock()
}
