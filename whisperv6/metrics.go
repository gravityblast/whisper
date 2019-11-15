package whisperv6

import prom "github.com/prometheus/client_golang/prometheus"

var (
	envelopesCounter = prom.NewCounter(prom.CounterOpts{
		Name: "whisper_envelopes_total",
		Help: "Number of envelopes processed.",
	})
	envelopesCacheFailedCounter = prom.NewCounterVec(prom.CounterOpts{
		Name: "whisper_envelopes_cache_failures_total",
		Help: "Number of envelopes which failed to be cached.",
	}, []string{"type"})
	envelopesCachedCounter = prom.NewCounterVec(prom.CounterOpts{
		Name: "whisper_envelopes_cached_total",
		Help: "Number of envelopes cached.",
	}, []string{"cache"})
	envelopesSizeMeter = prom.NewHistogram(prom.HistogramOpts{
		Name:    "whisper_envelopes_size_bytes",
		Help:    "Size of processed Whisper envelopes in bytes.",
		Buckets: prom.ExponentialBuckets(1024, 4, 10),
	})
	// rate limiter metrics
	rateLimitsProcessed = prom.NewCounter(prom.CounterOpts{
		Name: "whisper_rate_limits_processed_total",
		Help: "Number of packets whisper rate limiter processed.",
	})
	rateLimitsExceeded = prom.NewCounterVec(prom.CounterOpts{
		Name: "whisper_rate_limits_exceeded_total",
		Help: "Number of times the whisper rate limits were exceeded",
	}, []string{"type"})
)

func init() {
	prom.MustRegister(envelopesCounter)
	prom.MustRegister(envelopesCacheFailedCounter)
	prom.MustRegister(envelopesCachedCounter)
	prom.MustRegister(envelopesSizeMeter)
	prom.MustRegister(rateLimitsProcessed)
	prom.MustRegister(rateLimitsExceeded)
}
