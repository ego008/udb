package udb

import "testing"

func TestV517HashSetBatchAtomicValidation(t *testing.T) {
	db := openV57TestDB(t)
	defer db.Close()

	entries := []Entry{
		{Key: []byte("a"), Value: []byte("1")},
		{Key: nil, Value: []byte("bad")},
	}
	if err := db.HSetBatch("h", entries); err == nil {
		t.Fatal("expected invalid-key error")
	}
	if r := db.HGet("h", []byte("a")); r.State != bucketNotFound && r.State != keyNotFound {
		t.Fatalf("partial batch write detected: state=%s err=%v", r.State, r.Err)
	}
}

func TestV517ZSetBatchAtomicValidation(t *testing.T) {
	db := openV57TestDB(t)
	defer db.Close()

	entries := []ZEntry{
		{Member: []byte("a"), Score: 1},
		{Member: nil, Score: 2},
	}
	if err := db.ZSetBatch("z", entries); err == nil {
		t.Fatal("expected invalid-key error")
	}
	if r := db.ZGet("z", []byte("a")); r.State != bucketNotFound && r.State != keyNotFound {
		t.Fatalf("partial batch write detected: state=%s err=%v", r.State, r.Err)
	}
}

func TestV517BatchPersistence(t *testing.T) {
	path := t.TempDir() + "/batch.db"
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.HSetBatch("h", []Entry{{Key: []byte("a"), Value: []byte("1")}, {Key: []byte("b"), Value: []byte("2")}}); err != nil {
		t.Fatal(err)
	}
	if err := db.ZSetBatch("z", []ZEntry{{Member: []byte("a"), Score: 10}, {Member: []byte("b"), Score: 20}}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if r := db.HGet("h", []byte("b")); r.State != replyOK || string(r.Data[0]) != "2" {
		t.Fatalf("hash batch did not persist: %+v", r)
	}
	if r := db.ZGet("z", []byte("a")); r.State != replyOK {
		t.Fatalf("zset batch did not persist: %+v", r)
	}
	if _, err := db.CheckIntegrity(); err != nil {
		t.Fatal(err)
	}
}
