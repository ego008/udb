package udb

import bolt "go.etcd.io/bbolt"

// Tx is UDB's transaction type. The underlying bbolt transaction is kept
// private so application code cannot accidentally create a raw transaction
// that bypasses UDB lifecycle tracking.
//
// UDB operations are intentionally exposed as DB/Tx methods instead of
// exposing *bbolt.Tx. A Tx is valid only inside the callback passed to View or
// Update.
type Tx struct {
	inner *bolt.Tx
}

func newTx(tx *bolt.Tx) *Tx { return &Tx{inner: tx} }

func (tx *Tx) bucket(name []byte) *bolt.Bucket {
	if tx == nil || tx.inner == nil {
		return nil
	}
	return tx.inner.Bucket(name)
}

func (tx *Tx) createBucket(name []byte) (*bolt.Bucket, error) {
	return tx.inner.CreateBucket(name)
}

func (tx *Tx) createBucketIfNotExists(name []byte) (*bolt.Bucket, error) {
	return tx.inner.CreateBucketIfNotExists(name)
}

func (tx *Tx) deleteBucket(name []byte) error {
	return tx.inner.DeleteBucket(name)
}

func (tx *Tx) check() <-chan error {
	return tx.inner.Check()
}
