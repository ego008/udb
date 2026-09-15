package udb

import "bytes"

// ReadPath identifies the physical read algorithm selected by the adaptive
// read planner.
type ReadPath uint8

const (
	ReadPathPoint ReadPath = iota + 1
	ReadPathCursor
)

type ReadPlannerOptions struct {
	MinCursorKeys     int
	LocalityThreshold float64
	MinLocalityKeys   int
	// HeadProbeKeys bounds the First+Next probe before a sorted cursor
	// falls back to Seek. It is used by V5.31/V5.32 adaptive reads.
	HeadProbeKeys int
	// CostModel controls Point-vs-Cursor selection for sufficiently large
	// sorted batches. Zero values use the V5.33 defaults.
	CostModel ReadCostModelOptions
	// CostModelMinKeys is the minimum batch size at which the V5.33 cost
	// model is allowed to override the historical V5.29/V5.32 cursor
	// threshold. This preserves compatibility for callers that intentionally
	// lower MinCursorKeys for small-batch cursor experiments.
	CostModelMinKeys int
	// CursorStart selects the cursor access path when the planner chooses a
	// cursor. Zero is normalized to ReadCursorAdaptive for compatibility with
	// callers that construct ReadPlannerOptions using older fields only.
	CursorStart ReadCursorStart
}

func defaultReadPlannerOptions() ReadPlannerOptions {
	return ReadPlannerOptions{MinCursorKeys: 16, LocalityThreshold: 0.50, MinLocalityKeys: 4, HeadProbeKeys: 8, CostModel: defaultReadCostModelOptions(), CostModelMinKeys: 16, CursorStart: ReadCursorAdaptive}
}

type ReadPlan struct {
	Path ReadPath
	// AccessPath is the V5.32 physical access-path decision. It is redundant
	// with Path for compatibility, but makes Point/First/Seek/Adaptive explicit
	// for diagnostics and benchmark comparison.
	AccessPath ReadAccessPath
	// CursorStart describes how the sorted cursor path should position itself.
	// It is ReadCursorAdaptive for production reads in V5.31.
	CursorStart         ReadCursorStart
	CursorProbeKeys     int
	EstimatedPointCost  float64
	EstimatedCursorCost float64
	Sorted              bool
	KeyCount            int
	Locality            float64
	DuplicateRatio      float64
	Reason              string
}

func PlanReadKeys(keys [][]byte) ReadPlan { return planReadKeys(keys, defaultReadPlannerOptions()) }
func PlanReadKeysWithOptions(keys [][]byte, opts ReadPlannerOptions) ReadPlan {
	return planReadKeys(keys, opts)
}

func normalizePlannerOptions(opts ReadPlannerOptions) ReadPlannerOptions {
	if opts.MinCursorKeys < 2 {
		opts.MinCursorKeys = 2
	}
	if opts.MinLocalityKeys < 2 {
		opts.MinLocalityKeys = 2
	}
	if opts.LocalityThreshold < 0 {
		opts.LocalityThreshold = 0
	}
	if opts.LocalityThreshold > 1 {
		opts.LocalityThreshold = 1
	}
	if opts.HeadProbeKeys < 1 {
		opts.HeadProbeKeys = 1
	}
	opts.CostModel = normalizeReadCostModelOptions(opts.CostModel)
	if opts.CostModelMinKeys < opts.MinCursorKeys {
		opts.CostModelMinKeys = opts.MinCursorKeys
	}
	if opts.CostModelMinKeys < 1 {
		opts.CostModelMinKeys = 1
	}
	if opts.CursorStart != ReadCursorFirst && opts.CursorStart != ReadCursorSeek && opts.CursorStart != ReadCursorAdaptive {
		opts.CursorStart = ReadCursorAdaptive
	}
	return opts
}

// planReadKeys is the public/full planner. It retains the V5.28 locality and
// duplicate diagnostics for callers that inspect ReadPlan.
func planReadKeys(keys [][]byte, opts ReadPlannerOptions) ReadPlan {
	opts = normalizePlannerOptions(opts)
	plan := ReadPlan{Path: ReadPathPoint, AccessPath: ReadAccessPoint, CursorStart: opts.CursorStart, CursorProbeKeys: opts.HeadProbeKeys, KeyCount: len(keys), Reason: "unsorted-or-small"}
	if len(keys) < 2 {
		return plan
	}

	sorted := true
	duplicates := 0
	var localitySum float64
	for i := 1; i < len(keys); i++ {
		cmp := bytes.Compare(keys[i-1], keys[i])
		if cmp > 0 {
			sorted = false
			break
		}
		if cmp == 0 {
			duplicates++
		}
		localitySum += adjacentLocality(keys[i-1], keys[i])
	}
	if !sorted {
		return plan
	}
	plan.Sorted = true
	plan.DuplicateRatio = float64(duplicates) / float64(len(keys)-1)
	plan.Locality = localitySum / float64(len(keys)-1)
	if len(keys) >= opts.MinCursorKeys {
		plan.EstimatedPointCost, plan.EstimatedCursorCost = estimateReadCosts(len(keys), plan.DuplicateRatio, opts.CostModel)
		if len(keys) < opts.CostModelMinKeys || shouldUseCursorByCost(len(keys), plan.DuplicateRatio, opts.CostModel) {
			plan.Path = ReadPathCursor
			plan.CursorStart = opts.CursorStart
			plan.AccessPath = accessPathFromCursorStart(opts.CursorStart)
			plan.Reason = "sorted-enough-keys"
			return plan
		}
		plan.Reason = "sorted-cost-point"
		return plan
	}
	if len(keys) >= opts.MinLocalityKeys && (plan.Locality >= opts.LocalityThreshold || plan.DuplicateRatio >= 0.25) {
		plan.Path = ReadPathCursor
		plan.CursorStart = opts.CursorStart
		plan.AccessPath = accessPathFromCursorStart(opts.CursorStart)
		plan.Reason = "sorted-local"
		return plan
	}
	plan.Reason = "sorted-sparse-small"
	return plan
}

// planReadKeysFast is the hot-path planner used by actual reads. Once the
// batch is large enough that MinCursorKeys decides the path, it only checks
// sortedness; locality and duplicate statistics cannot change the decision.
// This removes the second O(n) heuristic pass and avoids floating-point work.
func planReadKeysFast(keys [][]byte, opts ReadPlannerOptions) ReadPlan {
	opts = normalizePlannerOptions(opts)
	n := len(keys)
	plan := ReadPlan{Path: ReadPathPoint, AccessPath: ReadAccessPoint, CursorStart: opts.CursorStart, CursorProbeKeys: opts.HeadProbeKeys, KeyCount: n, Reason: "unsorted-or-small"}
	if n < 2 {
		return plan
	}

	// For small batches, retain the V5.28 locality heuristic because it can
	// still change the decision below MinCursorKeys.
	if n < opts.MinCursorKeys {
		return planReadKeys(keys, opts)
	}

	duplicates := 0
	for i := 1; i < n; i++ {
		cmp := bytes.Compare(keys[i-1], keys[i])
		if cmp > 0 {
			return plan
		}
		if cmp == 0 {
			duplicates++
		}
	}
	plan.Sorted = true
	plan.DuplicateRatio = float64(duplicates) / float64(n-1)
	plan.EstimatedPointCost, plan.EstimatedCursorCost = estimateReadCosts(n, plan.DuplicateRatio, opts.CostModel)
	if n < opts.CostModelMinKeys || shouldUseCursorByCost(n, plan.DuplicateRatio, opts.CostModel) {
		plan.Path = ReadPathCursor
		plan.CursorStart = opts.CursorStart
		plan.AccessPath = accessPathFromCursorStart(opts.CursorStart)
		plan.Reason = "sorted-enough-keys"
		return plan
	}
	plan.Reason = "sorted-cost-point"
	return plan
}

func planReadOps(ops []readBatchOp, opts ReadPlannerOptions) ReadPlan {
	return planReadOpsFast(ops, opts)
}

// planReadOpsFast avoids constructing a temporary [][]byte for the common
// homogeneous ReadBatch case. When diagnostics are not needed it only checks
// the comparisons required to choose the physical path.
func planReadOpsFast(ops []readBatchOp, opts ReadPlannerOptions) ReadPlan {
	return planReadOpsFastMode(ops, opts, true)
}

func planReadOpsFastMode(ops []readBatchOp, opts ReadPlannerOptions, hot bool) ReadPlan {
	opts = normalizePlannerOptions(opts)
	plan := ReadPlan{Path: ReadPathPoint, AccessPath: ReadAccessPoint, CursorStart: opts.CursorStart, CursorProbeKeys: opts.HeadProbeKeys, KeyCount: len(ops), Reason: "unsorted-or-small"}
	if len(ops) < 2 {
		return plan
	}
	first := ops[0]
	for i := 1; i < len(ops); i++ {
		if ops[i].kind != first.kind || ops[i].name != first.name {
			plan.Reason = "mixed-bucket-or-kind"
			return plan
		}
	}
	if len(ops) < opts.MinCursorKeys {
		if hot {
			// Small batches still use the exact V5.28 heuristic. The temporary
			// slice is avoided by comparing directly.
			return planReadOpsFullSmall(ops, opts)
		}
	}
	for i := 1; i < len(ops); i++ {
		if bytes.Compare(ops[i-1].key, ops[i].key) > 0 {
			return plan
		}
	}
	plan.Sorted = true
	duplicates := 0
	for i := 1; i < len(ops); i++ {
		if bytes.Equal(ops[i-1].key, ops[i].key) {
			duplicates++
		}
	}
	plan.DuplicateRatio = float64(duplicates) / float64(len(ops)-1)
	plan.EstimatedPointCost, plan.EstimatedCursorCost = estimateReadCosts(len(ops), plan.DuplicateRatio, opts.CostModel)
	if len(ops) < opts.CostModelMinKeys || shouldUseCursorByCost(len(ops), plan.DuplicateRatio, opts.CostModel) {
		plan.Path = ReadPathCursor
		plan.CursorStart = opts.CursorStart
		plan.AccessPath = accessPathFromCursorStart(opts.CursorStart)
		// Preserve the V5.29/V5.30 diagnostic reason for the normal cursor
		// decision so existing callers/tests remain compatible.
		plan.Reason = "sorted-enough-keys"
		return plan
	}
	plan.Reason = "sorted-cost-point"
	return plan
}

func planReadOpsFullSmall(ops []readBatchOp, opts ReadPlannerOptions) ReadPlan {
	plan := ReadPlan{Path: ReadPathPoint, AccessPath: ReadAccessPoint, CursorStart: opts.CursorStart, CursorProbeKeys: opts.HeadProbeKeys, KeyCount: len(ops), Reason: "unsorted-or-small"}
	if len(ops) < 2 {
		return plan
	}
	duplicates := 0
	var localitySum float64
	for i := 1; i < len(ops); i++ {
		a, b := ops[i-1].key, ops[i].key
		cmp := bytes.Compare(a, b)
		if cmp > 0 {
			return plan
		}
		if cmp == 0 {
			duplicates++
		}
		localitySum += adjacentLocality(a, b)
	}
	plan.Sorted = true
	plan.DuplicateRatio = float64(duplicates) / float64(len(ops)-1)
	plan.Locality = localitySum / float64(len(ops)-1)
	if len(ops) >= opts.MinLocalityKeys && (plan.Locality >= opts.LocalityThreshold || plan.DuplicateRatio >= 0.25) {
		plan.EstimatedPointCost, plan.EstimatedCursorCost = estimateReadCosts(len(ops), plan.DuplicateRatio, opts.CostModel)
		plan.Path = ReadPathCursor
		plan.CursorStart = opts.CursorStart
		plan.AccessPath = accessPathFromCursorStart(opts.CursorStart)
		plan.Reason = "sorted-local"
		return plan
	}
	plan.Reason = "sorted-sparse-small"
	return plan
}

func adjacentLocality(a, b []byte) float64 {
	maxLen := len(a)
	if len(b) > maxLen {
		maxLen = len(b)
	}
	if maxLen == 0 {
		return 1
	}
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return float64(n) / float64(maxLen)
}
