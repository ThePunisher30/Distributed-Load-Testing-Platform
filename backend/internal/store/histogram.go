package store

import "math"

// latencyBoundariesMs MUST stay identical to the worker runner's boundaries
// (worker/internal/runner/histogram.go). A shard reports its histogram as bucket
// counts indexed by these bounds; the backend merges those counts and computes the
// run's percentiles here, so both sides must agree on what each bucket means. The
// backend is a separate module and cannot import the runner, hence the duplication.
var latencyBoundariesMs = []float64{1, 2, 5, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000}

// mergeBuckets sums shard histograms element-wise into one combined histogram.
// This is why shards report bucket COUNTS, not percentiles: counts add exactly,
// percentiles cannot be averaged. A nil/short shard array contributes nothing.
func mergeBuckets(shards [][]int64) []int64 {
	merged := make([]int64, len(latencyBoundariesMs)+1)
	for _, b := range shards {
		for i := 0; i < len(b) && i < len(merged); i++ {
			merged[i] += b[i]
		}
	}
	return merged
}

// percentile returns the latency (ms) at percentile p (0..1) from histogram bucket
// counts: the upper bound of the bucket holding the ceil(p*total)-th sample, or 0
// when there are no samples. Mirrors the runner's percentile.
func percentile(buckets []int64, p float64) float64 {
	var total int64
	for _, count := range buckets {
		total += count
	}
	if total == 0 {
		return 0
	}
	threshold := int64(math.Ceil(p * float64(total)))
	var cumulative int64
	for i, count := range buckets {
		cumulative += count
		if cumulative >= threshold {
			if i < len(latencyBoundariesMs) {
				return latencyBoundariesMs[i]
			}
			return latencyBoundariesMs[len(latencyBoundariesMs)-1]
		}
	}
	return latencyBoundariesMs[len(latencyBoundariesMs)-1]
}
