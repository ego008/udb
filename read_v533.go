package udb

// V5.33 Read Engine 5.0 adds a small, deterministic logical cost model to the
// read planner. The model deliberately uses relative cost units rather than
// pretending that nanoseconds measured on one machine are portable. It is
// therefore useful for selecting Point versus Cursor while remaining easy to
// tune with benchmarks on the target machine.
//
// The model is intentionally conservative:
//   - unsorted workloads remain Point;
//   - the existing V5.28 small-locality rule remains authoritative for small
//     clustered/duplicate batches, preserving historical behavior;
//   - for large sorted batches, Point and Cursor estimated costs are compared;
//   - CursorStart still controls First/Seek/Adaptive positioning.
//
// No bucket statistics, cache, long-lived bbolt transaction, or global lock is
// introduced. Runtime execution remains inside one short managed read tx.

// ReadCostModelOptions controls the relative logical cost model.
//
// The values are dimensionless. Only ratios matter. Defaults are intentionally
// simple: one point lookup costs 1 unit, while a cursor has a fixed setup cost
// plus a small per-result traversal cost. With the defaults, a sufficiently
// large sorted batch naturally crosses from Point to Cursor.
type ReadCostModelOptions struct {
	PointPerKey   float64
	CursorBase    float64
	CursorPerKey  float64
	DuplicateGain float64
}

func defaultReadCostModelOptions() ReadCostModelOptions {
	return ReadCostModelOptions{
		PointPerKey:   1.0,
		CursorBase:    12.0,
		CursorPerKey:  0.05,
		DuplicateGain: 0.50,
	}
}

func normalizeReadCostModelOptions(opts ReadCostModelOptions) ReadCostModelOptions {
	if opts.PointPerKey <= 0 {
		opts.PointPerKey = 1
	}
	if opts.CursorBase < 0 {
		opts.CursorBase = 0
	}
	if opts.CursorPerKey < 0 {
		opts.CursorPerKey = 0
	}
	if opts.DuplicateGain < 0 {
		opts.DuplicateGain = 0
	}
	if opts.DuplicateGain > 1 {
		opts.DuplicateGain = 1
	}
	return opts
}

func estimateReadCosts(keyCount int, duplicateRatio float64, opts ReadCostModelOptions) (point, cursor float64) {
	opts = normalizeReadCostModelOptions(opts)
	if keyCount <= 0 {
		return 0, 0
	}
	point = float64(keyCount) * opts.PointPerKey
	traversalKeys := float64(keyCount)
	if duplicateRatio > 0 {
		// Duplicate requests do not require another B-tree advance after the
		// first matching entry. The discount is deliberately bounded by the
		// configured ratio so duplicates can only make Cursor more attractive.
		traversalKeys *= 1 - duplicateRatio*opts.DuplicateGain
	}
	cursor = opts.CursorBase + traversalKeys*opts.CursorPerKey
	return point, cursor
}

func shouldUseCursorByCost(keyCount int, duplicateRatio float64, opts ReadCostModelOptions) bool {
	point, cursor := estimateReadCosts(keyCount, duplicateRatio, opts)
	return cursor < point
}
