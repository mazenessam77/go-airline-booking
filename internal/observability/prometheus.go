package observability

import (
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Prometheus holds the metrics registry and HTTP instruments. Labels are limited
// to method, route pattern and status so series stay bounded.
type Prometheus struct {
	registry *prometheus.Registry
	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
	inFlight prometheus.Gauge
}

func NewPrometheus() *Prometheus {
	p := &Prometheus{
		registry: prometheus.NewRegistry(),
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "HTTP requests handled, by method, route pattern and status code.",
		}, []string{"method", "route", "status"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP request latency, by method, route pattern and status code.",
			Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
		}, []string{"method", "route", "status"}),
		inFlight: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "http_requests_in_flight",
			Help: "HTTP requests currently being served.",
		}),
	}
	p.registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		p.requests, p.duration, p.inFlight,
	)
	return p
}

// RegisterPool exports pgxpool statistics, read from pool.Stat() at scrape time.
func (p *Prometheus) RegisterPool(pool *pgxpool.Pool) {
	p.registry.MustRegister(poolCollector{pool})
}

// Handler serves the registry in the Prometheus exposition format.
func (p *Prometheus) Handler() http.Handler {
	return promhttp.HandlerFor(p.registry, promhttp.HandlerOpts{MaxRequestsInFlight: 4})
}

// Middleware records request metrics. route maps a request to a bounded label,
// normally the matched ServeMux pattern.
func (p *Prometheus) Middleware(next http.Handler, route func(*http.Request) string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		p.inFlight.Inc()
		defer p.inFlight.Dec()
		writer := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(writer, r)
		if writer.status == 0 {
			writer.status = http.StatusOK
		}
		labels := prometheus.Labels{"method": method(r.Method), "route": route(r), "status": strconv.Itoa(writer.status)}
		p.requests.With(labels).Inc()
		p.duration.With(labels).Observe(time.Since(start).Seconds())
	})
}

func method(m string) string {
	switch m {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions:
		return m
	}
	return "OTHER"
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *statusWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}
func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

var (
	poolTotal        = prometheus.NewDesc("db_pool_total_connections", "Connections currently in the pool (idle, acquired and constructing).", nil, nil)
	poolIdle         = prometheus.NewDesc("db_pool_idle_connections", "Idle connections in the pool.", nil, nil)
	poolAcquired     = prometheus.NewDesc("db_pool_acquired_connections", "Connections currently checked out of the pool.", nil, nil)
	poolConstructing = prometheus.NewDesc("db_pool_constructing_connections", "Connections currently being established.", nil, nil)
	poolMax          = prometheus.NewDesc("db_pool_max_connections", "Configured maximum pool size.", nil, nil)
	poolAcquires     = prometheus.NewDesc("db_pool_acquires_total", "Successful connection acquires.", nil, nil)
	poolEmpty        = prometheus.NewDesc("db_pool_empty_acquires_total", "Acquires that had to wait because no idle connection was available.", nil, nil)
	poolCanceled     = prometheus.NewDesc("db_pool_canceled_acquires_total", "Acquires canceled by their context.", nil, nil)
	poolWait         = prometheus.NewDesc("db_pool_acquire_duration_seconds_total", "Cumulative time spent acquiring connections.", nil, nil)
)

type poolCollector struct{ pool *pgxpool.Pool }

func (poolCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range []*prometheus.Desc{poolTotal, poolIdle, poolAcquired, poolConstructing, poolMax, poolAcquires, poolEmpty, poolCanceled, poolWait} {
		ch <- d
	}
}

func (c poolCollector) Collect(ch chan<- prometheus.Metric) {
	s := c.pool.Stat()
	ch <- prometheus.MustNewConstMetric(poolTotal, prometheus.GaugeValue, float64(s.TotalConns()))
	ch <- prometheus.MustNewConstMetric(poolIdle, prometheus.GaugeValue, float64(s.IdleConns()))
	ch <- prometheus.MustNewConstMetric(poolAcquired, prometheus.GaugeValue, float64(s.AcquiredConns()))
	ch <- prometheus.MustNewConstMetric(poolConstructing, prometheus.GaugeValue, float64(s.ConstructingConns()))
	ch <- prometheus.MustNewConstMetric(poolMax, prometheus.GaugeValue, float64(s.MaxConns()))
	ch <- prometheus.MustNewConstMetric(poolAcquires, prometheus.CounterValue, float64(s.AcquireCount()))
	ch <- prometheus.MustNewConstMetric(poolEmpty, prometheus.CounterValue, float64(s.EmptyAcquireCount()))
	ch <- prometheus.MustNewConstMetric(poolCanceled, prometheus.CounterValue, float64(s.CanceledAcquireCount()))
	ch <- prometheus.MustNewConstMetric(poolWait, prometheus.CounterValue, s.AcquireDuration().Seconds())
}
