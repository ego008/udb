package udb

import (
	"encoding/binary"

	bolt "go.etcd.io/bbolt"
)

// ReadEngine is a transaction-scoped read facade. It centralizes bucket
// lookup and read ownership rules so HGet/ZGet/ZScore and scan operations use
// the same read path. It is valid only while the surrounding Tx callback is
// running.
type ReadEngine struct {
	tx *Tx

	hashName   string
	hash       *bolt.Bucket
	zScoreName string
	zScore     *bolt.Bucket
	zIndexName string
	zIndex     *bolt.Bucket
}

// NewReadEngine creates a transaction-scoped read engine.
func NewReadEngine(tx *Tx) (*ReadEngine, error) {
	if err := validTx(tx); err != nil {
		return nil, err
	}
	return &ReadEngine{tx: tx}, nil
}

func (r *ReadEngine) hashBucket(name string) (*bolt.Bucket, error) {
	if err := validName(name); err != nil {
		return nil, err
	}
	if r.hashName == name {
		return r.hash, nil
	}
	b := r.tx.bucket(bucketName(hashPrefix, name))
	r.hashName, r.hash = name, b
	return b, nil
}

func (r *ReadEngine) zScoreBucket(name string) (*bolt.Bucket, error) {
	if err := validName(name); err != nil {
		return nil, err
	}
	if r.zScoreName == name {
		return r.zScore, nil
	}
	b := r.tx.bucket(bucketName(zetScorePrefix, name))
	r.zScoreName, r.zScore = name, b
	return b, nil
}

func (r *ReadEngine) zIndexBucket(name string) (*bolt.Bucket, error) {
	if err := validName(name); err != nil {
		return nil, err
	}
	if r.zIndexName == name {
		return r.zIndex, nil
	}
	b := r.tx.bucket(bucketName(zetKeyPrefix, name))
	r.zIndexName, r.zIndex = name, b
	return b, nil
}

// HGet returns a copied value, safe after the transaction closes.
func (r *ReadEngine) HGet(name string, key []byte) *Reply {
	if r == nil || r.tx == nil {
		return errorReply(ErrInvalidTx)
	}
	if err := validKey(key); err != nil {
		return errorReply(err)
	}
	b, err := r.hashBucket(name)
	if err != nil {
		return errorReply(err)
	}
	if b == nil {
		return &Reply{State: bucketNotFound}
	}
	v := b.Get(key)
	if v == nil {
		return &Reply{State: keyNotFound}
	}
	return &Reply{State: replyOK, Data: []BS{cloneBytes(v)}}
}

// HGetInt reads a uint64 encoded Hash value without allocating a result
// buffer.
func (r *ReadEngine) HGetInt(name string, key []byte) (uint64, error) {
	if r == nil || r.tx == nil {
		return 0, ErrInvalidTx
	}
	if err := validKey(key); err != nil {
		return 0, err
	}
	b, err := r.hashBucket(name)
	if err != nil {
		return 0, err
	}
	if b == nil {
		return 0, ErrBucketNotFound
	}
	v := b.Get(key)
	if v == nil {
		return 0, ErrKeyNotFound
	}
	return DecodeUint64(v)
}

// ZGet returns a copied encoded score, safe after the transaction closes.
func (r *ReadEngine) ZGet(name string, key []byte) *Reply {
	if r == nil || r.tx == nil {
		return errorReply(ErrInvalidTx)
	}
	if err := validKey(key); err != nil {
		return errorReply(err)
	}
	b, err := r.zScoreBucket(name)
	if err != nil {
		return errorReply(err)
	}
	if b == nil {
		return &Reply{State: bucketNotFound}
	}
	v := b.Get(key)
	if v == nil {
		return &Reply{State: keyNotFound}
	}
	return &Reply{State: replyOK, Data: []BS{cloneBytes(v)}}
}

// ZScore returns a typed score without allocating an encoded score buffer.
func (r *ReadEngine) ZScore(name string, key []byte) (uint64, error) {
	if r == nil || r.tx == nil {
		return 0, ErrInvalidTx
	}
	if err := validKey(key); err != nil {
		return 0, err
	}
	b, err := r.zScoreBucket(name)
	if err != nil {
		return 0, err
	}
	if b == nil {
		return 0, ErrBucketNotFound
	}
	v := b.Get(key)
	if v == nil {
		return 0, ErrKeyNotFound
	}
	return DecodeUint64(v)
}

// HGetBatch reads multiple Hash keys in one transaction. Returned key/value
// pairs are copied and remain valid after the transaction closes.
func (r *ReadEngine) HGetBatch(name string, keys [][]byte) *Reply {
	if r == nil || r.tx == nil {
		return errorReply(ErrInvalidTx)
	}
	if err := validName(name); err != nil {
		return errorReply(err)
	}
	b, err := r.hashBucket(name)
	if err != nil {
		return errorReply(err)
	}
	result := &Reply{State: replyNotFound, Data: make([]BS, 0, 2*len(keys))}
	for _, key := range keys {
		if err := validKey(key); err != nil {
			result.Err = err
			result.State = replyError
			return result
		}
	}
	if b == nil {
		result.State = bucketNotFound
		return result
	}
	for _, key := range keys {
		if v := b.Get(key); v != nil {
			result.Data = append(result.Data, cloneBytes(key), cloneBytes(v))
		}
	}
	if len(result.Data) != 0 {
		result.State = replyOK
	}
	return result
}

// ZGetBatch reads multiple ZSet scores in one transaction.
func (r *ReadEngine) ZGetBatch(name string, keys [][]byte) *Reply {
	if r == nil || r.tx == nil {
		return errorReply(ErrInvalidTx)
	}
	if err := validName(name); err != nil {
		return errorReply(err)
	}
	b, err := r.zScoreBucket(name)
	if err != nil {
		return errorReply(err)
	}
	result := &Reply{State: replyNotFound, Data: make([]BS, 0, 2*len(keys))}
	for _, key := range keys {
		if err := validKey(key); err != nil {
			result.Err = err
			result.State = replyError
			return result
		}
	}
	if b == nil {
		result.State = bucketNotFound
		return result
	}
	for _, key := range keys {
		if v := b.Get(key); v != nil {
			result.Data = append(result.Data, cloneBytes(key), cloneBytes(v))
		}
	}
	if len(result.Data) != 0 {
		result.State = replyOK
	}
	return result
}

func (r *ReadEngine) HScan(name string, keyStart []byte, limit int) *Reply {
	return r.hscan(name, keyStart, limit, false)
}

func (r *ReadEngine) HRScan(name string, keyStart []byte, limit int) *Reply {
	return r.hscan(name, keyStart, limit, true)
}

func (r *ReadEngine) hscan(name string, keyStart []byte, limit int, reverse bool) *Reply {
	if r == nil || r.tx == nil {
		return errorReply(ErrInvalidTx)
	}
	if err := validLimit(limit); err != nil {
		return errorReply(err)
	}
	b, err := r.hashBucket(name)
	if err != nil {
		return errorReply(err)
	}
	result := &Reply{State: replyNotFound, Data: make([]BS, 0, 2*limit)}
	if b == nil {
		result.State = bucketNotFound
		return result
	}
	c := b.Cursor()
	var k, v []byte
	if reverse {
		k, v = seekReverse(c, keyStart, len(keyStart) > 0)
	} else {
		k, v = seekForward(c, keyStart, len(keyStart) > 0)
	}
	for n := 0; k != nil && n < limit; n++ {
		result.Data = append(result.Data, cloneBytes(k), cloneBytes(v))
		if reverse {
			k, v = c.Prev()
		} else {
			k, v = c.Next()
		}
	}
	if len(result.Data) != 0 {
		result.State = replyOK
	}
	return result
}

// ZScan uses the existing ordered-index cursor and ownership-safe copy path.
func (r *ReadEngine) ZScan(name string, keyStart, scoreStart []byte, limit int) *Reply {
	return r.zscan(name, keyStart, scoreStart, limit, false)
}

// ZRScan scans the ordered index in reverse.
func (r *ReadEngine) ZRScan(name string, keyStart, scoreStart []byte, limit int) *Reply {
	return r.zscan(name, keyStart, scoreStart, limit, true)
}

func (r *ReadEngine) zscan(name string, keyStart, scoreStart []byte, limit int, reverse bool) *Reply {
	if r == nil || r.tx == nil {
		return errorReply(ErrInvalidTx)
	}
	if err := validLimit(limit); err != nil {
		return errorReply(err)
	}
	if len(scoreStart) != 0 && len(scoreStart) != uint64EncodedLen {
		return errorReply(ErrInvalidScore)
	}
	b, err := r.zIndexBucket(name)
	if err != nil {
		return errorReply(err)
	}
	result := &Reply{State: replyNotFound, Data: make([]BS, 0, 2*limit)}
	if b == nil {
		result.State = bucketNotFound
		return result
	}
	c := b.Cursor()
	var k, v []byte
	if reverse {
		k, v = zseekReverse(c, keyStart, scoreStart)
	} else {
		k, v = zseekForward(c, keyStart, scoreStart)
	}
	arena := make([]byte, 0)
	for n := 0; k != nil && n < limit; n++ {
		if len(k) < uint64EncodedLen {
			result.Err = ErrInvalidScore
			result.State = replyError
			return result
		}
		start := len(arena)
		arena = append(arena, k...)
		entry := arena[start:]
		member := entry[uint64EncodedLen:len(entry):len(entry)]
		score := entry[:uint64EncodedLen:uint64EncodedLen]
		result.Data = append(result.Data, member, score)
		if reverse {
			k, v = c.Prev()
		} else {
			k, v = c.Next()
		}
	}
	_ = v
	if len(result.Data) != 0 {
		result.State = replyOK
	}
	return result
}

// ZScanEach consumes the ordered index without cloning member bytes. The
// callback must finish before the transaction callback returns.
func (r *ReadEngine) ZScanEach(name string, keyStart, scoreStart []byte, limit int, reverse bool, fn func(member []byte, score uint64) error) error {
	if r == nil || r.tx == nil {
		return ErrInvalidTx
	}
	if fn == nil {
		return ErrNilTransactionFunc
	}
	if err := validLimit(limit); err != nil {
		return err
	}
	if len(scoreStart) != 0 && len(scoreStart) != uint64EncodedLen {
		return ErrInvalidScore
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
		score := binary.BigEndian.Uint64(k[:uint64EncodedLen])
		if err := fn(k[uint64EncodedLen:], score); err != nil {
			return err
		}
		if reverse {
			k, _ = c.Prev()
		} else {
			k, _ = c.Next()
		}
	}
	return nil
}
