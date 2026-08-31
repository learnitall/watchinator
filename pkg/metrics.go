package pkg

import (
	"context"
	"errors"
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
			Help: "The total number of new items on GitHub the user has been subscribed to",
		},
	)
	MetricNewSubscriptionErrorTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "watchinator_new_subscription_error_total",
			Help: "The total number of errors observed when subscribing to new items on GitHub",
		},
	)
	MetricFilteredTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "watchinator_filtered_items_total",
			Help: "The total number of items on GitHub that were filtered out for a match",
		},
	)
	MetricConfigLoadTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "watchinator_config_load_total",
			Help: "The total number of times the configuration has been loaded",
		},
	)
	MetricConfigLoadErrorTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "watchinator_config_load_error_total",
			Help: "The total number of errors observed when loading configurations",
		},
	)
	MetricPollTickTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "watchinator_poll_tick_total",
			Help: "The total number of times an update poll has ticked",
		},
		[]string{"watch"},
	)
	MetricPollErrorTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "watchinator_poll_error_total",
			Help: "The total number of errors that have occurred during a poll tick",
		},
		[]string{"watch"},
	)
	MetricRepoQueryTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "watchinator_repo_query_total",
			Help: "The total number of repo queries that have been made against GitHub",
		},
	)
	MetricRepoQueryErrorTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "watchinator_repo_query_error_total",
			Help: "The total number of errors observed during repo queries against GitHub",
		},
	)
	MetricOrgQueryTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "watchinator_org_query_total",
			Help: "The total number of organization queries that have been made against GitHub",
		},
	)
	MetricOrgQueryErrorTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "watchinator_org_query_error_total",
			Help: "The total number of errors observed during organization queries against GitHub",
		},
	)
	MetricSearchQueryTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "watchinator_search_query_total",
			Help: "The total number of search queries that have been made against GitHub",
		},
	)
	MetricSearchQueryErrorTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "watchinator_search_query_error_total",
			Help: "The total number of errors observed during search queries against GitHub",
		},
	)
	MetricActionHandleTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "watchinator_action_handle_total",
			Help: "The total number of times an action handler performed an action, labeled by action name",
		}, []string{"action"},
	)
	MetricActionHandleErrorTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "watchinator_action_handle_error_total",
			Help: "The total number of times an error occurred during an action handler execution",
		}, []string{"action"},
	)
)

// ServePromEndpoint creates a new http server which serves prometheus metrics at :2112/metrics.
func ServePromEndpoint(ctx context.Context) {
	http.Handle("/metrics", promhttp.Handler())

	logger := NewLogger()

	timeout := 3 * time.Second
	server := &http.Server{
		Addr:              ":2112",
		ReadTimeout:       timeout,
		ReadHeaderTimeout: timeout,
		WriteTimeout:      timeout,
	}

	go func() {
		for {
			logger.Info("starting prom metric endpoint")

			if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				logger.Error("error observed when serving prom metric endpoint", LogKeyError, err)

				select {
				case <-ctx.Done():
					return
				case <-time.NewTimer(timeout).C:
					continue
				}
			}

			return
		}
	}()

	<-ctx.Done()
	logger.Info("stopping prom metric endpoint")

	if err := server.Close(); err != nil {
		logger.Error("error observed when closing prom metric endpoint", LogKeyError, err)
	}
}
