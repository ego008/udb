package udb

import (
	"context"
	"errors"
	"testing"
)

func TestV525ReadSessionDoesNotHoldReadTx(t *testing.T) {
	db := openV57TestDB(t)
	defer db.Close()
	if err := db.HSet("h", []byte("a"), []byte("1")); err != nil {
		t.Fatal(err)
	}
	s, err := db.NewReadSession()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if r := s.HGet("h", []byte("a")); r.Err != nil || r.State != replyOK {
		t.Fatalf("read: %+v", r)
	}
	if err := db.HSet("h", []byte("b"), []byte("2")); err != nil {
		t.Fatal(err)
	}
	if r := s.HGet("h", []byte("b")); r.Err != nil || r.State != replyOK {
		t.Fatalf("second read: %+v", r)
	}
}

func TestV525ReadSessionConsistentReadCallback(t *testing.T) {
	db := openV57TestDB(t)
	defer db.Close()
	_ = db.HSet("h", []byte("a"), []byte("1"))
	_ = db.HSet("h", []byte("b"), []byte("2"))
	s, err := db.NewReadSession()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	count := 0
	err = s.Read(func(r *ReadEngine) error {
		for _, k := range [][]byte{[]byte("a"), []byte("b")} {
			if x := r.HGet("h", k); x.Err != nil || x.State != replyOK {
				return x.ErrOrNil()
			}
			count++
		}
		return nil
	})
	if err != nil || count != 2 {
		t.Fatalf("read session: err=%v count=%d", err, count)
	}
}

func TestV525IteratorOwnershipAndClose(t *testing.T) {
	db := openV57TestDB(t)
	defer db.Close()
	for i, k := range []string{"a", "b", "c"} {
		if err := db.HSet("h", []byte(k), []byte{byte('0' + i)}); err != nil {
			t.Fatal(err)
		}
	}
	it, err := db.NewHIterator("h", nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	var got int
	for it.Next() {
		if len(it.Key()) == 0 || len(it.Value()) == 0 {
			t.Fatal("empty item")
		}
		got++
	}
	if err := it.Err(); err != nil {
		t.Fatal(err)
	}
	if got != 3 {
		t.Fatalf("got %d", got)
	}
	_ = it.Close()
	if it.Next() {
		t.Fatal("closed iterator advanced")
	}
}

func TestV525ZIteratorAndReverse(t *testing.T) {
	db := openV57TestDB(t)
	defer db.Close()
	for i, k := range []string{"a", "b", "c"} {
		if err := db.ZSet("z", []byte(k), uint64(i+1)); err != nil {
			t.Fatal(err)
		}
	}
	it, err := db.NewZIterator("z", nil, nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	var scores []uint64
	for it.Next() {
		scores = append(scores, it.Score())
	}
	if len(scores) != 3 || scores[0] != 1 || scores[2] != 3 {
		t.Fatalf("scores=%v", scores)
	}
	_ = it.Close()
	rit, err := db.NewZRIterator("z", nil, nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !rit.Next() || rit.Score() != 3 {
		t.Fatalf("reverse first=%d", rit.Score())
	}
	_ = rit.Close()
}

func TestV525IteratorSlowConsumerDoesNotBlockWrite(t *testing.T) {
	db := openV57TestDB(t)
	defer db.Close()
	for i := 0; i < 20; i++ {
		if err := db.ZSet("z", []byte{byte(i + 1)}, uint64(i)); err != nil {
			t.Fatal(err)
		}
	}
	it, err := db.NewZIterator("z", nil, nil, 20)
	if err != nil {
		t.Fatal(err)
	}
	defer it.Close()
	// The iterator owns materialized memory, so writes can proceed before the
	// consumer advances or closes it.
	if err := db.HSet("h", []byte("after"), []byte("write")); err != nil {
		t.Fatal(err)
	}
}

func TestV525ContextAndSessionClose(t *testing.T) {
	db := openV57TestDB(t)
	defer db.Close()
	s, err := db.NewReadSession()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.ReadContext(ctx, func(*ReadEngine) error { t.Fatal("callback must not run"); return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if r := s.HGet("h", []byte("x")); !errors.Is(r.Err, ErrDatabaseClosed) {
		t.Fatalf("closed session err=%v", r.Err)
	}
}
