package pkg

import (
	"context"
	"time"

	"golang.org/x/exp/slog"
)

// Watchinator is used to periodically poll GitHub for new GitHubItems that should be subscribed to. It uses a
// Configinator to dynamically reload watches based on config changes.
type Watchinator interface {
	// Watch is the 'main' function of the Watchinator, which sets up both a Pollinator and a Configinator to
	// watch GitHub for new items and subscribe to them as needed.
	Watch(ctx context.Context, configFilePath string) error
}

// watchinator is the internal implementation of the Watchinator interface.
type watchinator struct {
	gitHubinator GitHubinator
	logger       *slog.Logger
	pollinator   Pollinator
	configinator Configinator
	emailinator  Emailinator
}

// getPollCallback returns a function that executes on each tick in the poller for a Watch. It lists items from GitHub
// using the given GitHubinator, and subscribes to them if the viewer is not already subscribed. Errors are logged.
func (w *watchinator) getPollCallback(
	ctx context.Context, gh GitHubinator, e Emailinator, watch *Watch,
) func(t time.Time) {
	filter := watch.GetIssueFilter()
	matchinator := watch.GetMatchinator()
	actioninator := watch.GetActioninator(gh, e)

	MetricPollTickTotal.WithLabelValues(watch.Name).Inc()

	errorMetric := MetricPollErrorTotal.WithLabelValues(watch.Name)

	return func(t time.Time) {
		logger := w.logger.With("time", t, "watch", watch.Name)

		for _, r := range watch.Repositories {
			repoLogger := logger.With("repo", r)
			repoLogger.Info("updating repo")

			issues, err := gh.ListIssues(ctx, r, filter, matchinator)
			if err != nil {
				repoLogger.Error("unable to list issues from GitHub", LogKeyError, err)

				errorMetric.Inc()

				continue
			}

			for _, i := range issues {
				issueLogger := repoLogger.With(
					"issue",
					slog.GroupValue(
						slog.Int("number", i.Number),
						slog.String("title", i.Title),
					),
				)

				if err := actioninator.Handle(ctx, *i, issueLogger); err != nil {
					issueLogger.Error("unable to handle issue", LogKeyError, err)

					errorMetric.Inc()
				}
			}
		}
	}
}

// getConfigCallback returns a function that is executed whenever a config change is detected. It ensures the currently
// running polls in the pollinator match the watches in the config.
func (w *watchinator) getConfigCallback(ctx context.Context) func(c *Config) {
	return func(c *Config) {
		gh := w.gitHubinator.WithToken(c.PAT)
		e := w.emailinator.WithConfig(&c.Email)

		for _, p := range w.pollinator.List() {
			needsToBeDeleted := true

			for _, w := range c.Watches {
				if p == w.Name {
					needsToBeDeleted = false

					break
				}
			}

			if needsToBeDeleted {
				w.pollinator.Delete(p)
			}
		}

		for _, watch := range c.Watches {
			w.pollinator.Add(watch.Name, c.Interval, w.getPollCallback(ctx, gh, e, watch), true)
		}
	}
}

func (w *watchinator) Watch(ctx context.Context, configFilePath string) error {
	return w.configinator.Watch(
		ctx, configFilePath, w.getConfigCallback(ctx), w.gitHubinator, w.emailinator,
	)
}

// NewWatchinator creates a new Watchinator.
func NewWatchinator(
	logger *slog.Logger,
	gitHubinator GitHubinator,
	pollinator Pollinator,
	configinator Configinator,
	emailinator Emailinator,
) Watchinator {
	return &watchinator{
		logger:       logger,
		gitHubinator: gitHubinator,
		pollinator:   pollinator,
		configinator: configinator,
		emailinator:  emailinator,
	}
}
