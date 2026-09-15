package udb

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

func TestV528ReadPlanner(t *testing.T) {
	cases := []struct {
		name   string
		keys   [][]byte
		path   ReadPath
		reason string
	}{
		{"tiny", [][]byte{[]byte("a"), []byte("b")}, ReadPathPoint, "sorted-sparse-small"},
		{"local", [][]byte{[]byte("k-001"), []byte("k-002"), []byte("k-003"), []byte("k-004")}, ReadPathCursor, "sorted-local"},
		{"large", [][]byte{[]byte("a"), []byte("b")}, ReadPathPoint, "sorted-sparse-small"},
		{"unsorted", [][]byte{[]byte("c"), []byte("a"), []byte("b")}, ReadPathPoint, "unsorted-or-small"},
	}
	for _, tc := range cases {
		if tc.name == "large" {
			keys := make([][]byte, 16)
			for i := range keys {
				keys[i] = []byte{byte(i)}
			}
			tc.keys = keys
			tc.path = ReadPathCursor
			tc.reason = "sorted-enough-keys"
		}
		p := PlanReadKeys(tc.keys)
		if p.Path != tc.path || p.Reason != tc.reason || p.KeyCount != len(tc.keys) {
			t.Fatalf("%s: plan=%+v", tc.name, p)
		}
	}
}

func TestV528ReadPlannerDuplicateAndLocality(t *testing.T) {
	local := PlanReadKeys([][]byte{[]byte("user:0001"), []byte("user:0002"), []byte("user:0003"), []byte("user:0004")})
	if local.Path != ReadPathCursor || local.Locality <= 0.5 {
		t.Fatalf("local plan=%+v", local)
	}
	dup := PlanReadKeys([][]byte{[]byte("a"), []byte("a"), []byte("b"), []byte("b")})
	if dup.Path != ReadPathCursor || dup.DuplicateRatio < 0.25 {
		t.Fatalf("duplicate plan=%+v", dup)
	}
}

func TestV528AdaptiveReadBatchPreservesOrder(t *testing.T) {
	db := openV57TestDB(t)
	defer db.Close()
	for i := 0; i < 32; i++ {
		key := []byte{byte(i)}
		if err := db.HSet("h", key, append([]byte("v-"), key...)); err != nil {
			t.Fatal(err)
		}
	}

	rb, err := db.NewReadBatch()
	if err != nil {
		t.Fatal(err)
	}
	keys := [][]byte{[]byte{1}, []byte{3}, []byte{3}, []byte{7}, []byte{31}}
	for _, key := range keys {
		if err := rb.HGetBorrowed("h", key); err != nil {
			t.Fatal(err)
		}
	}
	var got [][]byte
	if err := rb.Execute(func(r ReadBatchResult) error {
		got = append(got, append([]byte(nil), r.Key...))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for i := range keys {
		if !bytes.Equal(got[i], keys[i]) {
			t.Fatalf("order mismatch i=%d got=%x want=%x", i, got[i], keys[i])
		}
	}
}

func TestV528PlannerDoesNotChangeReadSemantics(t *testing.T) {
	db := openV57TestDB(t)
	defer db.Close()
	for i := 0; i < 100; i++ {
		key := []byte{byte(i)}
		if err := db.ZSet("z", key, uint64(i+100)); err != nil {
			t.Fatal(err)
		}
	}

	keys := make([][]byte, 100)
	for i := range keys {
		keys[i] = []byte{byte(i)}
	}
	if err := db.ZScoreMany("z", keys, func(i int, key []byte, score uint64, found bool) error {
		if !found || score != uint64(i+100) || !bytes.Equal(key, keys[i]) {
			t.Fatalf("i=%d key=%x score=%d found=%v", i, key, score, found)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestV528CustomPlannerOptions(t *testing.T) {
	keys := [][]byte{[]byte("a"), []byte("b"), []byte("c"), []byte("d")}
	p := PlanReadKeysWithOptions(keys, ReadPlannerOptions{MinCursorKeys: 100, MinLocalityKeys: 100, LocalityThreshold: 1})
	if p.Path != ReadPathPoint {
		t.Fatalf("unexpected custom plan=%+v", p)
	}
}

func TestV528ContextAndCallbackError(t *testing.T) {
	db := openV57TestDB(t)
	defer db.Close()
	_ = db.HSet("h", []byte("a"), []byte("v"))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := db.HGetManyContext(ctx, "h", [][]byte{[]byte("a")}, func(int, []byte, []byte, bool) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("context err=%v", err)
	}

	want := errors.New("stop")
	if err := db.HGetMany("h", [][]byte{[]byte("a"), []byte("a")}, func(int, []byte, []byte, bool) error { return want }); !errors.Is(err, want) {
		t.Fatalf("callback err=%v", err)
	}
}
