package udb

import (
	"bytes"
	"fmt"
)

type ZSetCheckReport struct {
	Name                                                                                                       string
	KeyIndexEntries, ScoreIndexEntries, MissingKeyIndex, MissingScoreIndex, InvalidScoreValue, InvalidKeyIndex int
	Consistent                                                                                                 bool
}

func (db *DB) HLen(tx *Tx, name string) (int, error) {
	if err := validTx(tx); err != nil {
		return 0, err
	}
	if err := validName(name); err != nil {
		return 0, err
	}
	b := tx.bucket(bucketName(hashPrefix, name))
	if b == nil {
		return 0, nil
	}
	return b.Stats().KeyN, nil
}
func (db *DB) HExists(tx *Tx, name string, key []byte) (bool, error) {
	if err := validTx(tx); err != nil {
		return false, err
	}
	b := tx.bucket(bucketName(hashPrefix, name))
	return b != nil && b.Get(key) != nil, nil
}
func (db *DB) HKeys(tx *Tx, name string, limit int) ([][]byte, error) {
	return db.hKeys(tx, name, limit, false)
}
func (db *DB) HRevKeys(tx *Tx, name string, limit int) ([][]byte, error) {
	return db.hKeys(tx, name, limit, true)
}
func (db *DB) hKeys(tx *Tx, name string, limit int, rev bool) ([][]byte, error) {
	if err := validTx(tx); err != nil {
		return nil, err
	}
	if limit < 0 {
		return nil, ErrInvalidLimit
	}
	b := tx.bucket(bucketName(hashPrefix, name))
	if b == nil {
		return [][]byte{}, nil
	}
	out := make([][]byte, 0)
	c := b.Cursor()
	var k []byte
	if rev {
		k, _ = c.Last()
	} else {
		k, _ = c.First()
	}
	for k != nil && (limit == 0 || len(out) < limit) {
		out = append(out, cloneBytes(k))
		if rev {
			k, _ = c.Prev()
		} else {
			k, _ = c.Next()
		}
	}
	return out, nil
}
func (db *DB) ZCard(tx *Tx, name string) (int, error) {
	if err := validTx(tx); err != nil {
		return 0, err
	}
	b := tx.bucket(bucketName(zetScorePrefix, name))
	if b == nil {
		return 0, nil
	}
	return b.Stats().KeyN, nil
}
func (db *DB) ZExists(tx *Tx, name string, key []byte) (bool, error) {
	if err := validTx(tx); err != nil {
		return false, err
	}
	b := tx.bucket(bucketName(zetScorePrefix, name))
	return b != nil && b.Get(key) != nil, nil
}
func (db *DB) ZRange(tx *Tx, name string, startScore, endScore uint64, limit int) ([]Entry, error) {
	return db.zRange(tx, name, startScore, endScore, limit, false)
}
func (db *DB) ZRevRange(tx *Tx, name string, startScore, endScore uint64, limit int) ([]Entry, error) {
	return db.zRange(tx, name, startScore, endScore, limit, true)
}
func (db *DB) zRange(tx *Tx, name string, startScore, endScore uint64, limit int, rev bool) ([]Entry, error) {
	if err := validTx(tx); err != nil {
		return nil, err
	}
	if limit < 0 {
		return nil, ErrInvalidLimit
	}
	if startScore > endScore {
		return []Entry{}, nil
	}
	b := tx.bucket(bucketName(zetKeyPrefix, name))
	if b == nil {
		return []Entry{}, nil
	}
	c := b.Cursor()
	out := make([]Entry, 0)
	if !rev {
		for k, _ := c.Seek(I2b(startScore)); k != nil; k, _ = c.Next() {
			if len(k) < 8 {
				continue
			}
			s := binaryScore(k)
			if s > endScore {
				break
			}
			out = append(out, Entry{cloneBytes(k[8:]), cloneBytes(k[:8])})
			if limit > 0 && len(out) >= limit {
				break
			}
		}
		return out, nil
	}
	k, _ := c.Seek(I2b(endScore))
	if k == nil {
		k, _ = c.Last()
	} else if len(k) >= 8 && binaryScore(k) > endScore {
		k, _ = c.Prev()
	} else {
		for {
			n, _ := c.Next()
			if n == nil || len(n) < 8 || binaryScore(n) != endScore {
				break
			}
			k = n
		}
	}
	for ; k != nil; k, _ = c.Prev() {
		if len(k) < 8 {
			continue
		}
		s := binaryScore(k)
		if s < startScore {
			break
		}
		out = append(out, Entry{cloneBytes(k[8:]), cloneBytes(k[:8])})
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}
func (db *DB) ZRank(tx *Tx, name string, key []byte) (int, error) {
	return db.zRank(tx, name, key, false)
}
func (db *DB) ZRevRank(tx *Tx, name string, key []byte) (int, error) {
	return db.zRank(tx, name, key, true)
}
func (db *DB) zRank(tx *Tx, name string, key []byte, rev bool) (int, error) {
	if err := validTx(tx); err != nil {
		return -1, err
	}
	sb := tx.bucket(bucketName(zetScorePrefix, name))
	kb := tx.bucket(bucketName(zetKeyPrefix, name))
	if sb == nil || kb == nil {
		return -1, ErrKeyNotFound
	}
	s := sb.Get(key)
	if s == nil || len(s) != 8 {
		return -1, ErrKeyNotFound
	}
	target := Bconcat(s, key)
	rank := 0
	c := kb.Cursor()
	if !rev {
		for k, _ := c.First(); k != nil; k, _ = c.Next() {
			if bytes.Equal(k, target) {
				return rank, nil
			}
			rank++
		}
	} else {
		for k, _ := c.Last(); k != nil; k, _ = c.Prev() {
			if bytes.Equal(k, target) {
				return rank, nil
			}
			rank++
		}
	}
	return -1, ErrKeyNotFound
}
func (db *DB) ZRemRangeByScore(tx *Tx, name string, startScore, endScore uint64) (int, error) {
	if err := validTx(tx); err != nil {
		return 0, err
	}
	if startScore > endScore {
		return 0, nil
	}
	kb := tx.bucket(bucketName(zetKeyPrefix, name))
	sb := tx.bucket(bucketName(zetScorePrefix, name))
	if kb == nil || sb == nil {
		return 0, nil
	}
	keys := make([][]byte, 0)
	c := kb.Cursor()
	for k, _ := c.Seek(I2b(startScore)); k != nil; k, _ = c.Next() {
		if len(k) < 8 {
			continue
		}
		s := binaryScore(k)
		if s > endScore {
			break
		}
		keys = append(keys, cloneBytes(k[8:]))
	}
	for _, key := range keys {
		score := sb.Get(key)
		if score == nil {
			continue
		}
		if err := kb.Delete(Bconcat(score, key)); err != nil {
			return 0, err
		}
		if err := sb.Delete(key); err != nil {
			return 0, err
		}
	}
	return len(keys), nil
}
func (db *DB) CheckZSet(tx *Tx, name string) (*ZSetCheckReport, error) {
	if err := validTx(tx); err != nil {
		return nil, err
	}
	r := &ZSetCheckReport{Name: name}
	kb := tx.bucket(bucketName(zetKeyPrefix, name))
	sb := tx.bucket(bucketName(zetScorePrefix, name))
	if kb == nil && sb == nil {
		r.Consistent = true
		return r, nil
	}
	if kb != nil {
		c := kb.Cursor()
		for k, _ := c.First(); k != nil; k, _ = c.Next() {
			r.KeyIndexEntries++
			if len(k) < 8 {
				r.InvalidKeyIndex++
				continue
			}
			m := k[8:]
			s := k[:8]
			var actual []byte
			if sb != nil {
				actual = sb.Get(m)
			}
			if actual == nil {
				r.MissingScoreIndex++
			} else if !bytes.Equal(actual, s) {
				r.InvalidKeyIndex++
			}
		}
	}
	if sb != nil {
		c := sb.Cursor()
		for m, s := c.First(); m != nil; m, s = c.Next() {
			r.ScoreIndexEntries++
			if len(s) != 8 {
				r.InvalidScoreValue++
				continue
			}
			if kb == nil || kb.Get(Bconcat(s, m)) == nil {
				r.MissingKeyIndex++
			}
		}
	}
	r.Consistent = r.KeyIndexEntries == r.ScoreIndexEntries && r.MissingKeyIndex == 0 && r.MissingScoreIndex == 0 && r.InvalidScoreValue == 0 && r.InvalidKeyIndex == 0
	return r, nil
}
func (db *DB) RepairZSet(tx *Tx, name string) error {
	if err := validTx(tx); err != nil {
		return err
	}
	sb := tx.bucket(bucketName(zetScorePrefix, name))
	if sb == nil {
		return nil
	}
	kb, err := getOrCreateBucket(tx, bucketName(zetKeyPrefix, name))
	if err != nil {
		return err
	}
	var dels [][]byte
	c := kb.Cursor()
	for k, _ := c.First(); k != nil; k, _ = c.Next() {
		dels = append(dels, cloneBytes(k))
	}
	for _, k := range dels {
		if err := kb.Delete(k); err != nil {
			return err
		}
	}
	c = sb.Cursor()
	for m, s := c.First(); m != nil; m, s = c.Next() {
		if len(s) != 8 {
			return fmt.Errorf("udb: invalid zset score length %d", len(s))
		}
		if err := kb.Put(Bconcat(s, m), nil); err != nil {
			return err
		}
	}
	return nil
}
func binaryScore(k []byte) uint64 {
	return uint64(k[0])<<56 | uint64(k[1])<<48 | uint64(k[2])<<40 | uint64(k[3])<<32 | uint64(k[4])<<24 | uint64(k[5])<<16 | uint64(k[6])<<8 | uint64(k[7])
}
