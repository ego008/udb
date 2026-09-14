// Package udb is a small Bolt wrapper that provides hash and sorted-set
// primitives while keeping the original transaction-oriented API.
package udb

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	bolt "go.etcd.io/bbolt"
)

const (
	replyOK        = "ok"
	replyNotFound  = "not_found"
	replyError     = "error"
	bucketNotFound = "bucket_not_found"
	keyNotFound    = "key_not_found"
	kvpLen         = "kvs len must is an even number"

	scoreMin uint64 = 0
	scoreMax uint64 = ^uint64(0)

	uint64EncodedLen = 8
)

var (
	hashPrefix     = []byte{30}
	zetKeyPrefix   = []byte{31}
	zetScorePrefix = []byte{29}

	ErrKeyNotFound     = errors.New(keyNotFound)
	ErrKeyValuePairLen = errors.New(kvpLen)
	ErrBucketNotFound  = errors.New(bucketNotFound)
	ErrOverFlowNumber  = errors.New("overflow number")
	ErrInvalidTx       = errors.New("nil bolt transaction")
	ErrInvalidName     = errors.New("empty bucket name")
	ErrInvalidKey      = errors.New("empty key")
	ErrInvalidScore    = errors.New("invalid uint64 score encoding")
	ErrInvalidValue    = errors.New("invalid uint64 value encoding")
	ErrInvalidLimit    = errors.New("limit must be greater than zero")
)

type (
	// BS is a byte string used by Reply and conversion helpers.
	BS []byte

	// Reply holds returned data and optional error details.
	// Data is copied out of Bolt's mmap-backed pages, so it remains valid after
	// the read transaction closes.
	Reply struct {
		State string
		Data  []BS
		Err   error
	}

	// Entry is one key-value pair.
	Entry struct {
		Key, Value BS
	}
)

func validTx(tx *Tx) error {
	if tx == nil || tx.inner == nil {
		return ErrInvalidTx
	}
	return nil
}

func validName(name string) error {
	if name == "" {
		return ErrInvalidName
	}
	return nil
}

func validKey(key []byte) error {
	if len(key) == 0 {
		return ErrInvalidKey
	}
	return nil
}

func validLimit(limit int) error {
	if limit <= 0 {
		return ErrInvalidLimit
	}
	return nil
}

func bucketName(prefix []byte, name string) []byte {
	return Bconcat(prefix, []byte(name))
}

func cloneBytes(v []byte) BS {
	if v == nil {
		return nil
	}
	return append(BS(nil), v...)
}

func newReply() *Reply {
	return &Reply{State: replyError, Data: make([]BS, 0, 2)}
}

func (db *DB) Hset(tx *Tx, name string, key, val []byte) error {
	if err := validTx(tx); err != nil {
		return err
	}
	if err := validName(name); err != nil {
		return err
	}
	if err := validKey(key); err != nil {
		return err
	}
	b, err := getOrCreateBucket(tx, bucketName(hashPrefix, name))
	if err != nil {
		return err
	}
	return b.Put(key, val)
}

func (db *DB) Hmset(tx *Tx, name string, kvs ...[]byte) error {
	if err := validTx(tx); err != nil {
		return err
	}
	if err := validName(name); err != nil {
		return err
	}
	if len(kvs) == 0 || len(kvs)%2 != 0 {
		return ErrKeyValuePairLen
	}
	for i := 0; i < len(kvs); i += 2 {
		if err := validKey(kvs[i]); err != nil {
			return fmt.Errorf("hash item %d: %w", i/2, err)
		}
	}
	b, err := getOrCreateBucket(tx, bucketName(hashPrefix, name))
	if err != nil {
		return err
	}
	for i := 0; i < len(kvs); i += 2 {
		if err := b.Put(kvs[i], kvs[i+1]); err != nil {
			return err
		}
	}
	return nil
}

func (db *DB) Hincr(tx *Tx, name string, key []byte, step int64) (uint64, error) {
	if err := validTx(tx); err != nil {
		return 0, err
	}
	if err := validName(name); err != nil {
		return 0, err
	}
	if err := validKey(key); err != nil {
		return 0, err
	}
	b, err := getOrCreateBucket(tx, bucketName(hashPrefix, name))
	if err != nil {
		return 0, err
	}
	old := b.Get(key)
	var current uint64
	if old != nil {
		current, err = DecodeUint64(old)
		if err != nil {
			return 0, fmt.Errorf("hash value %q: %w", key, ErrInvalidValue)
		}
	}
	current, err = addInt64(current, step)
	if err != nil {
		return 0, err
	}
	if err := b.Put(key, I2b(current)); err != nil {
		return 0, err
	}
	return current, nil
}

func (db *DB) Hdel(tx *Tx, name string, key []byte) error {
	if err := validTx(tx); err != nil {
		return err
	}
	if err := validName(name); err != nil {
		return err
	}
	if err := validKey(key); err != nil {
		return err
	}
	b := tx.bucket(bucketName(hashPrefix, name))
	if b == nil {
		return nil
	}
	return b.Delete(key)
}

func (db *DB) Hmdel(tx *Tx, name string, keys [][]byte) error {
	if err := validTx(tx); err != nil {
		return err
	}
	if err := validName(name); err != nil {
		return err
	}
	b := tx.bucket(bucketName(hashPrefix, name))
	if b == nil {
		return nil
	}
	for _, key := range keys {
		if err := validKey(key); err != nil {
			return err
		}
		if err := b.Delete(key); err != nil {
			return err
		}
	}
	return nil
}

func (db *DB) HdelBucket(tx *Tx, name string) error {
	if err := validTx(tx); err != nil {
		return err
	}
	if err := validName(name); err != nil {
		return err
	}
	return tx.deleteBucket(bucketName(hashPrefix, name))
}

func (db *DB) Hget(tx *Tx, name string, key []byte) *Reply {
	if err := validTx(tx); err != nil {
		return errorReply(err)
	}
	if err := validName(name); err != nil {
		return errorReply(err)
	}
	if err := validKey(key); err != nil {
		return errorReply(err)
	}
	r := newReply()
	b := tx.bucket(bucketName(hashPrefix, name))
	if b == nil {
		r.State = bucketNotFound
		return r
	}
	v := b.Get(key)
	if v == nil {
		r.State = keyNotFound
		return r
	}
	r.State = replyOK
	r.Data = append(r.Data, cloneBytes(v))
	return r
}

func (db *DB) HgetInt(tx *Tx, name string, key []byte) (uint64, error) {
	if err := validTx(tx); err != nil {
		return 0, err
	}
	if err := validName(name); err != nil {
		return 0, err
	}
	if err := validKey(key); err != nil {
		return 0, err
	}
	b := tx.bucket(bucketName(hashPrefix, name))
	if b == nil {
		return 0, ErrBucketNotFound
	}
	v := b.Get(key)
	if v == nil {
		return 0, ErrKeyNotFound
	}
	return DecodeUint64(v)
}

func (db *DB) Hsequence(tx *Tx, name string) (uint64, error) {
	if err := validTx(tx); err != nil {
		return 0, err
	}
	if err := validName(name); err != nil {
		return 0, err
	}
	b := tx.bucket(bucketName(hashPrefix, name))
	if b == nil {
		return 0, ErrBucketNotFound
	}
	return b.Sequence(), nil
}

func (db *DB) HsetSequence(tx *Tx, name string, v uint64) error {
	if err := validTx(tx); err != nil {
		return err
	}
	if err := validName(name); err != nil {
		return err
	}
	b, err := getOrCreateBucket(tx, bucketName(hashPrefix, name))
	if err != nil {
		return err
	}
	return b.SetSequence(v)
}

func (db *DB) HnextSequence(tx *Tx, name string) (uint64, error) {
	if err := validTx(tx); err != nil {
		return 0, err
	}
	if err := validName(name); err != nil {
		return 0, err
	}
	b, err := getOrCreateBucket(tx, bucketName(hashPrefix, name))
	if err != nil {
		return 0, err
	}
	return b.NextSequence()
}

func (db *DB) Hmget(tx *Tx, name string, keys [][]byte) *Reply {
	if err := validTx(tx); err != nil {
		return errorReply(err)
	}
	if err := validName(name); err != nil {
		return errorReply(err)
	}
	r := newReply()
	b := tx.bucket(bucketName(hashPrefix, name))
	if b == nil {
		r.State = bucketNotFound
		return r
	}
	for _, key := range keys {
		if err := validKey(key); err != nil {
			r.Err = err
			return r
		}
		if v := b.Get(key); v != nil {
			r.Data = append(r.Data, cloneBytes(key), cloneBytes(v))
		}
	}
	if len(r.Data) > 0 {
		r.State = replyOK
	} else {
		r.State = replyNotFound
	}
	return r
}

func (db *DB) Hscan(tx *Tx, name string, keyStart []byte, limit int) *Reply {
	return db.hscan(tx, name, keyStart, limit, false)
}

func (db *DB) Hrscan(tx *Tx, name string, keyStart []byte, limit int) *Reply {
	return db.hscan(tx, name, keyStart, limit, true)
}

func (db *DB) hscan(tx *Tx, name string, keyStart []byte, limit int, reverse bool) *Reply {
	if err := validTx(tx); err != nil {
		return errorReply(err)
	}
	if err := validName(name); err != nil {
		return errorReply(err)
	}
	if err := validLimit(limit); err != nil {
		return errorReply(err)
	}
	r := newReply()
	b := tx.bucket(bucketName(hashPrefix, name))
	if b == nil {
		r.State = bucketNotFound
		return r
	}
	c := b.Cursor()
	var k, v []byte
	if reverse {
		k, v = seekReverse(c, keyStart, len(keyStart) > 0)
	} else {
		k, v = seekForward(c, keyStart, len(keyStart) > 0)
	}
	n := 0
	for k != nil && n < limit {
		r.Data = append(r.Data, cloneBytes(k), cloneBytes(v))
		n++
		if reverse {
			k, v = c.Prev()
		} else {
			k, v = c.Next()
		}
	}
	if len(r.Data) > 0 {
		r.State = replyOK
	} else {
		r.State = replyNotFound
	}
	return r
}

func (db *DB) Zset(tx *Tx, name string, key []byte, val uint64) error {
	if err := validTx(tx); err != nil {
		return err
	}
	if err := validName(name); err != nil {
		return err
	}
	if err := validKey(key); err != nil {
		return err
	}
	return db.zset(tx, name, key, I2b(val))
}

func (db *DB) zset(tx *Tx, name string, key, score []byte) error {
	if len(score) != uint64EncodedLen {
		return ErrInvalidScore
	}
	keyBucket, scoreBucket, err := db.zsetBuckets(tx, name)
	if err != nil {
		return err
	}
	oldScore := scoreBucket.Get(key)
	newIndexKey := Bconcat(score, key)
	if bytes.Equal(oldScore, score) {
		// The score is unchanged. Do not delete the same index key below.
		// If the secondary index is missing, rebuild it in this transaction.
		if !bucketHasKey(keyBucket, newIndexKey) {
			return keyBucket.Put(newIndexKey, nil)
		}
		return nil
	}
	if err := keyBucket.Put(newIndexKey, nil); err != nil {
		return err
	}
	if err := scoreBucket.Put(key, score); err != nil {
		return err
	}
	if oldScore != nil {
		if err := keyBucket.Delete(Bconcat(oldScore, key)); err != nil {
			return err
		}
	}
	return nil
}

func (db *DB) Zmset(tx *Tx, name string, kvs ...[]byte) error {
	if err := validTx(tx); err != nil {
		return err
	}
	if err := validName(name); err != nil {
		return err
	}
	if len(kvs) == 0 || len(kvs)%2 != 0 {
		return ErrKeyValuePairLen
	}
	// Validate everything before mutating the transaction.
	for i := 0; i < len(kvs); i += 2 {
		if err := validKey(kvs[i]); err != nil {
			return fmt.Errorf("zset item %d: %w", i/2, err)
		}
		if len(kvs[i+1]) != uint64EncodedLen {
			return fmt.Errorf("zset item %d: %w", i/2, ErrInvalidScore)
		}
	}
	for i := 0; i < len(kvs); i += 2 {
		if err := db.zset(tx, name, kvs[i], kvs[i+1]); err != nil {
			return err
		}
	}
	return nil
}

func (db *DB) Zincr(tx *Tx, name string, key []byte, step int64) (uint64, error) {
	if err := validTx(tx); err != nil {
		return 0, err
	}
	if err := validName(name); err != nil {
		return 0, err
	}
	if err := validKey(key); err != nil {
		return 0, err
	}
	_, scoreBucket, err := db.zsetBuckets(tx, name)
	if err != nil {
		return 0, err
	}
	oldScoreB := scoreBucket.Get(key)
	var current uint64
	if oldScoreB != nil {
		current, err = DecodeUint64(oldScoreB)
		if err != nil {
			return 0, ErrInvalidScore
		}
	}
	current, err = addInt64(current, step)
	if err != nil {
		return 0, err
	}
	newScoreB := I2b(current)

	// Keep ZSet's two-index mutation in one canonical implementation. In
	// particular, Zincr(step=0) must not Put the current index and then delete
	// that exact same index. zset() handles unchanged scores idempotently and
	// can also repair a missing secondary index in the same transaction.
	if err := db.zset(tx, name, key, newScoreB); err != nil {
		return 0, err
	}
	return current, nil
}

func (db *DB) Zdel(tx *Tx, name string, key []byte) error {
	if err := validTx(tx); err != nil {
		return err
	}
	if err := validName(name); err != nil {
		return err
	}
	if err := validKey(key); err != nil {
		return err
	}
	keyBucket := tx.bucket(bucketName(zetKeyPrefix, name))
	scoreBucket := tx.bucket(bucketName(zetScorePrefix, name))
	if scoreBucket == nil {
		return nil
	}
	oldScore := scoreBucket.Get(key)
	if oldScore == nil {
		return nil
	}
	if keyBucket != nil {
		if err := keyBucket.Delete(Bconcat(oldScore, key)); err != nil {
			return err
		}
	}
	return scoreBucket.Delete(key)
}

func (db *DB) Zmdel(tx *Tx, name string, keys [][]byte) error {
	if err := validTx(tx); err != nil {
		return err
	}
	if err := validName(name); err != nil {
		return err
	}
	for _, key := range keys {
		if err := validKey(key); err != nil {
			return err
		}
	}
	for _, key := range keys {
		if err := db.Zdel(tx, name, key); err != nil {
			return err
		}
	}
	return nil
}

func (db *DB) ZdelBucket(tx *Tx, name string) error {
	if err := validTx(tx); err != nil {
		return err
	}
	if err := validName(name); err != nil {
		return err
	}
	if err := tx.deleteBucket(bucketName(zetKeyPrefix, name)); err != nil && !isBucketNotFound(err) {
		return err
	}
	if err := tx.deleteBucket(bucketName(zetScorePrefix, name)); err != nil && !isBucketNotFound(err) {
		return err
	}
	return nil
}

func (db *DB) Zget(tx *Tx, name string, key []byte) *Reply {
	if err := validTx(tx); err != nil {
		return errorReply(err)
	}
	if err := validName(name); err != nil {
		return errorReply(err)
	}
	if err := validKey(key); err != nil {
		return errorReply(err)
	}
	r := newReply()
	b := tx.bucket(bucketName(zetScorePrefix, name))
	if b == nil {
		r.State = bucketNotFound
		return r
	}
	v := b.Get(key)
	if v == nil {
		r.State = keyNotFound
		return r
	}
	r.State = replyOK
	r.Data = append(r.Data, cloneBytes(v))
	return r
}

// Zscore is the typed equivalent of Zget.
func (db *DB) Zscore(tx *Tx, name string, key []byte) (uint64, error) {
	if err := validTx(tx); err != nil {
		return 0, err
	}
	if err := validName(name); err != nil {
		return 0, err
	}
	if err := validKey(key); err != nil {
		return 0, err
	}
	b := tx.bucket(bucketName(zetScorePrefix, name))
	if b == nil {
		return 0, ErrBucketNotFound
	}
	v := b.Get(key)
	if v == nil {
		return 0, ErrKeyNotFound
	}
	return DecodeUint64(v)
}

func (db *DB) Zsequence(tx *Tx, name string) (uint64, error) {
	if err := validTx(tx); err != nil {
		return 0, err
	}
	if err := validName(name); err != nil {
		return 0, err
	}
	b := tx.bucket(bucketName(zetScorePrefix, name))
	if b == nil {
		return 0, ErrBucketNotFound
	}
	return b.Sequence(), nil
}

func (db *DB) ZsetSequence(tx *Tx, name string, v uint64) error {
	if err := validTx(tx); err != nil {
		return err
	}
	if err := validName(name); err != nil {
		return err
	}
	b, err := getOrCreateBucket(tx, bucketName(zetScorePrefix, name))
	if err != nil {
		return err
	}
	return b.SetSequence(v)
}

func (db *DB) ZnextSequence(tx *Tx, name string) (uint64, error) {
	if err := validTx(tx); err != nil {
		return 0, err
	}
	if err := validName(name); err != nil {
		return 0, err
	}
	b, err := getOrCreateBucket(tx, bucketName(zetScorePrefix, name))
	if err != nil {
		return 0, err
	}
	return b.NextSequence()
}

func (db *DB) Zmget(tx *Tx, name string, keys [][]byte) *Reply {
	if err := validTx(tx); err != nil {
		return errorReply(err)
	}
	if err := validName(name); err != nil {
		return errorReply(err)
	}
	r := newReply()
	b := tx.bucket(bucketName(zetScorePrefix, name))
	if b == nil {
		r.State = bucketNotFound
		return r
	}
	for _, key := range keys {
		if err := validKey(key); err != nil {
			r.Err = err
			return r
		}
		if v := b.Get(key); v != nil {
			r.Data = append(r.Data, cloneBytes(key), cloneBytes(v))
		}
	}
	if len(r.Data) > 0 {
		r.State = replyOK
	} else {
		r.State = replyNotFound
	}
	return r
}

// Zscan returns entries strictly greater than (scoreStart, keyStart), ordered
// by score ascending and key ascending for equal scores.
func (db *DB) Zscan(tx *Tx, name string, keyStart, scoreStart []byte, limit int) *Reply {
	return db.zscan(tx, name, keyStart, scoreStart, limit, false)
}

// Zrscan returns entries strictly less than (scoreStart, keyStart), ordered
// by score descending and key descending for equal scores.
func (db *DB) Zrscan(tx *Tx, name string, keyStart, scoreStart []byte, limit int) *Reply {
	return db.zscan(tx, name, keyStart, scoreStart, limit, true)
}

func (db *DB) zscan(tx *Tx, name string, keyStart, scoreStart []byte, limit int, reverse bool) *Reply {
	if err := validTx(tx); err != nil {
		return errorReply(err)
	}
	if err := validName(name); err != nil {
		return errorReply(err)
	}
	if err := validLimit(limit); err != nil {
		return errorReply(err)
	}
	if len(scoreStart) != 0 && len(scoreStart) != uint64EncodedLen {
		return errorReply(ErrInvalidScore)
	}
	r := newReply()
	b := tx.bucket(bucketName(zetKeyPrefix, name))
	if b == nil {
		r.State = bucketNotFound
		return r
	}
	c := b.Cursor()
	var k, v []byte
	if reverse {
		k, v = zseekReverse(c, keyStart, scoreStart)
	} else {
		k, v = zseekForward(c, keyStart, scoreStart)
	}
	n := 0
	for k != nil && n < limit {
		if len(k) < uint64EncodedLen {
			r.Err = ErrInvalidScore
			r.State = replyError
			return r
		}
		r.Data = append(r.Data, cloneBytes(k[8:]), cloneBytes(k[:8]))
		n++
		if reverse {
			k, v = c.Prev()
		} else {
			k, v = c.Next()
		}
	}
	_ = v
	if len(r.Data) > 0 {
		r.State = replyOK
	} else {
		r.State = replyNotFound
	}
	return r
}

func zseekForward(c *bolt.Cursor, keyStart, scoreStart []byte) (k, v []byte) {
	if len(scoreStart) == 0 {
		scoreStart = I2b(scoreMin)
	}
	boundary := Bconcat(scoreStart, keyStart)
	k, v = c.Seek(boundary)
	if k != nil && bytes.Compare(k, boundary) <= 0 {
		k, v = c.Next()
	}
	return
}

func zseekReverse(c *bolt.Cursor, keyStart, scoreStart []byte) (k, v []byte) {
	if len(scoreStart) == 0 {
		scoreStart = I2b(scoreMax)
	}
	if len(keyStart) == 0 && bytes.Equal(scoreStart, I2b(scoreMax)) {
		return c.Last()
	}
	boundary := Bconcat(scoreStart, keyStart)
	k, v = c.Seek(boundary)
	if k == nil {
		return c.Last()
	}
	if bytes.Compare(k, boundary) >= 0 {
		k, v = c.Prev()
	}
	return k, v
}

func seekForward(c *bolt.Cursor, start []byte, strict bool) (k, v []byte) {
	if !strict {
		return c.First()
	}
	k, v = c.Seek(start)
	if k != nil && bytes.Compare(k, start) <= 0 {
		return c.Next()
	}
	return k, v
}

func seekReverse(c *bolt.Cursor, start []byte, strict bool) (k, v []byte) {
	if !strict {
		return c.Last()
	}
	k, v = c.Seek(start)
	if k == nil {
		return c.Last()
	}
	if bytes.Compare(k, start) >= 0 {
		return c.Prev()
	}
	return k, v
}

func (r *Reply) OK() bool { return r != nil && r.State == replyOK && r.Err == nil }

func (r *Reply) NotFound() bool {
	return r != nil && (r.State == replyNotFound || r.State == keyNotFound || r.State == bucketNotFound)
}

func (r *Reply) ErrOrNil() error {
	if r == nil {
		return errors.New("nil reply")
	}
	return r.Err
}

func (r *Reply) Bytes() []byte {
	if r == nil || len(r.Data) == 0 {
		return nil
	}
	return r.Data[0]
}

func (r *Reply) String() string {
	if r == nil || len(r.Data) == 0 {
		return ""
	}
	return B2s(r.Data[0])
}

func (r *Reply) Int() int { return int(r.Uint64()) }

func (r *Reply) Int64() int64 { return int64(r.Uint64()) }

func (r *Reply) Uint() uint { return uint(r.Uint64()) }

func (r *Reply) Uint64() uint64 {
	if r == nil || len(r.Data) < 1 {
		return 0
	}
	v, err := DecodeUint64(r.Data[0])
	if err != nil {
		return 0
	}
	return v
}

func (r *Reply) List() []Entry {
	if r == nil || len(r.Data) < 2 {
		return []Entry{}
	}
	list := make([]Entry, 0, len(r.Data)/2)
	for i := 0; i+1 < len(r.Data); i += 2 {
		list = append(list, Entry{r.Data[i], r.Data[i+1]})
	}
	return list
}

func (r *Reply) Dict() map[string][]byte {
	dict := make(map[string][]byte)
	if r == nil {
		return dict
	}
	for i := 0; i+1 < len(r.Data); i += 2 {
		dict[B2s(r.Data[i])] = r.Data[i+1]
	}
	return dict
}

func (r *Reply) KvLen() int {
	if r == nil {
		return 0
	}
	return len(r.Data) / 2
}

func (r *Reply) KvEach(fn func(key, value BS)) int {
	if r == nil || fn == nil {
		return 0
	}
	for i := 0; i+1 < len(r.Data); i += 2 {
		fn(r.Data[i], r.Data[i+1])
	}
	return r.KvLen()
}

func (r *Reply) JSON(v interface{}) error {
	if r == nil || len(r.Data) == 0 {
		return errors.New("empty reply")
	}
	if v == nil {
		return errors.New("nil json destination")
	}
	return json.Unmarshal(r.Data[0], v)
}

func (b BS) Bytes() []byte  { return []byte(b) }
func (b BS) String() string { return B2s(b) }
func (b BS) Int() int       { return int(b.Uint64()) }
func (b BS) Int64() int64   { return int64(b.Uint64()) }
func (b BS) Uint() uint     { return uint(b.Uint64()) }
func (b BS) Uint64() uint64 {
	v, err := DecodeUint64(b)
	if err != nil {
		return 0
	}
	return v
}
func (b BS) JSON(v interface{}) error {
	if v == nil {
		return errors.New("nil json destination")
	}
	return json.Unmarshal(b, v)
}

func Bconcat(slices ...[]byte) []byte {
	totalLen := 0
	for _, s := range slices {
		totalLen += len(s)
	}
	tmp := make([]byte, totalLen)
	off := 0
	for _, s := range slices {
		off += copy(tmp[off:], s)
	}
	return tmp
}

func DS2b(v string) []byte {
	i, err := strconv.ParseUint(v, 10, 64)
	if err != nil {
		return nil
	}
	return I2b(i)
}

func DS2i(v string) uint64 {
	i, err := strconv.ParseUint(v, 10, 64)
	if err != nil {
		return 0
	}
	return i
}

func I2b(v uint64) []byte {
	b := make([]byte, uint64EncodedLen)
	binary.BigEndian.PutUint64(b, v)
	return b
}

// DecodeUint64 validates the byte representation before decoding it.
func DecodeUint64(v []byte) (uint64, error) {
	if len(v) != uint64EncodedLen {
		return 0, ErrInvalidValue
	}
	return binary.BigEndian.Uint64(v), nil
}

func B2i(v []byte) uint64 {
	i, err := DecodeUint64(v)
	if err != nil {
		return 0
	}
	return i
}

func B2ds(v []byte) string {
	return strconv.FormatUint(B2i(v), 10)
}

// B2s performs a safe byte-to-string conversion.
func B2s(b []byte) string { return string(b) }

// S2b performs a safe string-to-byte conversion.
func S2b(s string) []byte { return []byte(s) }

func addInt64(current uint64, step int64) (uint64, error) {
	if step >= 0 {
		delta := uint64(step)
		if current > scoreMax-delta {
			return 0, ErrOverFlowNumber
		}
		return current + delta, nil
	}
	// Computes abs(step) without overflowing when step == MinInt64.
	delta := uint64(-(step + 1)) + 1
	if delta > current {
		return 0, ErrOverFlowNumber
	}
	return current - delta, nil
}

func bucketHasKey(b *bolt.Bucket, key []byte) bool {
	if b == nil {
		return false
	}
	c := b.Cursor()
	k, _ := c.Seek(key)
	return bytes.Equal(k, key)
}

func getOrCreateBucket(tx *Tx, name []byte) (*bolt.Bucket, error) {
	b := tx.bucket(name)
	if b != nil {
		return b, nil
	}
	return tx.createBucket(name)
}

func (db *DB) zsetBuckets(tx *Tx, name string) (*bolt.Bucket, *bolt.Bucket, error) {
	if err := validTx(tx); err != nil {
		return nil, nil, err
	}
	if err := validName(name); err != nil {
		return nil, nil, err
	}
	keyBucket, err := getOrCreateBucket(tx, bucketName(zetKeyPrefix, name))
	if err != nil {
		return nil, nil, err
	}
	scoreBucket, err := getOrCreateBucket(tx, bucketName(zetScorePrefix, name))
	if err != nil {
		return nil, nil, err
	}
	return keyBucket, scoreBucket, nil
}

func errorReply(err error) *Reply {
	return &Reply{State: replyError, Data: []BS{}, Err: err}
}

func isBucketNotFound(err error) bool {
	return errors.Is(err, bolt.ErrBucketNotFound)
}
