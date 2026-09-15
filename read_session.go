package udb

import "context"

// ReadSession groups a series of high-level reads behind one reusable object.
// Each operation uses a short managed read transaction; ReadSession never holds
// a bbolt read transaction across calls. Therefore a session may remain alive
// while writes, compaction, and Close continue to use the normal lifecycle.
type ReadSession struct {
	db *DB
}

// NewReadSession creates a reusable read session.
func (db *DB) NewReadSession() (*ReadSession, error) {
	if db == nil {
		return nil, ErrDatabaseClosed
	}
	if db.lifecycle.currentDB(db) == nil {
		return nil, ErrDatabaseClosed
	}
	return &ReadSession{db: db}, nil
}

func (s *ReadSession) check() error {
	if s == nil || s.db == nil {
		return ErrDatabaseClosed
	}
	return nil
}

func (s *ReadSession) HGet(name string, key []byte) *Reply {
	if err := s.check(); err != nil {
		return errorReply(err)
	}
	return s.db.HGet(name, key)
}

func (s *ReadSession) HGetInt(name string, key []byte) (uint64, error) {
	if err := s.check(); err != nil {
		return 0, err
	}
	return s.db.HGetInt(name, key)
}

func (s *ReadSession) ZGet(name string, key []byte) *Reply {
	if err := s.check(); err != nil {
		return errorReply(err)
	}
	return s.db.ZGet(name, key)
}

func (s *ReadSession) ZScore(name string, key []byte) (uint64, error) {
	if err := s.check(); err != nil {
		return 0, err
	}
	return s.db.ZScore(name, key)
}

func (s *ReadSession) HGetBatch(name string, keys [][]byte) *Reply {
	if err := s.check(); err != nil {
		return errorReply(err)
	}
	return s.db.HGetBatch(name, keys)
}

func (s *ReadSession) ZGetBatch(name string, keys [][]byte) *Reply {
	if err := s.check(); err != nil {
		return errorReply(err)
	}
	return s.db.ZGetBatch(name, keys)
}

func (s *ReadSession) HScan(name string, keyStart []byte, limit int) *Reply {
	if err := s.check(); err != nil {
		return errorReply(err)
	}
	var r *Reply
	if err := s.db.View(func(tx *Tx) error { r = s.readEngine(tx).HScan(name, keyStart, limit); return r.ErrOrNil() }); err != nil {
		return errorReply(err)
	}
	return r
}

func (s *ReadSession) HRScan(name string, keyStart []byte, limit int) *Reply {
	if err := s.check(); err != nil {
		return errorReply(err)
	}
	var r *Reply
	if err := s.db.View(func(tx *Tx) error { r = s.readEngine(tx).HRScan(name, keyStart, limit); return r.ErrOrNil() }); err != nil {
		return errorReply(err)
	}
	return r
}

func (s *ReadSession) ZScan(name string, keyStart, scoreStart []byte, limit int) *Reply {
	if err := s.check(); err != nil {
		return errorReply(err)
	}
	var r *Reply
	if err := s.db.View(func(tx *Tx) error { r = s.readEngine(tx).ZScan(name, keyStart, scoreStart, limit); return r.ErrOrNil() }); err != nil {
		return errorReply(err)
	}
	return r
}

func (s *ReadSession) ZRScan(name string, keyStart, scoreStart []byte, limit int) *Reply {
	if err := s.check(); err != nil {
		return errorReply(err)
	}
	var r *Reply
	if err := s.db.View(func(tx *Tx) error {
		r = s.readEngine(tx).ZRScan(name, keyStart, scoreStart, limit)
		return r.ErrOrNil()
	}); err != nil {
		return errorReply(err)
	}
	return r
}

func (s *ReadSession) readEngine(tx *Tx) *ReadEngine {
	r := newReadEngine(tx)
	return &r
}

// Read executes multiple reads in one short-lived managed transaction. The
// callback must not retain tx or the ReadEngine after it returns. This is the
// preferred API when callers need a consistent multi-read view.
func (s *ReadSession) Read(fn func(*ReadEngine) error) error {
	return s.ReadContext(context.Background(), fn)
}

func (s *ReadSession) ReadContext(ctx context.Context, fn func(*ReadEngine) error) error {
	if err := s.check(); err != nil {
		return err
	}
	if fn == nil {
		return ErrNilTransactionFunc
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.db.View(func(tx *Tx) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		r := newReadEngine(tx)
		return fn(&r)
	})
}

// Close releases the session handle. It does not close the database and is
// idempotent. A closed session behaves like a database-closed handle.
func (s *ReadSession) Close() error {
	if s == nil {
		return nil
	}
	s.db = nil
	return nil
}
