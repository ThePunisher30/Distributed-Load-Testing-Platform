package runner

import "testing"

// TestMergeStats_Mix exercises both record and mergeStats on a normal mix of
// successes, failures, and a transport error, across two VUs.
func TestMergeStats_Mix(t *testing.T) {
	var vu1 vuStats
	vu1.record(10, true, true)  // success, responded
	vu1.record(20, true, true)  // success, responded
	vu1.record(0, false, false) // transport error: no response, no latency

	var vu2 vuStats
	vu2.record(5, true, true)   // success, responded
	vu2.record(30, false, true) // a 500: failed, but it DID respond (has latency)

	got := mergeStats([]vuStats{vu1, vu2})

	want := Result{
		TotalRequests:      5,
		SuccessfulRequests: 3,
		FailedRequests:     2,
		AvgLatencyMs:       16.25, // (10+20+5+30) / 4
		MinLatencyMs:       5,
		MaxLatencyMs:       30,
	}

	if got != want {
		t.Errorf("mismatch:\n got  %+v\n want %+v", got, want)
	}
}

// TestMergeStats_ZeroSampleVU proves a VU that recorded no latency samples does
// NOT drag the global min down to 0. This is the bug the latencyCount>0 guard
// prevents.
func TestMergeStats_ZeroSampleVU(t *testing.T) {
	var vu1 vuStats
	vu1.record(5, true, true) // the only real sample

	var vu2 vuStats
	vu2.record(0, false, false) // transport error: min/max stay 0
	vu2.record(0, false, false) // transport error

	got := mergeStats([]vuStats{vu1, vu2})

	want := Result{
		TotalRequests:      3,
		SuccessfulRequests: 1,
		FailedRequests:     2,
		AvgLatencyMs:       5,
		MinLatencyMs:       5, // NOT 0 — vu2 contributed no samples
		MaxLatencyMs:       5,
	}

	if got != want {
		t.Errorf("mismatch:\n got  %+v\n want %+v", got, want)
	}
}

// TestMergeStats_AllFailures proves the divide-by-zero guard: when no request
// ever got a response, avg/min/max are 0 and nothing panics.
func TestMergeStats_AllFailures(t *testing.T) {
	var vu1 vuStats
	vu1.record(0, false, false) // transport error, no samples at all

	got := mergeStats([]vuStats{vu1})

	want := Result{
		TotalRequests:      1,
		SuccessfulRequests: 0,
		FailedRequests:     1,
		AvgLatencyMs:       0,
		MinLatencyMs:       0,
		MaxLatencyMs:       0,
	}

	if got != want {
		t.Errorf("mismatch:\n got  %+v\n want %+v", got, want)
	}
}
