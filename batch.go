package udb

import "encoding/binary"

// ZEntry is one ZSet member and its score.
//
// It is intentionally separate from Entry because ZSet scores are uint64 and
// are stored using UDB's canonical fixed-width encoding.
type ZEntry struct {
	Member BS
	Score  uint64
}

// HSetBatch applies all entries in one managed write transaction.
//
// Compared with calling HSet once per item, this avoids one bbolt write
// transaction/commit per item. The operation is atomic: if any item fails,
// the whole transaction is rolled back by bbolt.
func (db *DB) HSetBatch(name string, entries []Entry) error {
	if err := validName(name); err != nil {
		return err
	}
	if len(entries) == 0 {
		return nil
	}
	return db.Update(func(tx *Tx) error {
		b, err := getOrCreateBucket(tx, bucketName(hashPrefix, name))
		if err != nil {
			return err
		}
		for i := range entries {
			if err := validKey(entries[i].Key); err != nil {
				return err
			}
			if err := b.Put(entries[i].Key, entries[i].Value); err != nil {
				return err
			}
		}
		return nil
	})
}

// ZSetBatch applies all ZSet entries in one managed write transaction.
// ZSet's primary score map and secondary ordered index are updated atomically.
func (db *DB) ZSetBatch(name string, entries []ZEntry) error {
	if err := validName(name); err != nil {
		return err
	}
	if len(entries) == 0 {
		return nil
	}
	return db.Update(func(tx *Tx) error {
		// Validate the complete batch before changing the transaction so a bad
		// member cannot leave a partially modified transaction callback.
		for i := range entries {
			if err := validKey(entries[i].Member); err != nil {
				return err
			}
		}
		keyBucket, scoreBucket, err := db.zsetBuckets(tx, name)
		if err != nil {
			return err
		}
		for i := range entries {
			var score [uint64EncodedLen]byte
			binary.BigEndian.PutUint64(score[:], entries[i].Score)
			if err := zsetIntoBuckets(keyBucket, scoreBucket, entries[i].Member, score[:]); err != nil {
				return err
			}
		}
		return nil
	})
}

// HDelBatch deletes multiple Hash keys in one managed write transaction.
func (db *DB) HDelBatch(name string, keys [][]byte) error {
	if err := validName(name); err != nil {
		return err
	}
	if len(keys) == 0 {
		return nil
	}
	return db.Update(func(tx *Tx) error {
		return db.Hmdel(tx, name, keys)
	})
}

// ZDelBatch deletes multiple ZSet members in one managed write transaction.
func (db *DB) ZDelBatch(name string, keys [][]byte) error {
	if err := validName(name); err != nil {
		return err
	}
	if len(keys) == 0 {
		return nil
	}
	return db.Update(func(tx *Tx) error {
		return db.Zmdel(tx, name, keys)
	})
}
