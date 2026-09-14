package udb

import (
	"context"
	"errors"

	bolt "go.etcd.io/bbolt"
)

var (
	ErrBatchCommitted = errors.New("udb: batch already committed")
	ErrBatchClosed    = errors.New("udb: batch is closed")
)

// Batch is a reusable transaction builder. It is not safe for concurrent
// mutation. Inputs are copied when appended, so callers may reuse their
// buffers before Commit.
type Batch struct {
	db        *DB
	ops       []writeOp
	committed bool
	closed    bool
}

// NewBatch creates an empty atomic transaction builder.
func (db *DB) NewBatch() (*Batch, error) {
	if db == nil {
		return nil, ErrDatabaseClosed
	}
	if db.opts.ReadOnly {
		return nil, bolt.ErrDatabaseReadOnly
	}
	return &Batch{db: db, ops: make([]writeOp, 0, 16)}, nil
}

func (b *Batch) ensureOpen() error {
	if b == nil || b.db == nil {
		return ErrBatchClosed
	}
	if b.closed {
		return ErrBatchClosed
	}
	if b.committed {
		return ErrBatchCommitted
	}
	return nil
}

func (b *Batch) HSet(name string, key, value []byte) error {
	if err := b.ensureOpen(); err != nil {
		return err
	}
	if err := validName(name); err != nil {
		return err
	}
	if err := validKey(key); err != nil {
		return err
	}
	b.ops = append(b.ops, writeOp{kind: writeHSet, name: name, key: cloneBytes(key), value: cloneBytes(value)})
	return nil
}

func (b *Batch) HDel(name string, key []byte) error {
	if err := b.ensureOpen(); err != nil {
		return err
	}
	if err := validName(name); err != nil {
		return err
	}
	if err := validKey(key); err != nil {
		return err
	}
	b.ops = append(b.ops, writeOp{kind: writeHDel, name: name, key: cloneBytes(key)})
	return nil
}

func (b *Batch) ZSet(name string, key []byte, score uint64) error {
	if err := b.ensureOpen(); err != nil {
		return err
	}
	if err := validName(name); err != nil {
		return err
	}
	if err := validKey(key); err != nil {
		return err
	}
	b.ops = append(b.ops, writeOp{kind: writeZSet, name: name, key: cloneBytes(key), score: score})
	return nil
}

func (b *Batch) ZDel(name string, key []byte) error {
	if err := b.ensureOpen(); err != nil {
		return err
	}
	if err := validName(name); err != nil {
		return err
	}
	if err := validKey(key); err != nil {
		return err
	}
	b.ops = append(b.ops, writeOp{kind: writeZDel, name: name, key: cloneBytes(key)})
	return nil
}

func (b *Batch) Len() int {
	if b == nil {
		return 0
	}
	return len(b.ops)
}

// Abort discards the pending operations. It is safe to call more than once.
func (b *Batch) Abort() {
	if b == nil {
		return
	}
	b.closed = true
	b.ops = nil
}

func (b *Batch) validate() error {
	if err := b.ensureOpen(); err != nil {
		return err
	}
	for i := range b.ops {
		if err := validateWriteOp(&b.ops[i]); err != nil {
			return err
		}
	}
	return nil
}

// Commit executes the complete batch in one managed bbolt write transaction.
// All operations are applied in submission order. If any operation fails,
// bbolt rolls the entire transaction back.
func (b *Batch) Commit() error {
	return b.CommitContext(context.Background())
}

func (b *Batch) CommitContext(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := b.validate(); err != nil {
		return err
	}
	if len(b.ops) == 0 {
		b.committed = true
		return nil
	}
	if err := b.db.Update(func(tx *Tx) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		return applyWriteOps(tx, b.ops)
	}); err != nil {
		return err
	}
	b.committed = true
	recordBatchMetrics(b.db, len(b.ops))
	return nil
}
