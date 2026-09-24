package runner

// vuStats is one virtual user's private tally of results. During a run, each
// virtual user (a goroutine) touches ONLY its own vuStats — nothing is shared —
// so no locks are needed. The separate tallies are combined once at the end.
type vuStats struct {
	total        int64   // every request attempted
	success      int64   // requests that returned a status < 400
	failed       int64   // transport errors, or a status >= 400
	latencyCount int64   // how many requests produced a latency sample
	sumLatencyMs float64 // running total of latencies (used for the average)
	minLatencyMs float64 // fastest sample seen so far
	maxLatencyMs float64 // slowest sample seen so far
}

// record folds a single request's outcome into this VU's tally.
//
//	latencyMs   - how long the request took, in milliseconds
//	success     - true if we got a response with status < 400
//	gotResponse - true if the server responded at all (a 500 IS a response;
//	              a connection timeout is NOT). Only responses have a latency.
func (s *vuStats) record(latencyMs float64, success bool, gotResponse bool) {
	s.total++
	if success {
		s.success++
	} else {
		s.failed++
	}
	if gotResponse {
		s.sumLatencyMs += latencyMs
		// On the first sample (latencyCount == 0) min/max are still zero-valued,
		// so seed them with this sample rather than comparing against 0.
		if s.latencyCount == 0 || latencyMs < s.minLatencyMs {
			s.minLatencyMs = latencyMs
		}
		if s.latencyCount == 0 || latencyMs > s.maxLatencyMs {
			s.maxLatencyMs = latencyMs
		}
		s.latencyCount++
	}
}

// mergeStats combines every VU's private tally into the final Result.
func mergeStats(all []vuStats) Result {
	var total int64
	var successful int64
	var failed int64
	var latencyCount int64
	var sumLatency float64
	var minMs float64
	var maxMs float64
	var avg float64
	for _, vu := range all {
		total += vu.total
		successful += vu.success
		failed += vu.failed
		sumLatency += vu.sumLatencyMs
		if vu.latencyCount > 0 {
			if latencyCount == 0 || vu.minLatencyMs < minMs {
				minMs = vu.minLatencyMs
			}
			if latencyCount == 0 || vu.maxLatencyMs > maxMs {
				maxMs = vu.maxLatencyMs
			}
		}
		latencyCount += vu.latencyCount
	}
	if latencyCount > 0 {
		avg = sumLatency / float64(latencyCount)
	}
	return Result{
		TotalRequests:      total,
		SuccessfulRequests: successful,
		FailedRequests:     failed,
		LatencyCount:       latencyCount,
		AvgLatencyMs:       avg,
		MinLatencyMs:       minMs,
		MaxLatencyMs:       maxMs,
	}
}
