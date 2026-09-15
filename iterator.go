package udb

import "context"

// HIterator iterates over an ownership-safe in-memory result captured from one
// short read transaction. It never keeps a bbolt transaction open between Next
// calls, so a caller may consume it slowly without blocking database writes.
type HIterator interface {
	Next() bool
	Key() []byte
	Value() []byte
	Err() error
	Close() error
}

type hIterator struct {
	items  []Entry
	pos    int
	key    []byte
	value  []byte
	err    error
	closed bool
}

func (it *hIterator) Next() bool {
	if it == nil || it.closed || it.err != nil {
		return false
	}
	if it.pos >= len(it.items) {
		return false
	}
	e := it.items[it.pos]
	it.pos++
	it.key, it.value = e.Key, e.Value
	return true
}
func (it *hIterator) Key() []byte {
	if it == nil || it.closed {
		return nil
	}
	return it.key
}
func (it *hIterator) Value() []byte {
	if it == nil || it.closed {
		return nil
	}
	return it.value
}
func (it *hIterator) Err() error {
	if it == nil {
		return ErrDatabaseClosed
	}
	return it.err
}
func (it *hIterator) Close() error {
	if it == nil {
		return nil
	}
	it.closed = true
	it.items = nil
	it.key = nil
	it.value = nil
	return nil
}

// ZIterator iterates over an ownership-safe in-memory ZSet result captured from
// one short read transaction.
type ZIterator interface {
	Next() bool
	Member() []byte
	Score() uint64
	Err() error
	Close() error
}

type zIteratorItem struct {
	member []byte
	score  uint64
}
type zIterator struct {
	items  []zIteratorItem
	pos    int
	member []byte
	score  uint64
	err    error
	closed bool
}

func (it *zIterator) Next() bool {
	if it == nil || it.closed || it.err != nil {
		return false
	}
	if it.pos >= len(it.items) {
		return false
	}
	e := it.items[it.pos]
	it.pos++
	it.member, it.score = e.member, e.score
	return true
}
func (it *zIterator) Member() []byte {
	if it == nil || it.closed {
		return nil
	}
	return it.member
}
func (it *zIterator) Score() uint64 {
	if it == nil || it.closed {
		return 0
	}
	return it.score
}
func (it *zIterator) Err() error {
	if it == nil {
		return ErrDatabaseClosed
	}
	return it.err
}
func (it *zIterator) Close() error {
	if it == nil {
		return nil
	}
	it.closed = true
	it.items = nil
	it.member = nil
	return nil
}

func (db *DB) NewHIterator(name string, keyStart []byte, limit int) (HIterator, error) {
	return db.NewHIteratorContext(context.Background(), name, keyStart, limit, false)
}
func (db *DB) NewHRIterator(name string, keyStart []byte, limit int) (HIterator, error) {
	return db.NewHIteratorContext(context.Background(), name, keyStart, limit, true)
}
func (db *DB) NewHIteratorContext(ctx context.Context, name string, keyStart []byte, limit int, reverse bool) (HIterator, error) {
	if db == nil {
		return nil, ErrDatabaseClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validLimit(limit); err != nil {
		return nil, err
	}
	it := &hIterator{items: make([]Entry, 0, limit)}
	err := db.View(func(tx *Tx) error {
		r, err := NewReadEngine(tx)
		if err != nil {
			return err
		}
		if reverse {
			return captureHIterator(r, name, keyStart, limit, true, it)
		}
		return captureHIterator(r, name, keyStart, limit, false, it)
	})
	if err != nil {
		_ = it.Close()
		return nil, err
	}
	return it, nil
}

func captureHIterator(r *ReadEngine, name string, keyStart []byte, limit int, reverse bool, it *hIterator) error {
	if r == nil || r.tx == nil {
		return ErrInvalidTx
	}
	if err := validName(name); err != nil {
		return err
	}
	if err := validLimit(limit); err != nil {
		return err
	}
	b, err := r.hashBucket(name)
	if err != nil {
		return err
	}
	if b == nil {
		return nil
	}
	c := b.Cursor()
	var k, v []byte
	if reverse {
		k, v = seekReverse(c, keyStart, len(keyStart) > 0)
	} else {
		k, v = seekForward(c, keyStart, len(keyStart) > 0)
	}
	for n := 0; k != nil && n < limit; n++ {
		it.items = append(it.items, Entry{Key: cloneBytes(k), Value: cloneBytes(v)})
		if reverse {
			k, v = c.Prev()
		} else {
			k, v = c.Next()
		}
	}
	return nil
}

func (db *DB) NewZIterator(name string, keyStart, scoreStart []byte, limit int) (ZIterator, error) {
	return db.NewZIteratorContext(context.Background(), name, keyStart, scoreStart, limit, false)
}

func (db *DB) NewZRIterator(name string, keyStart, scoreStart []byte, limit int) (ZIterator, error) {
	return db.NewZIteratorContext(context.Background(), name, keyStart, scoreStart, limit, true)
}

func (db *DB) NewZIteratorContext(ctx context.Context, name string, keyStart, scoreStart []byte, limit int, reverse bool) (ZIterator, error) {
	if db == nil {
		return nil, ErrDatabaseClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validLimit(limit); err != nil {
		return nil, err
	}
	if len(scoreStart) != 0 && len(scoreStart) != uint64EncodedLen {
		return nil, ErrInvalidScore
	}
	it := &zIterator{items: make([]zIteratorItem, 0, limit)}
	err := db.View(func(tx *Tx) error {
		r, err := NewReadEngine(tx)
		if err != nil {
			return err
		}
		if err := validName(name); err != nil {
			return err
		}
		b, err := r.zIndexBucket(name)
		if err != nil {
			return err
		}
		if b == nil {
			return nil
		}
		c := b.Cursor()
		var k []byte
		if reverse {
			k, _ = zseekReverse(c, keyStart, scoreStart)
		} else {
			k, _ = zseekForward(c, keyStart, scoreStart)
		}
		for n := 0; k != nil && n < limit; n++ {
			if len(k) < uint64EncodedLen {
				return ErrInvalidScore
			}
			it.items = append(it.items, zIteratorItem{member: cloneBytes(k[uint64EncodedLen:]), score: binaryScore(k[:uint64EncodedLen])})
			if reverse {
				k, _ = c.Prev()
			} else {
				k, _ = c.Next()
			}
		}
		return nil
	})
	if err != nil {
		_ = it.Close()
		return nil, err
	}
	return it, nil
}
