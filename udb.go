// Package udb is a Bolt wrapper that allows easy store hash, zset data.
package udb

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"

	bolt "go.etcd.io/bbolt"
)

const (
	replyOK                 = "ok"
	replyNotFound           = "not_found"
	replyError              = "error"
	bucketNotFound          = "bucket_not_found"
	keyNotFound             = "key_not_found"
	kvpLen                  = "kvs len must is an even number"
	scoreMin         uint64 = 0
	scoreMax         uint64 = ^uint64(0)
	uint64EncodedLen        = 8
)

var (
	hashPrefix     = []byte{30}
	zetKeyPrefix   = []byte{31}
	zetScorePrefix = []byte{29}

	ErrKeyNotFound     = errors.New(keyNotFound)
	ErrKeyValuePairLen = errors.New(kvpLen)
	ErrBucketNotFound  = errors.New(bucketNotFound)
	ErrOverFlowNumber  = errors.New("overflow number")
)

type (
	BS []byte
	// DB embeds a bolt.DB.
	DB struct {
		db *bolt.DB
		wg sync.WaitGroup // 等待组，用于优雅关闭
	}

	// Reply a holder for an Entry list of a hashmap.
	Reply struct {
		State string
		Data  []BS
	}

	// Entry a key-value pair.
	Entry struct {
		Key, Value BS
	}
)

func Open(path string) (*DB, error) {
	return OpenWithMode(path, 0o600)
}

// OpenWithMode opens a database using the supplied file mode.
func OpenWithMode(path string, mode os.FileMode) (*DB, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("empty database path")
	}
	database, err := bolt.Open(path, mode, &bolt.Options{Timeout: time.Second})
	if err != nil {
		return nil, err
	}
	return &DB{db: database}, nil
}

// View executes a read-only transaction.
func (db *DB) View(fn func(*bolt.Tx) error) error {
	if db == nil || db.db == nil {
		return errors.New("nil database")
	}
	if fn == nil {
		return errors.New("nil transaction callback")
	}
	db.wg.Add(1)
	defer db.wg.Done()
	return db.db.View(fn)
}

// Update executes a read-write transaction.
func (db *DB) Update(fn func(*bolt.Tx) error) error {
	if db == nil || db.db == nil {
		return errors.New("nil database")
	}
	if fn == nil {
		return errors.New("nil transaction callback")
	}
	db.wg.Add(1)
	defer db.wg.Done()
	return db.db.Update(fn)
}

// Close closes the embedded bolt.DB.
func (db *DB) Close() error {
	db.wg.Wait()
	return db.db.Close()
}

// Hset set the byte value in argument as value of the key of a hashmap.
func (db *DB) Hset(tx *bolt.Tx, name string, key, val []byte) error {
	bucketName := Bconcat(hashPrefix, S2b(name))
	b, err := tx.CreateBucketIfNotExists(bucketName)
	if err != nil {
		return err
	}
	return b.Put(key, val)
}

// Hmset set multiple key-value pairs of a hashmap in one method call.
func (db *DB) Hmset(tx *bolt.Tx, name string, kvs ...[]byte) error {
	if len(kvs) == 0 || len(kvs)%2 != 0 {
		return ErrKeyValuePairLen
	}
	bucketName := Bconcat(hashPrefix, S2b(name))

	b, err := tx.CreateBucketIfNotExists(bucketName)
	if err != nil {
		return err
	}

	for i := 0; i < len(kvs)-1; i += 2 {
		if err = b.Put(kvs[i], kvs[i+1]); err != nil {
			return err
		}
	}
	return nil
}

// Hincr increment the number stored at key in a hashmap by step.
func (db *DB) Hincr(tx *bolt.Tx, name string, key []byte, step int64) (uint64, error) {
	bucketName := Bconcat(hashPrefix, S2b(name))
	b, err := tx.CreateBucketIfNotExists(bucketName)
	if err != nil {
		return 0, err
	}

	var oldNum uint64
	if v := b.Get(key); v != nil {
		oldNum = B2i(v)
	}

	if step > 0 {
		if (scoreMax - uint64(step)) < oldNum {
			return 0, ErrOverFlowNumber
		}
		oldNum += uint64(step)
	} else {
		if uint64(-step) > oldNum {
			return 0, ErrOverFlowNumber
		}
		oldNum -= uint64(-step)
	}

	err = b.Put(key, I2b(oldNum))
	if err != nil {
		return 0, err
	}
	return oldNum, nil
}

// Hdel delete specified key of a hashmap.
func (db *DB) Hdel(tx *bolt.Tx, name string, key []byte) error {
	bucketName := Bconcat(hashPrefix, S2b(name))
	b := tx.Bucket(bucketName)
	if b != nil {
		return b.Delete(key)
	}
	return nil
}

// Hmdel delete specified multiple keys of a hashmap.
func (db *DB) Hmdel(tx *bolt.Tx, name string, keys [][]byte) error {
	bucketName := Bconcat(hashPrefix, S2b(name))
	b := tx.Bucket(bucketName)
	if b != nil {
		for _, key := range keys {
			_ = b.Delete(key)
		}
	}
	return nil
}

// HdelBucket delete all keys in a hashmap.
func (db *DB) HdelBucket(tx *bolt.Tx, name string) error {
	bucketName := Bconcat(hashPrefix, S2b(name))
	return tx.DeleteBucket(bucketName)
}

// Hget get the value related to the specified key of a hashmap.
func (db *DB) Hget(tx *bolt.Tx, name string, key []byte) *Reply {
	r := &Reply{State: replyError, Data: []BS{}}
	bucketName := Bconcat(hashPrefix, S2b(name))

	b := tx.Bucket(bucketName)
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
	r.Data = append(r.Data, v)
	return r
}

// HgetInt get the value related to the specified key of a hashmap.
func (db *DB) HgetInt(tx *bolt.Tx, name string, key []byte) (uint64, error) {
	bucketName := Bconcat(hashPrefix, S2b(name))
	b := tx.Bucket(bucketName)
	if b == nil {
		return 0, ErrBucketNotFound
	}
	v := b.Get(key)
	if v == nil {
		return 0, ErrKeyNotFound
	}

	return B2i(v), nil
}

// Hsequence returns the current integer for the bucket without incrementing it.
func (db *DB) Hsequence(tx *bolt.Tx, name string) (uint64, error) {
	bucketName := Bconcat(hashPrefix, S2b(name))

	b := tx.Bucket(bucketName)
	if b == nil {
		return 0, ErrBucketNotFound
	}

	return b.Sequence(), nil
}

// HsetSequence updates the sequence number for the bucket.
func (db *DB) HsetSequence(tx *bolt.Tx, name string, v uint64) error {
	bucketName := Bconcat(hashPrefix, S2b(name))
	b := tx.Bucket(bucketName)
	if b == nil {
		var err error
		b, err = tx.CreateBucket(bucketName)
		if err != nil {
			return err
		}
	}
	return b.SetSequence(v)

}

// HnextSequence updates the sequence number for the bucket.
func (db *DB) HnextSequence(tx *bolt.Tx, name string) (uint64, error) {
	bucketName := Bconcat(hashPrefix, S2b(name))
	var err error
	var sequence uint64
	b := tx.Bucket(bucketName)
	if b == nil {
		b, err = tx.CreateBucket(bucketName)
		if err != nil {
			return 0, err
		}
	}
	sequence, err = b.NextSequence()
	if err != nil {
		return 0, err
	}

	return sequence, nil
}

// Hmget get the values related to the specified multiple keys of a hashmap.
func (db *DB) Hmget(tx *bolt.Tx, name string, keys [][]byte) *Reply {
	r := &Reply{
		State: replyError,
		Data:  []BS{},
	}
	bucketName := Bconcat(hashPrefix, S2b(name))
	b := tx.Bucket(bucketName)
	if b == nil {
		r.State = bucketNotFound
		return r
	}
	for _, key := range keys {
		v := b.Get(key)
		if v != nil {
			r.Data = append(r.Data, key, v)
		}
	}
	if len(r.Data) > 0 {
		r.State = replyOK
	}

	return r
}

// Hscan list key-value pairs of a hashmap with keys in range (key_start, key_end].
func (db *DB) Hscan(tx *bolt.Tx, name string, keyStart []byte, limit int) *Reply {
	r := &Reply{
		State: replyError,
		Data:  []BS{},
	}
	bucketName := Bconcat(hashPrefix, S2b(name))
	b := tx.Bucket(bucketName)
	if b == nil {
		r.State = bucketNotFound
		return r
	}
	c := b.Cursor()
	n := 0
	for k, v := c.Seek(keyStart); k != nil; k, v = c.Next() {
		if bytes.Compare(k, keyStart) == 1 {
			r.Data = append(r.Data, k, v)
			n++
			if n == limit {
				break
			}
		}
	}
	if n > 0 {
		r.State = replyOK
	}

	return r
}

// Hrscan list key-value pairs of a hashmap with keys in range (key_start, key_end], in reverse order.
func (db *DB) Hrscan(tx *bolt.Tx, name string, keyStart []byte, limit int) *Reply {
	r := &Reply{
		State: replyError,
		Data:  []BS{},
	}
	bucketName := Bconcat(hashPrefix, S2b(name))
	b := tx.Bucket(bucketName)
	if b == nil {
		r.State = bucketNotFound
		return r
	}

	c := b.Cursor()
	var startKey = []byte{255}
	var k0, v0 []byte
	if len(keyStart) > 0 {
		startKey = make([]byte, len(keyStart))
		copy(startKey, keyStart)
		k0, v0 = c.Seek(startKey)
	} else {
		k0, v0 = c.Last()
	}

	n := 0
	for k, v := k0, v0; k != nil; k, v = c.Prev() {
		if bytes.Compare(k, startKey) == -1 {
			r.Data = append(r.Data, k, v)
			n++
			if n == limit {
				break
			}
		}
	}
	if len(r.Data) > 0 {
		r.State = replyOK
	}

	return r
}

// Zset set the score of the key of a zset.
func (db *DB) Zset(tx *bolt.Tx, name string, key []byte, val uint64) error {
	score := I2b(val)
	keyBucket := Bconcat(zetKeyPrefix, S2b(name))
	scoreBucket := Bconcat(zetScorePrefix, S2b(name))
	newKey := Bconcat(score, key)
	var err error
	b1 := tx.Bucket(keyBucket)
	if b1 == nil {
		b1, err = tx.CreateBucket(keyBucket)
		if err != nil {
			return err
		}
	}

	b2 := tx.Bucket(scoreBucket)
	if b2 == nil {
		b2, err = tx.CreateBucket(scoreBucket)
		if err != nil {
			return err
		}
	}

	oldScore := b2.Get(key)
	if !bytes.Equal(oldScore, score) {
		err = b1.Put(newKey, []byte{})
		if err != nil {
			return err
		}

		err = b2.Put(key, score)
		if err != nil {
			return err
		}

		if oldScore != nil {
			oldKey := Bconcat(oldScore, key)
			err = b1.Delete(oldKey)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// Zmset et multiple key-score pairs of a zset in one method call.
func (db *DB) Zmset(tx *bolt.Tx, name string, kvs ...[]byte) error {
	if len(kvs) == 0 || len(kvs)%2 != 0 {
		return ErrKeyValuePairLen
	}

	keyBucket := Bconcat(zetKeyPrefix, S2b(name))
	scoreBucket := Bconcat(zetScorePrefix, S2b(name))

	var err error
	b1 := tx.Bucket(keyBucket)
	if b1 == nil {
		b1, err = tx.CreateBucket(keyBucket)
		if err != nil {
			return err
		}
	}

	b2 := tx.Bucket(scoreBucket)
	if b2 == nil {
		b2, err = tx.CreateBucket(scoreBucket)
		if err != nil {
			return err
		}
	}

	for i := 0; i < (len(kvs) - 1); i += 2 {
		key := kvs[i]
		score := kvs[i+1]
		newKey := Bconcat(score, key)

		oldScore := b2.Get(key)
		if !bytes.Equal(oldScore, score) {
			err = b1.Put(newKey, []byte(""))
			if err != nil {
				return err
			}

			err = b2.Put(key, score)
			if err != nil {
				return err
			}

			if oldScore != nil {
				oldKey := Bconcat(oldScore, key)
				err = b1.Delete(oldKey)
				if err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// Zincr increment the number stored at key in a zset by step.
func (db *DB) Zincr(tx *bolt.Tx, name string, key []byte, step int64) (uint64, error) {
	var score uint64

	keyBucket := Bconcat(zetKeyPrefix, S2b(name))
	scoreBucket := Bconcat(zetScorePrefix, S2b(name))

	var err error

	b1 := tx.Bucket(keyBucket)
	if b1 == nil {
		b1, err = tx.CreateBucket(keyBucket)
		if err != nil {
			return 0, err
		}
	}

	b2 := tx.Bucket(scoreBucket)
	if b2 == nil {
		b2, err = tx.CreateBucket(scoreBucket)
		if err != nil {
			return 0, err
		}
	}

	vOld := b2.Get(key)
	if vOld != nil {
		score = B2i(vOld)
	}
	if step > 0 {
		if (scoreMax - uint64(step)) < score {
			return 0, ErrOverFlowNumber
		}
		score += uint64(step)
	} else {

		if uint64(-step) > score {
			return 0, ErrOverFlowNumber
		}
		score -= uint64(-step)
	}
	newScoreB := I2b(score)
	newKey := Bconcat(newScoreB, key)

	err = b1.Put(newKey, []byte{})
	if err != nil {
		return 0, err
	}

	err = b2.Put(key, newScoreB)
	if err != nil {
		return 0, err
	}

	if vOld != nil {
		oldKey := Bconcat(vOld, key)
		err = b1.Delete(oldKey)
		if err != nil {
			return 0, err
		}
	}

	return score, err
}

// Zdel delete specified key of a zset.
func (db *DB) Zdel(tx *bolt.Tx, name string, key []byte) error {
	keyBucket := Bconcat(zetKeyPrefix, S2b(name))
	scoreBucket := Bconcat(zetScorePrefix, S2b(name))
	b1 := tx.Bucket(keyBucket)
	if b1 == nil {
		return nil
	}
	b2 := tx.Bucket(scoreBucket)
	if b2 == nil {
		return nil
	}

	oldScore := b2.Get(key)
	if oldScore != nil {
		oldKey := Bconcat(oldScore, key)
		err := b1.Delete(oldKey)
		if err != nil {
			return err
		}
		return b2.Delete(key)
	}
	return nil
}

// Zmdel delete specified multiple keys of a zset.
func (db *DB) Zmdel(tx *bolt.Tx, name string, keys [][]byte) error {
	keyBucket := Bconcat(zetKeyPrefix, S2b(name))
	scoreBucket := Bconcat(zetScorePrefix, S2b(name))

	b1 := tx.Bucket(keyBucket)
	if b1 == nil {
		return nil
	}
	b2 := tx.Bucket(scoreBucket)
	if b2 == nil {
		return nil
	}

	for _, key := range keys {
		oldScore := b2.Get(key)
		if oldScore != nil {
			oldKey := Bconcat(oldScore, key)
			_ = b1.Delete(oldKey)
			_ = b2.Delete(key)
		}
	}
	return nil
}

// ZdelBucket delete all keys in a zset.
func (db *DB) ZdelBucket(tx *bolt.Tx, name string) error {
	keyBucket := Bconcat(zetKeyPrefix, S2b(name))
	scoreBucket := Bconcat(zetScorePrefix, S2b(name))
	err := tx.DeleteBucket(keyBucket)
	if err != nil {
		return err
	}
	return tx.DeleteBucket(scoreBucket)
}

// Zget get the score related to the specified key of a zset.
func (db *DB) Zget(tx *bolt.Tx, name string, key []byte) *Reply {
	r := &Reply{
		State: replyError,
		Data:  []BS{},
	}
	scoreBucket := Bconcat(zetScorePrefix, S2b(name))
	b := tx.Bucket(scoreBucket)
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
	r.Data = append(r.Data, v)

	return r
}

// Zsequence returns the current integer for the bucket without incrementing it.
func (db *DB) Zsequence(tx *bolt.Tx, name string) (uint64, error) {
	scoreBucket := Bconcat(zetScorePrefix, S2b(name))

	b := tx.Bucket(scoreBucket)
	if b == nil {
		return 0, ErrBucketNotFound
	}

	return b.Sequence(), nil
}

// ZsetSequence updates the sequence number for the bucket.
func (db *DB) ZsetSequence(tx *bolt.Tx, name string, v uint64) error {
	scoreBucket := Bconcat(zetScorePrefix, S2b(name))
	b := tx.Bucket(scoreBucket)
	if b == nil {
		var err error
		b, err = tx.CreateBucket(scoreBucket)
		if err != nil {
			return err
		}
	}
	return b.SetSequence(v)
}

// ZnextSequence updates the sequence number for the bucket.
func (db *DB) ZnextSequence(tx *bolt.Tx, name string) (uint64, error) {
	scoreBucket := Bconcat(zetScorePrefix, S2b(name))
	var sequence uint64
	var err error

	b := tx.Bucket(scoreBucket)
	if b == nil {
		b, err = tx.CreateBucket(scoreBucket)
		if err != nil {
			return 0, err
		}
	}
	sequence, err = b.NextSequence()
	if err != nil {
		return 0, err
	}

	return sequence, nil
}

// Zmget get the values related to the specified multiple keys of a zset.
func (db *DB) Zmget(tx *bolt.Tx, name string, keys [][]byte) *Reply {
	r := &Reply{
		State: replyError,
		Data:  []BS{},
	}
	scoreBucket := Bconcat(zetScorePrefix, S2b(name))

	b := tx.Bucket(scoreBucket)
	if b == nil {
		r.State = bucketNotFound
		return r
	}
	for _, key := range keys {
		v := b.Get(key)
		if v != nil {
			r.Data = append(r.Data, key, v)
		}
	}
	if len(r.Data) > 0 {
		r.State = replyOK
	}

	return r
}

// Zscan list key-score pairs in a zset, where key-score in range (key_start+score_start, score_end].
func (db *DB) Zscan(tx *bolt.Tx, name string, keyStart, scoreStart []byte, limit int) *Reply {
	r := &Reply{
		State: replyError,
		Data:  []BS{},
	}
	keyBucket := Bconcat(zetKeyPrefix, S2b(name))

	scoreStartB := I2b(scoreMin)
	if len(scoreStart) > 0 {
		scoreStartB = make([]byte, len(scoreStart))
		copy(scoreStartB, scoreStart)
	}

	startScoreKeyB := Bconcat(scoreStartB, keyStart)

	b := tx.Bucket(keyBucket)
	if b == nil {
		r.State = bucketNotFound
		return r
	}
	c := b.Cursor()
	n := 0

	for k, _ := c.Seek(scoreStartB); k != nil; k, _ = c.Next() {
		// 增加长度安全检查，防止切片越界 Panic
		if len(k) < uint64EncodedLen {
			continue
		}
		if bytes.Compare(k, startScoreKeyB) == 1 {
			r.Data = append(r.Data, k[uint64EncodedLen:], k[0:uint64EncodedLen])
			n++
			if n == limit {
				break
			}
		}
	}
	if n > 0 {
		r.State = replyOK
	}

	return r
}

// Zrscan list key-score pairs of a zset, in reverse order.
func (db *DB) Zrscan(tx *bolt.Tx, name string, keyStart, scoreStart []byte, limit int) *Reply {
	r := &Reply{
		State: replyError,
		Data:  []BS{},
	}
	keyBucket := Bconcat(zetKeyPrefix, S2b(name))

	startKey := []byte{255}
	if len(keyStart) > 0 {
		startKey = make([]byte, len(keyStart))
		copy(startKey, keyStart)
	}

	scoreStartB := I2b(scoreMax)
	if len(scoreStart) > 0 {
		scoreStartB = make([]byte, len(scoreStart))
		copy(scoreStartB, scoreStart)
	}

	startScoreKeyB := Bconcat(scoreStartB, startKey)

	b := tx.Bucket(keyBucket)
	if b == nil {
		r.State = bucketNotFound
		return r
	}
	c := b.Cursor()

	var k0, v0 []byte
	if len(scoreStart) > 0 {
		k0, v0 = c.Seek(scoreStartB)
	} else {
		k0, v0 = c.Last()
	}

	n := 0
	for k, _ := k0, v0; k != nil; k, _ = c.Prev() {
		if bytes.Compare(k, startScoreKeyB) == -1 {
			r.Data = append(r.Data, k[uint64EncodedLen:], k[0:uint64EncodedLen])
			n++
			if n == limit {
				break
			}
		}
	}
	if n > 0 {
		r.State = replyOK
	}

	return r
}

func (r *Reply) OK() bool {
	return r.State == replyOK
}

func (r *Reply) NotFound() bool {
	return r.State == replyNotFound
}

// Clone 深度拷贝 Reply 中的 mmap 数据。
// 重要：如果在 bolt.Tx 事务闭包外使用 Reply 数据，必须调用此方法以防止段错误。
func (r *Reply) Clone() *Reply {
	if len(r.Data) == 0 {
		return r
	}
	newReply := &Reply{
		State: r.State,
		Data:  make([]BS, len(r.Data)),
	}
	for i, b := range r.Data {
		copied := make([]byte, len(b))
		copy(copied, b)
		newReply.Data[i] = copied
	}
	return newReply
}

func (r *Reply) Bytes() []byte {
	if len(r.Data) > 0 {
		return r.Data[0]
	}
	return nil
}

// String is a convenience wrapper over Get for string value.
func (r *Reply) String() string {
	if len(r.Data) > 0 {
		return B2s(r.Data[0])
	}
	return ""
}

// Int is a convenience wrapper over Get for int value of a hashmap.
func (r *Reply) Int() int {
	return int(r.Uint64())
}

// Int64 is a convenience wrapper over Get for int64 value of a hashmap.
func (r *Reply) Int64() int64 {
	if len(r.Data) < 1 {
		return 0
	}
	return int64(r.Uint64())
}

// Uint is a convenience wrapper over Get for uint value of a hashmap.
func (r *Reply) Uint() uint {
	return uint(r.Uint64())
}

// Uint64 is a convenience wrapper over Get for uint64 value of a hashmap.
func (r *Reply) Uint64() uint64 {
	if len(r.Data) < 1 {
		return 0
	}
	if len(r.Data[0]) < uint64EncodedLen {
		return 0
	}
	return binary.BigEndian.Uint64(r.Data[0])
}

// List retrieves the key/value pairs from reply of a hashmap.
func (r *Reply) List() []Entry {
	if len(r.Data) < 1 {
		return []Entry{}
	}
	list := make([]Entry, len(r.Data)/2)
	j := 0
	for i := 0; i < (len(r.Data) - 1); i += 2 {
		list[j] = Entry{r.Data[i], r.Data[i+1]}
		j++
	}
	return list
}

// Dict retrieves the key/value pairs from reply of a hashmap.
func (r *Reply) Dict() map[string][]byte {
	if len(r.Data) < 1 {
		return map[string][]byte{}
	}
	dict := make(map[string][]byte, len(r.Data)/2)
	for i := 0; i < (len(r.Data) - 1); i += 2 {
		dict[B2s(r.Data[i])] = r.Data[i+1]
	}
	return dict
}

func (r *Reply) KvLen() int {
	return len(r.Data) / 2
}

func (r *Reply) KvEach(fn func(key, value BS)) int {
	for i := 0; i < (len(r.Data) - 1); i += 2 {
		fn(r.Data[i], r.Data[i+1])
	}
	return r.KvLen()
}

// JSON parses the JSON-encoded Reply Entry value and stores the result
// in the value pointed to by v.
func (r *Reply) JSON(v interface{}) error {
	if r == nil || len(r.Data) == 0 {
		return errors.New("empty reply")
	}
	if v == nil {
		return errors.New("nil json destination")
	}
	return json.Unmarshal(r.Data[0], v)
}

func (b BS) Bytes() []byte {
	return b
}

func (b BS) String() string {
	return B2s(b)
}

// Int is a convenience wrapper over Get for int value of a hashmap.
func (b BS) Int() int {
	return int(b.Uint64())
}

// Int64 is a convenience wrapper over Get for int64 value of a hashmap.
func (b BS) Int64() int64 {
	return int64(b.Uint64())
}

// Uint is a convenience wrapper over Get for uint value of a hashmap.
func (b BS) Uint() uint {
	return uint(b.Uint64())
}

// Uint64 is a convenience wrapper over Get for uint64 value of a hashmap.
func (b BS) Uint64() uint64 {
	if len(b) < uint64EncodedLen {
		return 0
	}
	return binary.BigEndian.Uint64(b)
}

// JSON parses the JSON-encoded Reply Entry value and stores the result
// in the value pointed to by v.
func (b BS) JSON(v interface{}) error {
	if v == nil {
		return errors.New("nil json destination")
	}
	return json.Unmarshal(b, v)
}

// Bconcat 性能略微优化的实现
func Bconcat(slices ...[]byte) []byte {
	var totalLen int
	for _, s := range slices {
		totalLen += len(s)
	}
	tmp := make([]byte, 0, totalLen)
	for _, s := range slices {
		tmp = append(tmp, s...)
	}
	return tmp
}

// DS2b returns an 8-byte big endian representation of Digit string
// v ("123456") -> uint64(123456) -> 8-byte big endian.
func DS2b(v string) []byte {
	i, err := strconv.ParseUint(v, 10, 64)
	if err != nil {
		return []byte("")
	}
	return I2b(i)
}

// DS2i returns uint64 of Digit string
// v ("123456") -> uint64(123456).
func DS2i(v string) uint64 {
	i, err := strconv.ParseUint(v, 10, 64)
	if err != nil {
		return uint64(0)
	}
	return i
}

// I2b returns an 8-byte big endian representation of v
// v uint64(123456) -> 8-byte big endian.
func I2b(v uint64) []byte {
	b := make([]byte, uint64EncodedLen)
	binary.BigEndian.PutUint64(b, v)
	return b
}

// B2i return an int64 of v
// v (8-byte big endian) -> uint64(123456).
func B2i(v []byte) uint64 {
	if len(v) < uint64EncodedLen {
		return 0
	}
	return binary.BigEndian.Uint64(v)
}

// B2ds return a Digit string of v
// v (8-byte big endian) -> uint64(123456) -> "123456".
func B2ds(v []byte) string {
	return strconv.FormatUint(binary.BigEndian.Uint64(v), 10)
}

// B2s converts byte slice to a string without memory allocation (Go 1.20+ safe).
func B2s(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	return unsafe.String(unsafe.SliceData(b), len(b))
}

// S2b converts string to a byte slice without memory allocation (Go 1.20+ safe).
func S2b(s string) []byte {
	if len(s) == 0 {
		return nil
	}
	return unsafe.Slice(unsafe.StringData(s), len(s))
}
