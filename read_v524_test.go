package udb

import (
	"bytes"
	"testing"
)

func TestV524ReadEnginePointReads(t *testing.T) {
	db := openV57TestDB(t)
	defer db.Close()

	if err := db.HSet("h", []byte("a"), []byte("value")); err != nil {
		t.Fatal(err)
	}
	if err := db.ZSet("z", []byte("a"), 42); err != nil {
		t.Fatal(err)
	}

	err := db.ReadTransaction(func(tx *Tx) error {
		r, err := NewReadEngine(tx)
		if err != nil {
			return err
		}
		hr := r.HGet("h", []byte("a"))
		if hr.Err != nil || hr.State != replyOK || !bytes.Equal(hr.Data[0], []byte("value")) {
			t.Fatalf("HGet=%+v", hr)
		}
		hv, err := r.HGetInt("h", []byte("missing"))
		if err != ErrKeyNotFound || hv != 0 {
			t.Fatalf("HGetInt missing value=%d err=%v", hv, err)
		}
		zr := r.ZGet("z", []byte("a"))
		if zr.Err != nil || zr.State != replyOK || B2i(zr.Data[0]) != 42 {
			t.Fatalf("ZGet=%+v", zr)
		}
		zv, err := r.ZScore("z", []byte("a"))
		if err != nil || zv != 42 {
			t.Fatalf("ZScore=%d err=%v", zv, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestV524ReadEngineBatchReads(t *testing.T) {
	db := openV57TestDB(t)
	defer db.Close()
	for i := 0; i < 4; i++ {
		k := []byte{byte('a' + i)}
		if err := db.HSet("h", k, []byte{byte('0' + i)}); err != nil {
			t.Fatal(err)
		}
		if err := db.ZSet("z", k, uint64(i+10)); err != nil {
			t.Fatal(err)
		}
	}
	keys := [][]byte{[]byte("a"), []byte("c"), []byte("missing")}
	err := db.ReadTransaction(func(tx *Tx) error {
		r, err := NewReadEngine(tx)
		if err != nil {
			return err
		}
		hr := r.HGetBatch("h", keys)
		if hr.Err != nil || hr.State != replyOK || len(hr.Data) != 4 {
			t.Fatalf("HGetBatch=%+v", hr)
		}
		zr := r.ZGetBatch("z", keys)
		if zr.Err != nil || zr.State != replyOK || len(zr.Data) != 4 {
			t.Fatalf("ZGetBatch=%+v", zr)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if r := db.HGetBatch("h", [][]byte{[]byte("a"), []byte("missing")}); r.State != replyOK || len(r.Data) != 2 {
		t.Fatalf("public HGetBatch=%+v", r)
	}
	if r := db.ZGetBatch("z", [][]byte{[]byte("a"), []byte("missing")}); r.State != replyOK || len(r.Data) != 2 {
		t.Fatalf("public ZGetBatch=%+v", r)
	}
}

func TestV524ReadEngineScanMatchesCanonical(t *testing.T) {
	db := openV57TestDB(t)
	defer db.Close()
	for _, e := range []ZEntry{
		{Member: []byte("b"), Score: 10},
		{Member: []byte("a"), Score: 10},
		{Member: []byte("c"), Score: 20},
	} {
		if err := db.ZSet("z", e.Member, e.Score); err != nil {
			t.Fatal(err)
		}
	}

	err := db.ReadTransaction(func(tx *Tx) error {
		r, err := NewReadEngine(tx)
		if err != nil {
			return err
		}
		got := r.ZScan("z", nil, nil, 10)
		if got.Err != nil || len(got.Data) != 6 {
			t.Fatalf("ZScan=%+v", got)
		}
		want := []string{"a", "b", "c"}
		for i, member := range want {
			if string(got.Data[i*2]) != member {
				t.Fatalf("entry %d member=%q want=%q", i, got.Data[i*2], member)
			}
		}
		reverse := r.ZRScan("z", nil, nil, 10)
		if reverse.Err != nil || len(reverse.Data) != 6 {
			t.Fatalf("ZRScan=%+v", reverse)
		}
		if string(reverse.Data[0]) != "c" || string(reverse.Data[2]) != "b" || string(reverse.Data[4]) != "a" {
			t.Fatalf("unexpected reverse order: %+v", reverse.Data)
		}
		var count int
		err = r.ZScanEach("z", nil, nil, 10, false, func(member []byte, score uint64) error {
			count++
			if score == 0 || len(member) == 0 {
				t.Fatalf("bad callback member=%q score=%d", member, score)
			}
			return nil
		})
		if err != nil || count != 3 {
			t.Fatalf("ZScanEach count=%d err=%v", count, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestV524ReadEngineInputAndOwnership(t *testing.T) {
	db := openV57TestDB(t)
	defer db.Close()
	key := []byte("k")
	value := []byte("value")
	if err := db.HSet("h", key, value); err != nil {
		t.Fatal(err)
	}
	value[0] = 'X'

	var got BS
	err := db.ReadTransaction(func(tx *Tx) error {
		r, err := NewReadEngine(tx)
		if err != nil {
			return err
		}
		reply := r.HGet("h", key)
		got = reply.Data[0]
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, []byte("value")) {
		t.Fatalf("copied value=%q", got)
	}
}
