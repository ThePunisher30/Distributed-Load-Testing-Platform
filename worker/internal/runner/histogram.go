package runner

import "math"

// latencyBoundariesMs are the upper bounds (in milliseconds) of the latency
// histogram buckets. They are log-spaced: fine resolution where most requests
// live (a few ms) and coarse resolution out in the slow tail. A sample belongs to
// the first bucket whose bound it is <= ; a sample larger than the last bound
// falls into the extra overflow bucket.
var latencyBoundariesMs = [...]float64{1, 2, 5, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000}

// numLatencyBuckets is the number of histogram slots: one per boundary, plus one
// overflow slot for samples beyond the last boundary. len() of an array is a
// compile-time constant, so this is a valid const (and sizes the vuStats array).
const numLatencyBuckets = len(latencyBoundariesMs) + 1

// ---- You write bucketIndex and percentile below (see the task notes). ----
func bucketIndex(latencyMs float64) int {
	for i, bound := range latencyBoundariesMs {
		if latencyMs <= bound {
			return i
		}
	}
	return numLatencyBuckets - 1 // overflow bucket
}

// percentile returns the latency (ms) at percentile p (0..1) from histogram bucket
// counts: the upper bound of the bucket where the p-th sample falls. Approximate
// to the bucket width; returns 0 when there are no samples.
func percentile(buckets []int64, p float64) float64 {
	var total int64
	for _, count := range buckets {
		total += count
	}
	if total == 0 {
		return 0 // no samples
	}
	// Nearest-rank: the p-th percentile is the bucket holding the ceil(p*total)-th
	// sample. Ceil (not truncation) keeps threshold >= 1, so empty leading buckets
	// never satisfy a zero threshold.
	threshold := int64(math.Ceil(p * float64(total)))
	var cumulative int64
	for i, count := range buckets {
		cumulative += count
		if cumulative >= threshold {
			if i < len(latencyBoundariesMs) {
				return latencyBoundariesMs[i]
			}
			return latencyBoundariesMs[len(latencyBoundariesMs)-1] // overflow bucket
		}
	}
	return latencyBoundariesMs[len(latencyBoundariesMs)-1]
}
