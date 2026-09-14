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

type batchOpType uint8

const (
	batchHSet batchOpType = iota + 1
	batchHDel
	batchZSet
	batchZDel
)

type batchOp struct {
	op    batchOpType
	name  string
	key   BS
	value BS
	score uint64
}

// Batch is a reusable transaction builder. It is not safe for concurrent
// mutation. Inputs are copied when appended, so callers may reuse their
// buffers before Commit.
type Batch struct {
	db        *DB
	ops       []batchOp
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
	return &Batch{db: db, ops: make([]batchOp, 0, 16)}, nil
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
	b.ops = append(b.ops, batchOp{op: batchHSet, name: name, key: cloneBytes(key), value: cloneBytes(value)})
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
	b.ops = append(b.ops, batchOp{op: batchHDel, name: name, key: cloneBytes(key)})
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
	b.ops = append(b.ops, batchOp{op: batchZSet, name: name, key: cloneBytes(key), score: score})
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
	b.ops = append(b.ops, batchOp{op: batchZDel, name: name, key: cloneBytes(key)})
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
		if err := validName(b.ops[i].name); err != nil {
			return err
		}
		if err := validKey(b.ops[i].key); err != nil {
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
		for i := range b.ops {
			if err := ctx.Err(); err != nil {
				return err
			}
			op := &b.ops[i]
			var err error
			switch op.op {
			case batchHSet:
				err = b.db.Hset(tx, op.name, op.key, op.value)
			case batchHDel:
				err = b.db.Hdel(tx, op.name, op.key)
			case batchZSet:
				err = b.db.Zset(tx, op.name, op.key, op.score)
			case batchZDel:
				err = b.db.Zdel(tx, op.name, op.key)
			default:
				err = errors.New("udb: invalid batch operation")
			}
			if err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	b.committed = true
	if b.db.metrics != nil {
		b.db.metrics.batchCommits.Add(1)
		b.db.metrics.batchItems.Add(uint64(len(b.ops)))
	}
	return nil
}
