package udb

import "time"

// BatchPolicy selects the target number of requests for the next transaction.
// Implementations should be cheap and side-effect free.
type BatchPolicy interface {
	NextBatchSize(queueDepth, queueCapacity, currentBatch int, stats PipelineStats) int
}

// FixedBatchPolicy keeps a stable target batch size. It is useful for tests and
// for applications that already know their workload shape.
type FixedBatchPolicy struct{ Size int }

func (p FixedBatchPolicy) NextBatchSize(_, _, _ int, _ PipelineStats) int {
	if p.Size < 1 {
		return 1
	}
	return p.Size
}

// AdaptiveBatchPolicy uses queue pressure and recent commit latency as a small
// feedback controller. It never exceeds MaxBatchSize and never goes below
// MinBatchSize. The policy changes conservatively to avoid oscillation.
type AdaptiveBatchPolicy struct {
	MinBatchSize    int
	MaxBatchSize    int
	TargetLatency   time.Duration
	ScaleUpFactor   float64
	ScaleDownFactor float64
}

func DefaultAdaptiveBatchPolicy() AdaptiveBatchPolicy {
	return AdaptiveBatchPolicy{MinBatchSize: 25, MaxBatchSize: 1000, TargetLatency: 25 * time.Millisecond, ScaleUpFactor: 2, ScaleDownFactor: 0.5}
}

func (p AdaptiveBatchPolicy) NextBatchSize(queueDepth, queueCapacity, currentBatch int, stats PipelineStats) int {
	min, max := p.MinBatchSize, p.MaxBatchSize
	if min < 1 {
		min = 1
	}
	if max < min {
		max = min
	}
	cur := currentBatch
	if cur < min {
		cur = min
	}
	if cur > max {
		cur = max
	}
	pressure := 0.0
	if queueCapacity > 0 {
		pressure = float64(queueDepth) / float64(queueCapacity)
	}
	up, down := p.ScaleUpFactor, p.ScaleDownFactor
	if up <= 1 {
		up = 2
	}
	if down <= 0 || down >= 1 {
		down = 0.5
	}
	if pressure >= 0.50 || (stats.AvgCommitLatency > 0 && p.TargetLatency > 0 && stats.AvgCommitLatency < p.TargetLatency/2) {
		cur = int(float64(cur) * up)
	} else if pressure <= 0.05 && stats.AvgCommitLatency > p.TargetLatency && stats.AvgCommitLatency > 0 {
		cur = int(float64(cur) * down)
	}
	if cur < min {
		cur = min
	}
	if cur > max {
		cur = max
	}
	return cur
}
