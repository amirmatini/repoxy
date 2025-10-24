package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	CacheHits = promauto.NewCounter(prometheus.CounterOpts{
		Name: "edgecache_cache_hits_total",
		Help: "Total number of cache hits",
	})

	CacheMisses = promauto.NewCounter(prometheus.CounterOpts{
		Name: "edgecache_cache_misses_total",
		Help: "Total number of cache misses",
	})

	CacheBypasses = promauto.NewCounter(prometheus.CounterOpts{
		Name: "edgecache_cache_bypasses_total",
		Help: "Total number of cache bypasses",
	})

	CacheSize = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "edgecache_cache_size_bytes",
		Help: "Current cache size in bytes",
	})

	CacheEntries = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "edgecache_cache_entries",
		Help: "Current number of cache entries",
	})

	RequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "edgecache_request_duration_seconds",
		Help:    "Request duration in seconds",
		Buckets: prometheus.DefBuckets,
	}, []string{"repo", "status"})

	UpstreamDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "edgecache_upstream_duration_seconds",
		Help:    "Upstream request duration in seconds",
		Buckets: prometheus.DefBuckets,
	}, []string{"repo"})

	EvictedEntries = promauto.NewCounter(prometheus.CounterOpts{
		Name: "edgecache_evicted_entries_total",
		Help: "Total number of evicted entries",
	})

	EvictedBytes = promauto.NewCounter(prometheus.CounterOpts{
		Name: "edgecache_evicted_bytes_total",
		Help: "Total bytes evicted",
	})
)
