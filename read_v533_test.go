package udb

import "testing"

func TestV533CostModelDefaults(t *testing.T) {
	opts := defaultReadCostModelOptions()
	if opts.PointPerKey <= 0 || opts.CursorBase <= 0 || opts.CursorPerKey < 0 {
		t.Fatalf("invalid defaults: %+v", opts)
	}
}

func TestV533CostModelCrossover(t *testing.T) {
	opts := defaultReadPlannerOptions()
	keys10 := make([][]byte, 10)
	keys100 := make([][]byte, 100)
	for i := range keys10 {
		keys10[i] = []byte("k-" + pad8(i))
	}
	for i := range keys100 {
		keys100[i] = []byte("k-" + pad8(i))
	}
	p10 := planReadKeysFast(keys10, opts)
	p100 := planReadKeysFast(keys100, opts)
	if p10.Path != ReadPathPoint && p10.Path != ReadPathCursor {
		t.Fatalf("unexpected 10-key path: %+v", p10)
	}
	if p100.Path != ReadPathCursor {
		t.Fatalf("100-key plan should choose cursor: %+v", p100)
	}
	if !(p100.EstimatedCursorCost < p100.EstimatedPointCost) {
		t.Fatalf("bad costs: %+v", p100)
	}
}

func TestV533CostModelCanForcePointForLargeSortedBatch(t *testing.T) {
	opts := defaultReadPlannerOptions()
	opts.CostModel = ReadCostModelOptions{
		PointPerKey:   1,
		CursorBase:    1000,
		CursorPerKey:  1,
		DuplicateGain: 0,
	}
	keys := make([][]byte, 100)
	for i := range keys {
		keys[i] = []byte("k-" + pad8(i))
	}
	p := planReadKeysFast(keys, opts)
	if p.Path != ReadPathPoint || p.Reason != "sorted-cost-point" {
		t.Fatalf("plan=%+v", p)
	}
	if p.EstimatedPointCost >= p.EstimatedCursorCost {
		t.Fatalf("costs=%+v", p)
	}
}

func TestV533CostModelDuplicateDiscount(t *testing.T) {
	opts := defaultReadCostModelOptions()
	point1, cursor1 := estimateReadCosts(100, 0, opts)
	point2, cursor2 := estimateReadCosts(100, 1, opts)
	if point1 != point2 {
		t.Fatalf("point cost changed with duplicates: %v %v", point1, point2)
	}
	if cursor2 >= cursor1 {
		t.Fatalf("duplicate discount not applied: %v >= %v", cursor2, cursor1)
	}
}

func TestV533PlannerCompatibility(t *testing.T) {
	local := [][]byte{[]byte("user:0001"), []byte("user:0002"), []byte("user:0003"), []byte("user:0004")}
	p := PlanReadKeys(local)
	if p.Path != ReadPathCursor || p.Reason != "sorted-local" || p.Locality <= 0.5 {
		t.Fatalf("plan=%+v", p)
	}
	large := make([][]byte, 100)
	for i := range large {
		large[i] = []byte("user:" + pad8(i))
	}
	p = PlanReadKeys(large)
	if p.Path != ReadPathCursor || p.Reason != "sorted-enough-keys" {
		t.Fatalf("large plan=%+v", p)
	}
}

func TestV533CostModelMinKeysCompatibility(t *testing.T) {
	opts := defaultReadPlannerOptions()
	opts.MinCursorKeys = 2
	opts.CostModelMinKeys = 16
	keys := [][]byte{[]byte("k-0001"), []byte("k-0002"), []byte("k-0003"), []byte("k-0004")}
	p := planReadKeysFast(keys, opts)
	if p.Path != ReadPathCursor || p.Reason != "sorted-enough-keys" {
		t.Fatalf("historical small cursor threshold changed: %+v", p)
	}
}
