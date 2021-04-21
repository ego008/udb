package udb

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
		return db.Hset(tx, bucket, []byte("key1"), []byte("value1"))
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTx_Hget(t *testing.T) {
	err = db.View(func(tx *bolt.Tx) error {
		rs := db.Hget(tx, bucket, []byte("key1"))
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
		rs := db.Hscan(tx, bucket, nil, 2)
		if !rs.OK() || rs.KvLen() != 1 {
			return errors.New(" Hscan err")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTx_Zset(t *testing.T) {
	err = db.Update(func(tx *bolt.Tx) error {
		return db.Zset(tx, bucket, []byte("key1"), 2)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTx_Zget(t *testing.T) {
	err = db.View(func(tx *bolt.Tx) error {
		rs := db.Zget(tx, bucket, []byte("key1"))
		if rs.Uint64() != 2 {
			return errors.New(" Zget err")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
