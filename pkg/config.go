package pkg

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/shurcooL/githubv4"
	"golang.org/x/exp/slog"
	"gopkg.in/yaml.v3"
	"k8s.io/apimachinery/pkg/labels"
)

// Config specifies the list of Watches to create.
type Config struct {
	// GitHub username watch will apply to.
	User string `yaml:"user"`
	// GitHub PAT used for authentication.
	PAT string `yaml:"pat"`
	// Interval used to determine when to update watches.
	Interval time.Duration `yaml:"interval"`
	// Watches is a list of Watch definitions.
	Watches []*Watch `yaml:"watches"`
}

func (c *Config) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("user", c.User),
		slog.String("pat", "redeacted"),
		slog.Duration("interval", c.Interval),
		slog.Any("watches", c.Watches),
	)
}

// Validate ensures that the Config struct is populated correctly. If a field is not properly set, an error is
// returned explaining why.
func (c *Config) Validate(ctx context.Context, gh GitHubinator) error {
	if len(c.User) == 0 {
		return errors.New("user cannot be empty")
	}

	if len(c.PAT) == 0 {
		return errors.New("pat cannot be empty")
	}

	if len(c.Watches) == 0 {
		return errors.New("expected at least one watch")
	}

	if c.Interval <= 0 {
		return fmt.Errorf("interval must be greater than zero '%s'", c.Interval)
	}

	user, err := gh.WithToken(c.PAT).WhoAmI(ctx)
	if err != nil {
		return fmt.Errorf("unable to validate pat: %w", err)
	}

	if user != c.User {
		return fmt.Errorf("configured user '%s' does not match PAT user '%s'", user, c.User)
	}

	for _, w := range c.Watches {
		if err := w.ValidateAndPopulate(ctx, gh); err != nil {
			return fmt.Errorf("unable to validate watch %+v: %w", w, err)
		}
	}

	return nil
}

// GetWatch returns a pointer to the Watch with the given name. If the Watch is not present in the config, nil
// is returned.
func (c *Config) GetWatch(name string) *Watch {
	for _, w := range c.Watches {
		if w.Name == name {
			return w
		}
	}

	return nil
}

// NewConfigFromFile opens the given path and attempts to unmarshal it into a Config struct. Config.Validate is not
// called and still needs to be executed by the user.
func NewConfigFromFile(path string) (*Config, error) {
	configBody, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	c := &Config{}

	err = yaml.Unmarshal(configBody, c)
	if err != nil {
		return nil, err
	}

	return c, nil
}

// NewTestConfig creates a new Config struct with pre-populated fields. It can be used in unit tests.
func NewTestConfig() *Config {
	return &Config{
		User:     "user",
		PAT:      "1234",
		Interval: time.Hour * 1,
		Watches:  []*Watch{NewTestWatch()},
	}
}

// Watch specifies which items in GitHub a user will be subscribed to. This struct uses private fields to hold
// values that are unmarshalled from a YAML config, therefore it cannot be marshalled back into bytes.
// This is to allow ValidateAndPopulate to perform type conversions from unmarshalled fields into exported fields.
type Watch struct {
	// Name is a human-readable description of the watch.
	Name string `yaml:"name"`
	// Repositories to watch issues from.
	Repositories []GitHubRepository `yaml:"repos"`
	// Selectors are used to specify which items to watch, follows the k8s label selector syntax.
	// See the GitHubItem struct for valid keys and fields and
	// https://pkg.go.dev/k8s.io/apimachinery@v0.27.1/pkg/labels#Parse for the syntax.
	Selectors []labels.Selector `yaml:"-"`
	selectors []string          `yaml:"selectors"`
	// RequiredLabels are a list of labels that must be present for an item to be watched. An item must have all of
	// these labels to be watched.
	RequiredLabels []string `yaml:"requiredLabels"`
	// SearchLabels are a set of labels that will be used to find new items. They will not be used as criteria for if
	// an item is watched, but if an item is discovered from GitHub.
	SearchLabels []string `yaml:"searchLabels"`
	// BodyRegex is a list of regex expressions which must match the item's body.
	BodyRegex []*regexp.Regexp `yaml:"-"`
	bodyRegex []string         `yaml:"bodyRegex"`
	// States are a list of issues states to filter by.
	States []string `yaml:"states"`
}

func (w *Watch) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("name", w.Name),
		slog.Any("repos", w.Repositories),
		slog.Any("selectors", w.selectors),
		slog.Any("requiredLabels", w.RequiredLabels),
		slog.Any("searchLabels", w.SearchLabels),
		slog.Any("bodyRegex", w.bodyRegex),
		slog.Any("states", w.States),
	)
}

// ValidateAndPopulate ensures that the Watch struct has its fields properly set and populates fields as necessary
// when the struct was unmarshalled from a YAML config. For instance, the field BodyRegex has an associated
// unexported field bodyRegex of the type []string, which is populated during unmarshalling. After calling
// ValidateAndPopulate, the field BodyRegex of the type []*regex.Regexp will be populated with the parsed regex strings
// found in the bodyRegex field.
func (w *Watch) ValidateAndPopulate(ctx context.Context, gh GitHubinator) error {
	if len(w.Name) == 0 {
		return fmt.Errorf("name cannot be empty")
	}

	if len(w.Repositories) == 0 {
		return fmt.Errorf("expected at least one repository")
	}

	if len(w.selectors) == 0 && len(w.bodyRegex) == 0 && len(w.RequiredLabels) == 0 && len(w.States) == 0 {
		return fmt.Errorf("expected at least one filter type")
	}

	for _, r := range w.Repositories {
		if err := gh.CheckRepository(ctx, r); err != nil {
			return fmt.Errorf("unable to validate repository %+v: %w", r, err)
		}
	}

	for _, s := range w.selectors {
		parsed, err := labels.Parse(s)
		if err != nil {
			return fmt.Errorf("unable to parse label selector %+v: %w", s, err)
		}

		w.Selectors = append(w.Selectors, parsed)
	}

	// Do this double loop here so we can test how we handle w.Selectors without needing to also set w.selectors.
	for _, s := range w.Selectors {
		requirements, selectable := s.Requirements()
		if !selectable {
			return errors.New("got an empty selector")
		}

		for _, r := range requirements {
			if key := r.Key(); !isGitHubItemField(key) {
				return fmt.Errorf("unknown key '%s' in selector", key)
			}
		}
	}

	for _, r := range w.bodyRegex {
		compiled, err := regexp.Compile(r)
		if err != nil {
			return fmt.Errorf("unable to compile regex '%s', %w'", r, err)
		}

		w.BodyRegex = append(w.BodyRegex, compiled)
	}

	for _, s := range w.States {
		switch githubv4.IssueState(s) {
		case githubv4.IssueStateClosed, githubv4.IssueStateOpen:
			continue
		default:
			return fmt.Errorf("unknown issue state %s", s)
		}
	}

	return nil
}

// GetIssueFilter returns a GitHubIssueFilter based on the Watch's specified SearchLabels and States. It can
// be passed to a GitHubinator for listing issues that match the Watch.
func (w *Watch) GetIssueFilter() *GitHubIssueFilter {
	return &GitHubIssueFilter{
		Labels: w.SearchLabels,
		States: w.States,
	}
}

// GetMatchinator returns a Matchinator based on the Watch's specified BodyRegex, Selectors, and RequiredLabels fields.
// It can be passed to a GitHubinator for listing issues that match the Watch.
func (w *Watch) GetMatchinator() Matchinator {
	return NewMatchinator().
		WithBodyRegexes(w.BodyRegex...).
		WithSelectors(w.Selectors...).
		WithRequiredLabels(w.RequiredLabels...)
}

// NewTestWatch creates a new Watch instance with pre-populated fields. It can be used in unit tests.
func NewTestWatch() *Watch {
	return &Watch{
		Name: "name",
		Repositories: []GitHubRepository{{
			Owner: "owner",
			Name:  "repo",
		}},
		selectors:      []string{"type==issue"},
		RequiredLabels: []string{"a/requiredLabel"},
		SearchLabels:   []string{"a/searchLabel"},
		bodyRegex:      []string{".*"},
		States:         []string{"OPEN"},
	}
}

// Configinator handles loading a config from disk.
type Configinator interface {
	// Watch will continually watch the given config path for changes.
	// If a change occurs, the new config will be validated and sent to the given callback.
	// If an error occurs, the watch stops and the error is returned.
	Watch(ctx context.Context, path string, callback func(*Config), gh GitHubinator) error
}

// NewConfiginator creates a new Configinator instance based on the packages internal implementation.
func NewConfiginator(logger *slog.Logger) Configinator {
	c := &configinator{logger: logger}

	return c
}

// configinator implements the Configinator interface.
type configinator struct {
	logger *slog.Logger
}

// setupWatcher creates a new fsnotify.Watcher to watch for changes to the given path.
func (c *configinator) setupWatcher(path string) (*fsnotify.Watcher, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	err = w.Add(path)
	if err != nil {
		return nil, err
	}

	return w, nil
}

// loadConfig attempts to unmarshal and validate the config at the given path.
func (c *configinator) loadConfig(ctx context.Context, gh GitHubinator, path string) (*Config, error) {
	MetricConfigLoadTotal.Inc()

	config, err := NewConfigFromFile(path)
	if err != nil {
		MetricConfigLoadErrorTotal.Inc()

		return nil, fmt.Errorf("err loading config: %w", err)
	}

	err = config.Validate(ctx, gh)
	if err != nil {
		MetricConfigLoadErrorTotal.Inc()

		return nil, fmt.Errorf("unable to validate config: %w", err)
	}

	return config, nil
}

// Watch will continually watch for new changes to the Config located at the given path. If a change occurs,
// the Config will be unmarshalled, validated, and passed to the given callback.
func (c *configinator) Watch(ctx context.Context, path string, callback func(*Config), gh GitHubinator) error {
	w, err := c.setupWatcher(path)
	if err != nil {
		return fmt.Errorf("unable to setup fsnotify watcher for '%s': %w", path, err)
	}
	defer w.Close()

	c.logger.Debug("attempting initial load of config file")

	config, err := c.loadConfig(ctx, gh, path)
	if err != nil {
		return fmt.Errorf("unable to load initial config file: %w", err)
	} else {
		c.logger.Debug("initial config", "config", config)
		callback(config)
	}

	c.logger.Debug("watching config file")

	for {
		select {
		case <-ctx.Done():
			c.logger.Debug("stopping, context cancelled", LogKeyError, err)

			return ctx.Err()
		case err := <-w.Errors:
			c.logger.Error("stopping, err from watcher", LogKeyError, err)

			return err
		case event := <-w.Events:
			// Filter out empty events
			if event.Op == 0 {
				continue
			}

			// Filter out non-write events
			if !event.Op.Has(fsnotify.Write) {
				continue
			}

			c.logger.Info("config file changed")

			config, err := c.loadConfig(ctx, gh, path)
			if err != nil {
				c.logger.Error("unable to handle config change event", LogKeyError, err)

				continue
			}

			c.logger.Debug("new config", "config", config)

			callback(config)
		}
	}
}
