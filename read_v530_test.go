package udb

import (
	"context"
	"fmt"
	"testing"
)

func TestV530ReadEngineStackConstruction(t *testing.T) {
	db := openV57TestDB(t)
	defer db.Close()
	if err := db.HSet("h", []byte("k"), []byte("v")); err != nil {
		t.Fatal(err)
	}
	var got string
	err := db.View(func(tx *Tx) error {
		r := newReadEngine(tx)
		reply := r.HGet("h", []byte("k"))
		if err := reply.ErrOrNil(); err != nil {
			return err
		}
		got = string(reply.Data[0])
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != "v" {
		t.Fatalf("got %q", got)
	}
}

func TestV530SortedManyStartsWithSeek(t *testing.T) {
	db := openV57TestDB(t)
	defer db.Close()
	for i := 0; i < 100; i++ {
		if err := db.HSet("h", []byte(fmt.Sprintf("k-%03d", i)), []byte(fmt.Sprintf("v-%03d", i))); err != nil {
			t.Fatal(err)
		}
	}
	keys := [][]byte{[]byte("k-070"), []byte("k-071"), []byte("k-099")}
	var got []string
	err := db.HGetManyContext(context.Background(), "h", keys, func(i int, key, value []byte, found bool) error {
		if !found {
			t.Fatalf("key %d not found", i)
		}
		got = append(got, string(value))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"v-070", "v-071", "v-099"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestV530ZScoreManyStartsWithSeek(t *testing.T) {
	db := openV57TestDB(t)
	defer db.Close()
	for i := 0; i < 100; i++ {
		if err := db.ZSet("z", []byte(fmt.Sprintf("k-%03d", i)), uint64(i)); err != nil {
			t.Fatal(err)
		}
	}
	keys := [][]byte{[]byte("k-070"), []byte("k-071"), []byte("k-099")}
	var got []uint64
	err := db.ZScoreManyContext(context.Background(), "z", keys, func(i int, key []byte, score uint64, found bool) error {
		if !found {
			t.Fatalf("key %d not found", i)
		}
		got = append(got, score)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []uint64{70, 71, 99}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestV530UnsortedManySemanticsUnchanged(t *testing.T) {
	db := openV57TestDB(t)
	defer db.Close()
	for i := 0; i < 20; i++ {
		if err := db.HSet("h", []byte(fmt.Sprintf("k-%02d", i)), []byte(fmt.Sprintf("v-%02d", i))); err != nil {
			t.Fatal(err)
		}
	}
	keys := [][]byte{[]byte("k-09"), []byte("k-02"), []byte("missing"), []byte("k-02")}
	seen := make([]string, len(keys))
	found := make([]bool, len(keys))
	err := db.HGetMany("h", keys, func(i int, key, value []byte, ok bool) error {
		found[i] = ok
		if ok {
			seen[i] = string(value)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !found[0] || seen[0] != "v-09" || !found[1] || seen[1] != "v-02" || found[2] || !found[3] || seen[3] != "v-02" {
		t.Fatalf("unexpected result found=%v seen=%v", found, seen)
	}
}
