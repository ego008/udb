package udb

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestV522AtomicRollback(t *testing.T) {
	db := openV57TestDB(t)
	defer db.Close()
	if err := db.Atomic(func(tx *Tx) error {
		if err := db.Hset(tx, "v522", []byte("ok"), []byte("value")); err != nil {
			return err
		}
		return errors.New("forced rollback")
	}); err == nil {
		t.Fatal("expected rollback error")
	}
	if r := db.HGet("v522", []byte("ok")); r.State != bucketNotFound && r.State != keyNotFound {
		t.Fatalf("partial write after rollback: state=%s err=%v", r.State, r.Err)
	}
}

func TestV522BatchBuilderAtomicAndInputCopy(t *testing.T) {
	db := openV57TestDB(t)
	defer db.Close()
	key := []byte("key")
	value := []byte("value")
	b, err := db.NewBatch()
	if err != nil {
		t.Fatal(err)
	}
	if err := b.HSet("h", key, value); err != nil {
		t.Fatal(err)
	}
	if err := b.ZSet("z", []byte("member"), 42); err != nil {
		t.Fatal(err)
	}
	key[0] = 'X'
	value[0] = 'X'
	if b.Len() != 2 {
		t.Fatalf("len=%d", b.Len())
	}
	if err := b.Commit(); err != nil {
		t.Fatal(err)
	}
	if got := db.HGet("h", []byte("key")); got.State != replyOK || string(got.Data[0]) != "value" {
		t.Fatalf("bad hash: %+v", got)
	}
	if score, err := func() (uint64, error) {
		r := db.ZGet("z", []byte("member"))
		if r.Err != nil {
			return 0, r.Err
		}
		return DecodeUint64(r.Data[0])
	}(); err != nil || score != 42 {
		t.Fatalf("bad zset score=%d err=%v", score, err)
	}
	if err := b.Commit(); !errors.Is(err, ErrBatchCommitted) {
		t.Fatalf("second commit err=%v", err)
	}
}

func TestV522EmptyBatch(t *testing.T) {
	db := openV57TestDB(t)
	defer db.Close()
	b, err := db.NewBatch()
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Commit(); err != nil {
		t.Fatal(err)
	}
	if b.Len() != 0 {
		t.Fatalf("len=%d", b.Len())
	}
}

func TestV522SnapshotConsistency(t *testing.T) {
	db := openV57TestDB(t)
	defer db.Close()
	if err := db.HSet("h", []byte("k"), []byte("v1")); err != nil {
		t.Fatal(err)
	}
	s, err := db.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := db.HSet("h", []byte("k"), []byte("v2")); err != nil {
		t.Fatal(err)
	}
	if r := s.HGet("h", []byte("k")); r.State != replyOK || string(r.Data[0]) != "v1" {
		t.Fatalf("snapshot changed: %+v", r)
	}
	if r := db.HGet("h", []byte("k")); r.State != replyOK || string(r.Data[0]) != "v2" {
		t.Fatalf("live value wrong: %+v", r)
	}
}

func TestV522SnapshotCloseAndMetrics(t *testing.T) {
	db := openV57TestDB(t)
	defer db.Close()
	db.ResetMetrics()
	if err := db.View(func(tx *Tx) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := db.Update(func(tx *Tx) error { return nil }); err != nil {
		t.Fatal(err)
	}
	s, err := db.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	m := db.Metrics()
	if m.ViewTransactions != 2 || m.UpdateTransactions != 1 || m.SnapshotsOpened != 1 || m.SnapshotsActive != 1 {
		t.Fatalf("metrics before close: %+v", m)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	m = db.Metrics()
	if m.SnapshotsClosed != 1 || m.SnapshotsActive != 0 {
		t.Fatalf("metrics after close: %+v", m)
	}
}

func TestV522SnapshotCloseIdempotent(t *testing.T) {
	db := openV57TestDB(t)
	defer db.Close()
	s, err := db.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if r := s.HGet("h", []byte("k")); r.Err != ErrDatabaseClosed {
		t.Fatalf("expected closed snapshot error: %+v", r)
	}
}

func TestV522ContextCancellation(t *testing.T) {
	db := openV57TestDB(t)
	defer db.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := db.AtomicContext(ctx, func(tx *Tx) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
	if err := db.ReadTransactionContext(ctx, func(tx *Tx) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
}

func TestV522SnapshotDoesNotBlockWrites(t *testing.T) {
	db := openV57TestDB(t)
	defer db.Close()
	if err := db.HSet("h", []byte("k"), []byte("v1")); err != nil {
		t.Fatal(err)
	}
	s, err := db.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	done := make(chan error, 1)
	go func() { done <- db.HSet("h", []byte("k"), []byte("v2")) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("write blocked while snapshot remained open")
	}

	if r := s.HGet("h", []byte("k")); r.State != replyOK || string(r.Data[0]) != "v1" {
		t.Fatalf("snapshot changed after live write: %+v", r)
	}
}

func TestV522SnapshotDoesNotBlockClose(t *testing.T) {
	db := openV57TestDB(t)
	if err := db.HSet("h", []byte("k"), []byte("v1")); err != nil {
		db.Close()
		t.Fatal(err)
	}
	s, err := db.Snapshot()
	if err != nil {
		db.Close()
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() { done <- db.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		_ = s.Close()
		t.Fatal("DB.Close blocked while snapshot remained open")
	}

	if r := s.HGet("h", []byte("k")); r.State != replyOK || string(r.Data[0]) != "v1" {
		t.Fatalf("snapshot invalid after source close: %+v", r)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}
