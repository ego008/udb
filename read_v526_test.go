package udb

import (
	"context"
	"errors"
	"testing"
)

func TestV526ReadBatchZeroCopy(t *testing.T) {
	db := openV57TestDB(t)
	defer db.Close()
	if err := db.HSet("h", []byte("a"), []byte("va")); err != nil {
		t.Fatal(err)
	}
	if err := db.ZSet("z", []byte("b"), 42); err != nil {
		t.Fatal(err)
	}
	b, err := db.NewReadBatch()
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if err := b.HGet("h", []byte("a")); err != nil {
		t.Fatal(err)
	}
	if err := b.ZScore("z", []byte("b")); err != nil {
		t.Fatal(err)
	}
	var seen int
	err = b.Execute(func(r ReadBatchResult) error {
		seen++
		if !r.Found {
			return errors.New("expected found")
		}
		switch r.Kind {
		case ReadBatchHGet:
			if string(r.Value) != "va" {
				t.Fatalf("value=%q", r.Value)
			}
		case ReadBatchZScore:
			if r.Score != 42 {
				t.Fatalf("score=%d", r.Score)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if seen != 2 {
		t.Fatalf("seen=%d", seen)
	}
	if err := b.Execute(func(ReadBatchResult) error { return nil }); !errors.Is(err, ErrReadBatchClosed) {
		t.Fatalf("second execute err=%v", err)
	}
}

func TestV526ReadBatchCallbackErrorIsAtomicRead(t *testing.T) {
	db := openV57TestDB(t)
	defer db.Close()
	_ = db.HSet("h", []byte("a"), []byte("v"))
	b, _ := db.NewReadBatch()
	_ = b.HGet("h", []byte("a"))
	want := errors.New("stop")
	if err := b.Execute(func(ReadBatchResult) error { return want }); !errors.Is(err, want) {
		t.Fatalf("err=%v", err)
	}
}

func TestV526ReadBatchContext(t *testing.T) {
	db := openV57TestDB(t)
	defer db.Close()
	b, _ := db.NewReadBatch()
	_ = b.HGet("h", []byte("a"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := b.ExecuteContext(ctx, func(ReadBatchResult) error { t.Fatal("callback called"); return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
}

func TestV526IteratorLowAllocationOwnership(t *testing.T) {
	db := openV57TestDB(t)
	defer db.Close()
	for i := 0; i < 100; i++ {
		if err := db.HSet("h", []byte{byte(i)}, []byte("value")); err != nil {
			t.Fatal(err)
		}
	}
	it, err := db.NewHIterator("h", nil, 100)
	if err != nil {
		t.Fatal(err)
	}
	defer it.Close()
	var n int
	for it.Next() {
		n++
		if len(it.Key()) != 1 || string(it.Value()) != "value" {
			t.Fatal("bad item")
		}
	}
	if n != 100 || it.Err() != nil {
		t.Fatalf("n=%d err=%v", n, it.Err())
	}
}
