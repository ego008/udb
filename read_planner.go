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
}

func defaultReadPlannerOptions() ReadPlannerOptions {
	return ReadPlannerOptions{MinCursorKeys: 16, LocalityThreshold: 0.50, MinLocalityKeys: 4}
}

type ReadPlan struct {
	Path           ReadPath
	Sorted         bool
	KeyCount       int
	Locality       float64
	DuplicateRatio float64
	Reason         string
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
	return opts
}

// planReadKeys is the public/full planner. It retains the V5.28 locality and
// duplicate diagnostics for callers that inspect ReadPlan.
func planReadKeys(keys [][]byte, opts ReadPlannerOptions) ReadPlan {
	opts = normalizePlannerOptions(opts)
	plan := ReadPlan{Path: ReadPathPoint, KeyCount: len(keys), Reason: "unsorted-or-small"}
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
		plan.Path = ReadPathCursor
		plan.Reason = "sorted-enough-keys"
		return plan
	}
	if len(keys) >= opts.MinLocalityKeys && (plan.Locality >= opts.LocalityThreshold || plan.DuplicateRatio >= 0.25) {
		plan.Path = ReadPathCursor
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
	plan := ReadPlan{Path: ReadPathPoint, KeyCount: n, Reason: "unsorted-or-small"}
	if n < 2 {
		return plan
	}

	// For small batches, retain the V5.28 locality heuristic because it can
	// still change the decision below MinCursorKeys.
	if n < opts.MinCursorKeys {
		return planReadKeys(keys, opts)
	}

	for i := 1; i < n; i++ {
		if bytes.Compare(keys[i-1], keys[i]) > 0 {
			return plan
		}
	}
	plan.Sorted = true
	plan.Path = ReadPathCursor
	plan.Reason = "sorted-enough-keys"
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
	plan := ReadPlan{Path: ReadPathPoint, KeyCount: len(ops), Reason: "unsorted-or-small"}
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
	plan.Path = ReadPathCursor
	plan.Reason = "sorted-enough-keys"
	return plan
}

func planReadOpsFullSmall(ops []readBatchOp, opts ReadPlannerOptions) ReadPlan {
	plan := ReadPlan{Path: ReadPathPoint, KeyCount: len(ops), Reason: "unsorted-or-small"}
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
		plan.Path = ReadPathCursor
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
