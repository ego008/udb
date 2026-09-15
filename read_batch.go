package udb

import "context"

// ReadBatchItemKind identifies an operation in ReadBatch.
type ReadBatchItemKind uint8

const (
	ReadBatchHGet ReadBatchItemKind = iota + 1
	ReadBatchZScore
)

// ReadBatchResult is valid only during the Execute callback. Key and Value
// point at bbolt memory and must not be retained. Score is copied by value.
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
// It is intentionally callback-based: the callback receives zero-copy Bolt
// slices and therefore avoids result allocation for large read batches.
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

func (b *ReadBatch) add(kind ReadBatchItemKind, name string, key []byte) error {
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
	// Copy caller input so the batch is stable even if the caller reuses its buffer.
	b.ops = append(b.ops, readBatchOp{kind: kind, name: name, key: cloneBytes(key)})
	return nil
}

func (b *ReadBatch) HGet(name string, key []byte) error {
	return b.add(ReadBatchHGet, name, key)
}

func (b *ReadBatch) ZScore(name string, key []byte) error {
	return b.add(ReadBatchZScore, name, key)
}

func (b *ReadBatch) Len() int {
	if b == nil {
		return 0
	}
	return len(b.ops)
}

// Execute runs all reads in one managed short-lived read transaction.
// Result bytes are only valid inside fn. This is the zero-copy read path.
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
		engine, err := NewReadEngine(tx)
		if err != nil {
			return err
		}
		for _, op := range b.ops {
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
					if err := fn(ReadBatchResult{Kind: op.kind, Key: op.key, Found: false}); err != nil {
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
					if err := fn(ReadBatchResult{Kind: op.kind, Key: op.key, Found: false}); err != nil {
						return err
					}
					continue
				}
				value := bucket.Get(op.key)
				if value == nil {
					if err := fn(ReadBatchResult{Kind: op.kind, Key: op.key, Found: false}); err != nil {
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
	})
	if err == nil {
		b.closed = true
		b.ops = nil
	}
	return err
}

func (b *ReadBatch) Close() error {
	if b == nil {
		return nil
	}
	b.closed = true
	b.ops = nil
	return nil
}
