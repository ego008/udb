package udb

import (
	"encoding/binary"
	"errors"

	bolt "go.etcd.io/bbolt"
)

// writeOpKind is the common internal representation used by Batch, the
// asynchronous WritePipeline, and the public batch helpers. Keeping one
// mutation core prevents the three write paths from slowly acquiring subtly
// different Hash/ZSet semantics.
type writeOpKind uint8

const (
	writeHSet writeOpKind = iota + 1
	writeHDel
	writeZSet
	writeZDel
)

type writeOp struct {
	kind  writeOpKind
	name  string
	key   []byte
	value []byte
	score uint64
}

var errInvalidWriteOp = errors.New("udb: invalid write operation")

func validateWriteOp(op *writeOp) error {
	if op == nil {
		return errInvalidWriteOp
	}
	if err := validName(op.name); err != nil {
		return err
	}
	if err := validKey(op.key); err != nil {
		return err
	}
	switch op.kind {
	case writeHSet, writeHDel, writeZSet, writeZDel:
		return nil
	default:
		return errInvalidWriteOp
	}
}

// applyWriteOps is the single mutation engine for all UDB batch-like writes.
// It intentionally preserves operation order and relies on the surrounding
// bbolt transaction for atomicity. Buckets are cached per transaction so a
// large batch does not repeatedly perform top-level bucket lookups.
func applyWriteOps(tx *Tx, ops []writeOp) error {
	if err := validTx(tx); err != nil {
		return err
	}

	hashes := make(map[string]*bolt.Bucket)
	zsets := make(map[string]*zsetBucketsCache)

	for i := range ops {
		op := &ops[i]
		if err := validateWriteOp(op); err != nil {
			return err
		}

		switch op.kind {
		case writeHSet:
			b, err := cachedHashBucket(tx, hashes, op.name, true)
			if err != nil {
				return err
			}
			if err := b.Put(op.key, op.value); err != nil {
				return err
			}
		case writeHDel:
			b, err := cachedHashBucket(tx, hashes, op.name, false)
			if err != nil {
				return err
			}
			if b != nil {
				if err := b.Delete(op.key); err != nil {
					return err
				}
			}
		case writeZSet:
			z, err := cachedZSetBuckets(tx, zsets, op.name, true)
			if err != nil {
				return err
			}
			var score [uint64EncodedLen]byte
			binary.BigEndian.PutUint64(score[:], op.score)
			if err := zsetIntoBuckets(z.key, z.score, op.key, score[:]); err != nil {
				return err
			}
		case writeZDel:
			z, err := cachedZSetBuckets(tx, zsets, op.name, false)
			if err != nil {
				return err
			}
			if z == nil || z.score == nil {
				continue
			}
			oldScore := z.score.Get(op.key)
			if oldScore == nil {
				continue
			}
			if z.key != nil {
				if err := z.key.Delete(Bconcat(oldScore, op.key)); err != nil {
					return err
				}
			}
			if err := z.score.Delete(op.key); err != nil {
				return err
			}
		}
	}
	return nil
}

type zsetBucketsCache struct {
	key   *bolt.Bucket
	score *bolt.Bucket
}

func cachedHashBucket(tx *Tx, cache map[string]*bolt.Bucket, name string, create bool) (*bolt.Bucket, error) {
	if b, ok := cache[name]; ok && (b != nil || !create) {
		return b, nil
	}
	bucket := tx.bucket(bucketName(hashPrefix, name))
	if bucket == nil && create {
		var err error
		bucket, err = getOrCreateBucket(tx, bucketName(hashPrefix, name))
		if err != nil {
			return nil, err
		}
	}
	cache[name] = bucket
	return bucket, nil
}

func cachedZSetBuckets(tx *Tx, cache map[string]*zsetBucketsCache, name string, create bool) (*zsetBucketsCache, error) {
	if z, ok := cache[name]; ok && (!create || (z != nil && z.key != nil && z.score != nil)) {
		return z, nil
	}
	z := &zsetBucketsCache{
		key:   tx.bucket(bucketName(zetKeyPrefix, name)),
		score: tx.bucket(bucketName(zetScorePrefix, name)),
	}
	if create {
		var err error
		if z.key == nil {
			z.key, err = getOrCreateBucket(tx, bucketName(zetKeyPrefix, name))
			if err != nil {
				return nil, err
			}
		}
		if z.score == nil {
			z.score, err = getOrCreateBucket(tx, bucketName(zetScorePrefix, name))
			if err != nil {
				return nil, err
			}
		}
	}
	cache[name] = z
	return z, nil
}
