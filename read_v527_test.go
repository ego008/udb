package udb

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

func TestV527ReadBatchBorrowedOwnership(t *testing.T) {
	db := openV57TestDB(t)
	defer db.Close()
	if err := db.HSet("h", []byte("a"), []byte("va")); err != nil {
		t.Fatal(err)
	}

	key := []byte("a")
	rb, err := db.NewReadBatch()
	if err != nil {
		t.Fatal(err)
	}
	if err := rb.HGetBorrowed("h", key); err != nil {
		t.Fatal(err)
	}
	key[0] = 'b'
	var got string
	if err := rb.Execute(func(r ReadBatchResult) error { got = string(r.Key); return nil }); err != nil {
		t.Fatal(err)
	}
	if got != "b" {
		t.Fatalf("borrowed key=%q", got)
	}

	safeKey := []byte("a")
	rb, err = db.NewReadBatch()
	if err != nil {
		t.Fatal(err)
	}
	if err := rb.HGet("h", safeKey); err != nil {
		t.Fatal(err)
	}
	safeKey[0] = 'b'
	if err := rb.Execute(func(r ReadBatchResult) error {
		if string(r.Key) != "a" || !r.Found || string(r.Value) != "va" {
			t.Fatalf("unexpected result: %+v", r)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestV527ReadBatchSortedAndUnsorted(t *testing.T) {
	db := openV57TestDB(t)
	defer db.Close()
	for _, k := range []string{"a", "b", "c", "d"} {
		if err := db.HSet("h", []byte(k), []byte("v-"+k)); err != nil {
			t.Fatal(err)
		}
		if err := db.ZSet("z", []byte(k), uint64(len(k))); err != nil {
			t.Fatal(err)
		}
	}

	keys := [][]byte{[]byte("a"), []byte("b"), []byte("d"), []byte("e")}
	var seen []string
	if err := db.HGetMany("h", keys, func(i int, key, value []byte, found bool) error {
		seen = append(seen, string(key)+":"+string(value))
		if i == 3 && found {
			t.Fatal("e unexpectedly found")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	want := []string{"a:v-a", "b:v-b", "d:v-d", "e:"}
	if !bytes.Equal([]byte(joinStrings(seen)), []byte(joinStrings(want))) {
		t.Fatalf("seen=%v want=%v", seen, want)
	}

	unsorted := [][]byte{[]byte("d"), []byte("a"), []byte("c")}
	var order []string
	if err := db.ZScoreMany("z", unsorted, func(i int, key []byte, score uint64, found bool) error {
		if !found || score != 1 {
			t.Fatalf("i=%d key=%q score=%d found=%v", i, key, score, found)
		}
		order = append(order, string(key))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got := joinStrings(order); got != "dac" {
		t.Fatalf("order=%q", got)
	}
}

func TestV527ReadBatchSortedDuplicateAndContext(t *testing.T) {
	db := openV57TestDB(t)
	defer db.Close()
	_ = db.HSet("h", []byte("a"), []byte("v"))
	keys := [][]byte{[]byte("a"), []byte("a"), []byte("b")}
	var n int
	if err := db.HGetMany("h", keys, func(i int, key, value []byte, found bool) error {
		n++
		if i < 2 && (!found || string(value) != "v") {
			t.Fatal("duplicate lookup failed")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("n=%d", n)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := db.HGetManyContext(ctx, "h", keys, func(int, []byte, []byte, bool) error { t.Fatal("callback called"); return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
}

func TestV527ReadBatchMixedStillPreservesOrder(t *testing.T) {
	db := openV57TestDB(t)
	defer db.Close()
	_ = db.HSet("h", []byte("a"), []byte("ha"))
	_ = db.ZSet("z", []byte("b"), 7)
	rb, err := db.NewReadBatch()
	if err != nil {
		t.Fatal(err)
	}
	_ = rb.HGetBorrowed("h", []byte("a"))
	_ = rb.ZScoreBorrowed("z", []byte("b"))
	var got []ReadBatchItemKind
	if err := rb.Execute(func(r ReadBatchResult) error { got = append(got, r.Kind); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != ReadBatchHGet || got[1] != ReadBatchZScore {
		t.Fatalf("got=%v", got)
	}
}

func joinStrings(v []string) string {
	var b bytes.Buffer
	for _, s := range v {
		b.WriteString(s)
	}
	return b.String()
}
