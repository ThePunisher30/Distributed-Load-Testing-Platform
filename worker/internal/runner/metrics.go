package runner

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// Counter: total requests, sliced by the labels chosen in Step 2. Every VU
	// goroutine calls .Inc() on this concurrently; the client makes that safe by
	// incrementing atomically under the hood, so no lock is needed here.
	requestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "loadtest_requests_total",
			Help: "Total load-test requests sent.",
		},
		[]string{"status_class", "method"},
	)

	// Histogram: request duration in seconds, sliced by method. .Observe() is also
	// concurrency-safe. The bucket boundaries become the auto-generated "le" label.
	requestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "loadtest_request_duration_seconds",
			Help:    "Load-test request duration in seconds.",
			Buckets: prometheus.DefBuckets, // default buckets for now; we can tune later
		},
		[]string{"method"},
	)

	activeVUs = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "loadtest_active_vus", // ← your gauge name from Step 2
			Help: "Virtual users currently running load on this worker.",
		},
	)
)
