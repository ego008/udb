package udb

import "bytes"

// ReadPath identifies the physical read algorithm selected by the adaptive
// read planner.
type ReadPath uint8

const (
	ReadPathPoint ReadPath = iota + 1
	ReadPathCursor
)

// ReadPlannerOptions controls automatic selection of point-vs-cursor reads.
// The defaults are deliberately conservative: cursor scans are preferred only
// when enough keys are requested or the requested keys are locally clustered.
type ReadPlannerOptions struct {
	// MinCursorKeys is the minimum number of sorted keys for which the cursor
	// path is considered without requiring a locality signal.
	MinCursorKeys int
	// LocalityThreshold is the minimum normalized adjacent-key locality score
	// for a smaller sorted batch to use the cursor path.
	LocalityThreshold float64
	// MinLocalityKeys enables locality-based selection once this many keys are
	// present. Smaller batches use point lookups unless MinCursorKeys is met.
	MinLocalityKeys int
}

func defaultReadPlannerOptions() ReadPlannerOptions {
	return ReadPlannerOptions{
		MinCursorKeys:     16,
		LocalityThreshold: 0.50,
		MinLocalityKeys:   4,
	}
}

// ReadPlan describes the planner's decision for a homogeneous point-read set.
type ReadPlan struct {
	Path           ReadPath
	Sorted         bool
	KeyCount       int
	Locality       float64
	DuplicateRatio float64
	Reason         string
}

// PlanReadKeys exposes the default deterministic planner used by the optimized
// read paths. It does not touch the database and is safe to call before a read.
func PlanReadKeys(keys [][]byte) ReadPlan {
	return planReadKeys(keys, defaultReadPlannerOptions())
}

// PlanReadKeysWithOptions exposes planner tuning for applications that have
// measured a different workload shape. It still never changes read semantics;
// it only selects point lookup versus sequential cursor traversal.
func PlanReadKeysWithOptions(keys [][]byte, opts ReadPlannerOptions) ReadPlan {
	return planReadKeys(keys, opts)
}

func planReadOps(ops []readBatchOp, opts ReadPlannerOptions) ReadPlan {
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

	plan.Sorted = true
	duplicates := 0
	var localitySum float64
	for i := 1; i < len(ops); i++ {
		a, b := ops[i-1].key, ops[i].key
		cmp := bytes.Compare(a, b)
		if cmp > 0 {
			plan.Sorted = false
			plan.Reason = "unsorted-or-small"
			return plan
		}
		if cmp == 0 {
			duplicates++
		}
		localitySum += adjacentLocality(a, b)
	}
	plan.DuplicateRatio = float64(duplicates) / float64(len(ops)-1)
	plan.Locality = localitySum / float64(len(ops)-1)
	if len(ops) >= opts.MinCursorKeys {
		plan.Path = ReadPathCursor
		plan.Reason = "sorted-enough-keys"
		return plan
	}
	if len(ops) >= opts.MinLocalityKeys && (plan.Locality >= opts.LocalityThreshold || plan.DuplicateRatio >= 0.25) {
		plan.Path = ReadPathCursor
		plan.Reason = "sorted-local"
		return plan
	}
	plan.Reason = "sorted-sparse-small"
	return plan
}

func planReadKeys(keys [][]byte, opts ReadPlannerOptions) ReadPlan {
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

	plan := ReadPlan{Path: ReadPathPoint, KeyCount: len(keys), Reason: "unsorted-or-small"}
	if len(keys) < 2 {
		return plan
	}

	sorted := true
	duplicates := 0
	var localitySum float64
	localityPairs := 0
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
		localityPairs++
	}
	if !sorted {
		return plan
	}
	plan.Sorted = true
	plan.DuplicateRatio = float64(duplicates) / float64(len(keys)-1)
	if localityPairs > 0 {
		plan.Locality = localitySum / float64(localityPairs)
	}

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

// adjacentLocality measures how much of two adjacent keys is shared. It is a
// generic byte-key heuristic; it never affects correctness, only physical path
// selection. Equal keys receive the strongest locality score.
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
