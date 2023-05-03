package pkg

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	MetricNewSubscriptionTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "watchinator_new_subscription_total",
			Help: "The total number of new things on GitHub the user has been subscribed to",
		},
	)
	MetricConfigReloadTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "watchinator_config_reload_total",
			Help: "The total number of times the configuration has been reloaded",
		},
	)
	MetricPollTickTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "watchinator_poll_tick_total",
			Help: "The total number of times an update poll has ticked",
		},
	)
)

// ServePromEndpoint creates a new http server which serves prometheus metrics at :2112/metrics.
func ServePromEndpoint() error {
	http.Handle("/metrics", promhttp.Handler())

	timeout := 3 * time.Second
	server := &http.Server{
		Addr:              ":2112",
		ReadTimeout:       timeout,
		ReadHeaderTimeout: timeout,
		WriteTimeout:      timeout,
	}

	return server.ListenAndServe()
}
