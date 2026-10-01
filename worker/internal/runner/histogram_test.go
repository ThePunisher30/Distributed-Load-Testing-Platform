package runner

import "testing"

// Boundary index reference for latencyBoundariesMs:
//   0:<=1  1:<=2  2:<=5  3:<=10  4:<=25  5:<=50  6:<=100
//   7:<=250  8:<=500  9:<=1000  10:<=2500  11:<=5000  12:overflow

func TestBucketIndex(t *testing.T) {
	cases := []struct {
		latency float64
		want    int
	}{
		{0.4, 0},   // <=1
		{1, 0},     // boundary is inclusive
		{3, 2},     // <=5
		{5, 2},     // <=5 (inclusive)
		{9, 3},     // <=10
		{1800, 10}, // <=2500
		{9000, 12}, // overflow
	}
	for _, c := range cases {
		if got := bucketIndex(c.latency); got != c.want {
			t.Errorf("bucketIndex(%v) = %d, want %d", c.latency, got, c.want)
		}
	}
}

func TestPercentile(t *testing.T) {
	// 95 requests at ~5ms (bucket 2), 5 slow ones at ~1800ms (bucket 10).
	// The average would be ~95ms (misleading); percentiles tell the real story.
	buckets := make([]int64, numLatencyBuckets)
	buckets[2] = 95
	buckets[10] = 5

	if got := percentile(buckets, 0.50); got != 5 {
		t.Errorf("p50 = %v, want 5 (typical request is fast)", got)
	}
	if got := percentile(buckets, 0.95); got != 5 {
		t.Errorf("p95 = %v, want 5 (95%% still in the fast bucket)", got)
	}
	if got := percentile(buckets, 0.99); got != 2500 {
		t.Errorf("p99 = %v, want 2500 (the tail the average hid)", got)
	}
}

func TestPercentileEmpty(t *testing.T) {
	if got := percentile(make([]int64, numLatencyBuckets), 0.95); got != 0 {
		t.Errorf("percentile of no samples = %v, want 0", got)
	}
}
