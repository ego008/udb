package udb

import (
	"bytes"
	"testing"
)

func TestV529FastPlannerLargeBatch(t *testing.T) {
	keys := make([][]byte, 100)
	for i := range keys {
		keys[i] = []byte("user:" + string([]byte{byte(i >> 8), byte(i)}))
	}
	p := planReadKeysFast(keys, defaultReadPlannerOptions())
	if p.Path != ReadPathCursor || !p.Sorted || p.Reason != "sorted-enough-keys" {
		t.Fatalf("unexpected fast plan: %+v", p)
	}
}

func TestV529FastPlannerPreservesSmallLocality(t *testing.T) {
	keys := [][]byte{[]byte("user:0001"), []byte("user:0002"), []byte("user:0003"), []byte("user:0004")}
	p := planReadKeysFast(keys, defaultReadPlannerOptions())
	if p.Path != ReadPathCursor || p.Reason != "sorted-local" {
		t.Fatalf("plan=%+v", p)
	}
}

func TestV529ReadSemanticsUnchanged(t *testing.T) {
	db := openV57TestDB(t)
	defer db.Close()
	for i := 0; i < 128; i++ {
		if err := db.HSet("h", []byte{byte(i)}, []byte{byte(i + 1)}); err != nil {
			t.Fatal(err)
		}
	}
	keys := make([][]byte, 100)
	for i := range keys {
		keys[i] = []byte{byte(i)}
	}
	var count int
	if err := db.HGetMany("h", keys, func(i int, key, value []byte, found bool) error {
		if !found || len(value) != 1 || value[0] != key[0]+1 {
			t.Fatalf("bad read i=%d key=%x value=%x found=%v", i, key, value, found)
		}
		count++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if count != len(keys) {
		t.Fatalf("count=%d", count)
	}
}

func TestV529PlannerPublicDiagnosticsRemainStable(t *testing.T) {
	keys := [][]byte{[]byte("user:0001"), []byte("user:0002"), []byte("user:0003"), []byte("user:0004")}
	p := PlanReadKeys(keys)
	if p.Path != ReadPathCursor || p.Reason != "sorted-local" || p.Locality <= 0.5 {
		t.Fatalf("plan=%+v", p)
	}
	if !bytes.Equal(keys[0], []byte("user:0001")) {
		t.Fatal("planner mutated input")
	}
}
