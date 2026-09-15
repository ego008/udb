package udb

import (
	"bytes"
	"context"

	bolt "go.etcd.io/bbolt"
)

// ReadBatchItemKind identifies an operation in ReadBatch.
type ReadBatchItemKind uint8

const (
	ReadBatchHGet ReadBatchItemKind = iota + 1
	ReadBatchZScore
)

// ReadBatchResult is valid only during the Execute callback. Key and Value
// point at bbolt memory for Borrowed operations and at batch-owned memory for
// the ownership-safe HGet/ZScore operations. They must not be retained after
// the callback returns. Score is copied by value.
type ReadBatchResult struct {
	Kind  ReadBatchItemKind
	Key   []byte
	Value []byte
	Score uint64
	Found bool
}

type readBatchOp struct {
	kind ReadBatchItemKind
	name string
	key  []byte
}

// ReadBatch groups heterogeneous point reads into one short View transaction.
// The default HGet/ZScore methods copy caller input. HGetBorrowed/ZScoreBorrowed
// avoid that copy and require the caller to keep the input key unchanged until
// Execute returns.
type ReadBatch struct {
	db     *DB
	ops    []readBatchOp
	closed bool
}

var (
	ErrReadBatchClosed = errReadBatchClosed
	ErrReadBatchEmpty  = errReadBatchEmpty
)

func (db *DB) NewReadBatch() (*ReadBatch, error) {
	if db == nil {
		return nil, ErrDatabaseClosed
	}
	return &ReadBatch{db: db, ops: make([]readBatchOp, 0, 16)}, nil
}

func (b *ReadBatch) checkOpen() error {
	if b == nil || b.db == nil {
		return ErrDatabaseClosed
	}
	if b.closed {
		return ErrReadBatchClosed
	}
	return nil
}

func (b *ReadBatch) add(kind ReadBatchItemKind, name string, key []byte, borrowed bool) error {
	if err := b.checkOpen(); err != nil {
		return err
	}
	if kind != ReadBatchHGet && kind != ReadBatchZScore {
		return ErrInvalidScore
	}
	if err := validName(name); err != nil {
		return err
	}
	if err := validKey(key); err != nil {
		return err
	}
	if !borrowed {
		key = cloneBytes(key)
	}
	b.ops = append(b.ops, readBatchOp{kind: kind, name: name, key: key})
	return nil
}

// HGet adds an ownership-safe Hash lookup. The key is copied immediately.
func (b *ReadBatch) HGet(name string, key []byte) error {
	return b.add(ReadBatchHGet, name, key, false)
}

// HGetBorrowed adds a zero-copy Hash lookup. The caller must not modify key
// until Execute/ExecuteContext returns.
func (b *ReadBatch) HGetBorrowed(name string, key []byte) error {
	return b.add(ReadBatchHGet, name, key, true)
}

// ZScore adds an ownership-safe ZSet score lookup. The key is copied immediately.
func (b *ReadBatch) ZScore(name string, key []byte) error {
	return b.add(ReadBatchZScore, name, key, false)
}

// ZScoreBorrowed adds a zero-copy ZSet lookup. The caller must not modify key
// until Execute/ExecuteContext returns.
func (b *ReadBatch) ZScoreBorrowed(name string, key []byte) error {
	return b.add(ReadBatchZScore, name, key, true)
}

func (b *ReadBatch) Len() int {
	if b == nil {
		return 0
	}
	return len(b.ops)
}

// Execute runs all reads in one managed short-lived read transaction.
// Homogeneous batches with keys already sorted in ascending byte order use one
// cursor plus sequential Next calls instead of one B-tree search per key.
func (b *ReadBatch) Execute(fn func(ReadBatchResult) error) error {
	return b.ExecuteContext(context.Background(), fn)
}

func (b *ReadBatch) ExecuteContext(ctx context.Context, fn func(ReadBatchResult) error) error {
	if err := b.checkOpen(); err != nil {
		return err
	}
	if fn == nil {
		return errReadBatchNilCallback
	}
	if len(b.ops) == 0 {
		return ErrReadBatchEmpty
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	err := b.db.ReadTransaction(func(tx *Tx) error {
		engine := newReadEngine(tx)
		if readBatchHomogeneousSorted(b.ops) && planReadOpsFast(b.ops, defaultReadPlannerOptions()).Path == ReadPathCursor {
			return executeReadBatchSorted(ctx, &engine, b.ops, fn)
		}
		return executeReadBatchPoint(ctx, &engine, b.ops, fn)
	})
	if err == nil {
		b.closed = true
		b.ops = nil
	}
	return err
}

func readBatchHomogeneousSorted(ops []readBatchOp) bool {
	if len(ops) < 2 {
		return false
	}
	first := ops[0]
	for i := 1; i < len(ops); i++ {
		if ops[i].kind != first.kind || ops[i].name != first.name || bytes.Compare(ops[i-1].key, ops[i].key) > 0 {
			return false
		}
	}
	return true
}

func executeReadBatchPoint(ctx context.Context, engine *ReadEngine, ops []readBatchOp, fn func(ReadBatchResult) error) error {
	for _, op := range ops {
		if err := ctx.Err(); err != nil {
			return err
		}
		switch op.kind {
		case ReadBatchHGet:
			bucket, err := engine.hashBucket(op.name)
			if err != nil {
				return err
			}
			if bucket == nil {
				if err := fn(ReadBatchResult{Kind: op.kind, Key: op.key}); err != nil {
					return err
				}
				continue
			}
			value := bucket.Get(op.key)
			if err := fn(ReadBatchResult{Kind: op.kind, Key: op.key, Value: value, Found: value != nil}); err != nil {
				return err
			}
		case ReadBatchZScore:
			bucket, err := engine.zScoreBucket(op.name)
			if err != nil {
				return err
			}
			if bucket == nil {
				if err := fn(ReadBatchResult{Kind: op.kind, Key: op.key}); err != nil {
					return err
				}
				continue
			}
			value := bucket.Get(op.key)
			if value == nil {
				if err := fn(ReadBatchResult{Kind: op.kind, Key: op.key}); err != nil {
					return err
				}
				continue
			}
			score, err := DecodeUint64(value)
			if err != nil {
				return err
			}
			if err := fn(ReadBatchResult{Kind: op.kind, Key: op.key, Score: score, Found: true}); err != nil {
				return err
			}
		default:
			return errInvalidReadBatchOp
		}
	}
	return nil
}

func executeReadBatchSorted(ctx context.Context, engine *ReadEngine, ops []readBatchOp, fn func(ReadBatchResult) error) error {
	first := ops[0]
	if first.kind == ReadBatchHGet {
		bucket, err := engine.hashBucket(first.name)
		if err != nil {
			return err
		}
		if bucket == nil {
			for _, op := range ops {
				if err := ctx.Err(); err != nil {
					return err
				}
				if err := fn(ReadBatchResult{Kind: op.kind, Key: op.key}); err != nil {
					return err
				}
			}
			return nil
		}
		c := bucket.Cursor()
		k, v := c.Seek(first.key)
		for _, op := range ops {
			if err := ctx.Err(); err != nil {
				return err
			}
			for k != nil && bytes.Compare(k, op.key) < 0 {
				k, v = c.Next()
			}
			if k != nil && bytes.Equal(k, op.key) {
				if err := fn(ReadBatchResult{Kind: op.kind, Key: op.key, Value: v, Found: true}); err != nil {
					return err
				}
			} else if err := fn(ReadBatchResult{Kind: op.kind, Key: op.key}); err != nil {
				return err
			}
		}
		return nil
	}

	if first.kind == ReadBatchZScore {
		bucket, err := engine.zScoreBucket(first.name)
		if err != nil {
			return err
		}
		if bucket == nil {
			for _, op := range ops {
				if err := ctx.Err(); err != nil {
					return err
				}
				if err := fn(ReadBatchResult{Kind: op.kind, Key: op.key}); err != nil {
					return err
				}
			}
			return nil
		}
		c := bucket.Cursor()
		k, v := c.Seek(first.key)
		for _, op := range ops {
			if err := ctx.Err(); err != nil {
				return err
			}
			for k != nil && bytes.Compare(k, op.key) < 0 {
				k, v = c.Next()
			}
			if k != nil && bytes.Equal(k, op.key) {
				score, err := DecodeUint64(v)
				if err != nil {
					return err
				}
				if err := fn(ReadBatchResult{Kind: op.kind, Key: op.key, Score: score, Found: true}); err != nil {
					return err
				}
			} else if err := fn(ReadBatchResult{Kind: op.kind, Key: op.key}); err != nil {
				return err
			}
		}
		return nil
	}
	return errInvalidReadBatchOp
}

// HGetMany executes a homogeneous Hash batch in one short transaction. It
// borrows the input keys and callback values for the duration of the callback.
func (db *DB) HGetMany(name string, keys [][]byte, fn func(index int, key, value []byte, found bool) error) error {
	return db.HGetManyContext(context.Background(), name, keys, fn)
}

func (db *DB) HGetManyContext(ctx context.Context, name string, keys [][]byte, fn func(index int, key, value []byte, found bool) error) error {
	if db == nil {
		return ErrDatabaseClosed
	}
	if fn == nil {
		return errReadBatchNilCallback
	}
	if err := validName(name); err != nil {
		return err
	}
	for _, key := range keys {
		if err := validKey(key); err != nil {
			return err
		}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return db.View(func(tx *Tx) error {
		r := newReadEngine(tx)
		b, err := r.hashBucket(name)
		if err != nil {
			return err
		}
		if b == nil {
			for i, key := range keys {
				if err := ctx.Err(); err != nil {
					return err
				}
				if err := fn(i, key, nil, false); err != nil {
					return err
				}
			}
			return nil
		}
		if planReadKeysFast(keys, defaultReadPlannerOptions()).Path == ReadPathCursor {
			return scanHashMany(ctx, b, keys, fn)
		}
		for i, key := range keys {
			if err := ctx.Err(); err != nil {
				return err
			}
			v := b.Get(key)
			if err := fn(i, key, v, v != nil); err != nil {
				return err
			}
		}
		return nil
	})
}

// ZScoreMany executes a homogeneous ZSet batch in one short transaction.
// Input keys and callback buffers are borrowed for the callback lifetime.
func (db *DB) ZScoreMany(name string, keys [][]byte, fn func(index int, key []byte, score uint64, found bool) error) error {
	return db.ZScoreManyContext(context.Background(), name, keys, fn)
}

func (db *DB) ZScoreManyContext(ctx context.Context, name string, keys [][]byte, fn func(index int, key []byte, score uint64, found bool) error) error {
	if db == nil {
		return ErrDatabaseClosed
	}
	if fn == nil {
		return errReadBatchNilCallback
	}
	if err := validName(name); err != nil {
		return err
	}
	for _, key := range keys {
		if err := validKey(key); err != nil {
			return err
		}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return db.View(func(tx *Tx) error {
		r := newReadEngine(tx)
		b, err := r.zScoreBucket(name)
		if err != nil {
			return err
		}
		if b == nil {
			for i, key := range keys {
				if err := ctx.Err(); err != nil {
					return err
				}
				if err := fn(i, key, 0, false); err != nil {
					return err
				}
			}
			return nil
		}
		if planReadKeysFast(keys, defaultReadPlannerOptions()).Path == ReadPathCursor {
			return scanZScoreMany(ctx, b, keys, fn)
		}
		for i, key := range keys {
			if err := ctx.Err(); err != nil {
				return err
			}
			v := b.Get(key)
			if v == nil {
				if err := fn(i, key, 0, false); err != nil {
					return err
				}
				continue
			}
			score, err := DecodeUint64(v)
			if err != nil {
				return err
			}
			if err := fn(i, key, score, true); err != nil {
				return err
			}
		}
		return nil
	})
}

func scanHashMany(ctx context.Context, b *bolt.Bucket, keys [][]byte, fn func(int, []byte, []byte, bool) error) error {
	c := b.Cursor()
	k, v := c.First()
	for i, key := range keys {
		if err := ctx.Err(); err != nil {
			return err
		}
		for k != nil && bytes.Compare(k, key) < 0 {
			k, v = c.Next()
		}
		if k != nil && bytes.Equal(k, key) {
			if err := fn(i, key, v, true); err != nil {
				return err
			}
		} else if err := fn(i, key, nil, false); err != nil {
			return err
		}
	}
	return nil
}

func scanZScoreMany(ctx context.Context, b *bolt.Bucket, keys [][]byte, fn func(int, []byte, uint64, bool) error) error {
	c := b.Cursor()
	k, v := c.First()
	for i, key := range keys {
		if err := ctx.Err(); err != nil {
			return err
		}
		for k != nil && bytes.Compare(k, key) < 0 {
			k, v = c.Next()
		}
		if k != nil && bytes.Equal(k, key) {
			score, err := DecodeUint64(v)
			if err != nil {
				return err
			}
			if err := fn(i, key, score, true); err != nil {
				return err
			}
		} else if err := fn(i, key, 0, false); err != nil {
			return err
		}
	}
	return nil
}

func (b *ReadBatch) Close() error {
	if b == nil {
		return nil
	}
	b.closed = true
	b.ops = nil
	return nil
}
