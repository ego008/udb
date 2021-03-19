package bdb

import (
	"errors"
	"github.com/boltdb/bolt"
	"testing"
)

var db *DB
var err error
var bucket = "test"

func init() {
	db, err = Open("b.db")
	if err != nil {
		panic(err)
	}
}

func TestTx_Hset(t *testing.T) {
	err = db.Update(func(tx *bolt.Tx) error {
		db.Tx = tx
		return db.Hset(bucket, []byte("key1"), []byte("value1"))
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTx_Hget(t *testing.T) {
	err = db.View(func(tx *bolt.Tx) error {
		db.Tx = tx
		rs := db.Hget(bucket, []byte("key1"))
		if rs.String() != "value1" {
			return errors.New(" Hget err")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTx_Hscan(t *testing.T) {
	err = db.View(func(tx *bolt.Tx) error {
		db.Tx = tx
		rs := db.Hscan(bucket, nil, 2)
		if !rs.OK() || rs.KvLen() != 1 {
			return errors.New(" Hscan err")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTx_Hset_TxNil(t *testing.T) {
	err = db.Update(func(tx *bolt.Tx) error {
		return db.Hset(bucket, []byte("key1"), []byte("value1"))
	})
	if err != ErrTxClosed {
		t.Fatal(err)
	}
}

func TestTx_Hset_TxNil2(t *testing.T) {
	err = db.Hset(bucket, []byte("key1"), []byte("value1"))
	if err != ErrTxClosed {
		t.Fatal(err)
	}
}

func TestTx_Hset_TxNotWritable(t *testing.T) {
	err = db.View(func(tx *bolt.Tx) error {
		db.Tx = tx
		return db.Hset(bucket, []byte("key1"), []byte("value1"))
	})
	if err != bolt.ErrTxNotWritable {
		t.Fatal(err)
	}
}

func TestTx_Zset(t *testing.T) {
	err = db.Update(func(tx *bolt.Tx) error {
		db.Tx = tx
		return db.Zset(bucket, []byte("key1"), 2)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTx_Zget(t *testing.T) {
	err = db.View(func(tx *bolt.Tx) error {
		db.Tx = tx
		rs := db.Zget(bucket, []byte("key1"))
		if rs.Uint64() != 2 {
			return errors.New(" Zget err")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
