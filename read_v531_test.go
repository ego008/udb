package udb

import (
	"bytes"
	"context"
	"fmt"
	"testing"
)

func TestV531PlannerSelectsAdaptiveCursor(t *testing.T) {
	keys := [][]byte{
		[]byte("k-0001"), []byte("k-0002"), []byte("k-0003"), []byte("k-0004"),
		[]byte("k-0005"), []byte("k-0006"), []byte("k-0007"), []byte("k-0008"),
		[]byte("k-0009"), []byte("k-0010"), []byte("k-0011"), []byte("k-0012"),
		[]byte("k-0013"), []byte("k-0014"), []byte("k-0015"), []byte("k-0016"),
	}
	p := planReadKeysFast(keys, defaultReadPlannerOptions())
	if p.Path != ReadPathCursor || p.CursorStart != ReadCursorAdaptive {
		t.Fatalf("unexpected plan: %+v", p)
	}
}

func TestV531AdaptiveCursorOptionsNormalize(t *testing.T) {
	if got := normalizeReadCursorOptions(ReadCursorOptions{}).HeadProbeKeys; got != 1 {
		t.Fatalf("HeadProbeKeys=%d, want 1", got)
	}
	if got := normalizeReadCursorOptions(ReadCursorOptions{HeadProbeKeys: 12}).HeadProbeKeys; got != 12 {
		t.Fatalf("HeadProbeKeys=%d, want 12", got)
	}
}

func TestV531SortedManySemantics(t *testing.T) {
	db := openV57TestDB(t)
	defer db.Close()
	for i := 0; i < 128; i++ {
		if err := db.HSet("h", []byte(fmt.Sprintf("k-%06d", i)), []byte(fmt.Sprintf("v-%d", i))); err != nil {
			t.Fatal(err)
		}
	}
	keys := make([][]byte, 32)
	for i := range keys {
		keys[i] = []byte(fmt.Sprintf("k-%06d", 40+i))
	}
	seen := 0
	if err := db.HGetManyContext(context.Background(), "h", keys, func(i int, key, value []byte, found bool) error {
		if i != seen || !found || !bytes.Equal(key, keys[i]) || string(value) != fmt.Sprintf("v-%d", 40+i) {
			t.Fatalf("bad result i=%d key=%q value=%q found=%v", i, key, value, found)
		}
		seen++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if seen != len(keys) {
		t.Fatalf("seen=%d want=%d", seen, len(keys))
	}
}
