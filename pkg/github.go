package pkg

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/goccy/go-json"
	"github.com/hashicorp/go-retryablehttp"
	"github.com/shurcooL/githubv4"
	"golang.org/x/exp/slog"
	"golang.org/x/oauth2"
	"k8s.io/apimachinery/pkg/labels"
)

// GitHubItemType specifies the type of a GitHub item.
type GitHubItemType string

const (
	GitHubItemIssue      GitHubItemType = "issue"
	gitHubNotFoundErrStr                = "Could not resolve to a"
)

// GitHubNotFoundError is raised when a GitHubinator cannot find the given item. It is a special error that can be
// used to debug why a request failed.
type GitHubNotFoundError error

// GitHubRepository represents a repository on GitHub.
// It is associated with the following GraphQL object:
// https://docs.github.com/en/graphql/reference/objects#repository.
type GitHubRepository struct {
	Owner string `json:"owner" yaml:"owner"`
	Name  string `json:"name" yaml:"name"`
}

// GitHubActor represents something that can take actions on GitHub (ie a user or bot).
// It is associated with the following GraphQL interface:
// https://docs.github.com/en/graphql/reference/interfaces#actor.
type GitHubActor struct {
	Login string `json:"login"`
}

// GitHubLabel represents an issue or PR label on GitHub.
// Is is associated with the following GraphQL object:
// https://docs.github.com/en/graphql/reference/objects#label.
type GitHubLabel struct {
	Name string `json:"name"`
}

// GitHubIssue represents an issue on GitHub.
// It is associated with the following GraphQL object:
// https://docs.github.com/en/graphql/reference/objects#issue.
type GitHubIssue struct {
	Author       *GitHubActor               `json:"author"`
	Body         string                     `json:"body"`
	Labels       []string                   `json:"labels"`
	Number       int                        `json:"number"`
	State        githubv4.IssueState        `json:"state"`
	Title        string                     `json:"title"`
	Subscription githubv4.SubscriptionState `json:"Subscription"`
}

// NewTestGitHubIssue creates a new GitHubIssue struct with pre-populated fields. It can be used in unit tests.
func NewTestGitHubIssue() *GitHubIssue {
	return &GitHubIssue{
		Author: &GitHubActor{
			Login: "actor",
		},
		Body:         "issue body",
		Labels:       []string{"a/test/label", "another/label"},
		Number:       1,
		State:        "OPEN",
		Title:        "a test issue",
		Subscription: "UNSUBSCRIBED",
	}
}

// gitHubIssueQuery is used to query the GitHub graphql for an issue.
// Because the GitHub issue contains paginated labels, we need to do a special work around to query the issue and
// labels separately to fill in a GitHubIssue struct.
// It is kept separate from the GitHubIssue type defined here to allow for easier documentation for users.
type gitHubIssueQuery struct {
	Author             GitHubActor
	BodyText           string
	ID                 githubv4.ID
	Number             int
	Title              string
	State              githubv4.IssueState
	ViewerSubscription githubv4.SubscriptionState
}

// AsGitHubIssue converts the gitHubIssueQuery into a GitHubIssue struct.
func (g gitHubIssueQuery) AsGitHubIssue() GitHubIssue {
	return GitHubIssue{
		Author:       &g.Author,
		Body:         g.BodyText,
		Number:       g.Number,
		State:        g.State,
		Title:        g.Title,
		Subscription: g.ViewerSubscription,
	}
}

// GitHubIssueFilter is a filter that can be used when listing issues on GitHub.
// It is associated with (but decoupled from) the following GraphQL input object:
// https://docs.github.com/en/graphql/reference/input-objects#issuefilters.
type GitHubIssueFilter struct {
	Labels []string
	States []string
}

// asGithubv4IssueFilters converts the GitHubIssueFilter into a githubv4.IssueFilters struct for usage in the
// githubv4 GraphQL library. It performs specific type conversions and formats issue states in all caps.
func (f GitHubIssueFilter) asGithubv4IssueFilters() githubv4.IssueFilters {
	var labels *[]githubv4.String = nil

	if f.Labels != nil {
		labels = &[]githubv4.String{}
		for _, l := range f.Labels {
			*labels = append(*labels, githubv4.String(l))
		}
	}

	var states *[]githubv4.IssueState = nil

	if f.States != nil {
		states = &[]githubv4.IssueState{}
		for _, s := range f.States {
			// GitHub expects these to be in all caps
			*states = append(*states, githubv4.IssueState(s))
		}
	}

	return githubv4.IssueFilters{
		Labels: labels,
		States: states,
	}
}

// GitHubItem is a container sturct holding different items that can be queried on GitHub. It is used to provide
// a common format for label selectors.
type GitHubItem struct {
	GitHubIssue
	Type GitHubItemType   `json:"type"`
	Repo GitHubRepository `json:"repo"`
	ID   githubv4.ID      `json:"id"`
}

// NewTestGitHubItem creates a new instance of a GitHubItem with pre-populated fields. It can be used in unit tests.
func NewTestGitHubItem() *GitHubItem {
	return &GitHubItem{
		Type: "issue",
		Repo: GitHubRepository{
			Owner: "owner",
			Name:  "repo",
		},
		GitHubIssue: *NewTestGitHubIssue(),
	}
}

// GitHubItemAsLabelSet converts the given GitHubItem into a k8s.io/apimachinery/pkg/labels.Set, for applying label
// selectors specified in a Watch. Fields are convered into lowercase keys in the map, and values are converted
// into strings. Nested structs in a GitHubItem will have their fields writtin with dot-notation. For instance,
// GitHubItem.Repo.Name will have the key "repo.name" in the returned set.
// This function does not use reflect, and is therefore coupled with the GitHubItem definition.
func GitHubItemAsLabelSet(i *GitHubItem) labels.Set {
	m := map[string]string{
		"type":         string(i.Type),
		"repo.owner":   i.Repo.Owner,
		"repo.name":    i.Repo.Name,
		"author.login": i.Author.Login,
		"body":         i.Body,
		"number":       strconv.Itoa(i.Number),
		"title":        i.Title,
		"state":        string(i.State),
		"subscription": string(i.Subscription),
	}

	return labels.Set(m)
}

// isGitHubItemField is used to validate if a label selector is targetting an actual field present in a GitHubItem.
// This function does not use reflect, and is therefore coupled with the GitHubItem definition.
func isGitHubItemField(f string) bool {
	switch f {
	case "type", "repo.owner", "repo.name", "author.login", "body", "number", "title", "state", "subscription":
		return true
	}

	return false
}

// GitHubinator is used to fetch and update data from GitHub. The With* builder methods return a new instance of
// a GitHubinator.
type GitHubinator interface {
	// WithRetries will set the number of retries the GitHubinator will use when fetching or updating data.
	WithRetries(retries int) GitHubinator

	// WithTimeout sets the amount of time per try that the GitHubinator will wait for success before assuming a
	// request has failed.
	WithTimeout(timeout time.Duration) GitHubinator

	// WithToken sets the authentication token to use for the GH API, such as a PAT.
	// A test request will be sent to GitHub to verify authentication.
	WithToken(token string) GitHubinator

	// WhoAmI will make a test query to GitHub to get the name and login for the given PAT.
	WhoAmI(ctx context.Context) (string, error)

	// CheckRepository checks if the given repository exists.
	CheckRepository(ctx context.Context, ghr GitHubRepository) error

	// ListIssues returns a list of issues for the given repository.
	ListIssues(
		ctx context.Context, ghr GitHubRepository, filter *GitHubIssueFilter, matcher Matchinator,
	) ([]*GitHubItem, error)

	// SetSubscription sets the subscription state of the given item for the viewer.
	SetSubscription(ctx context.Context, id githubv4.ID, state githubv4.SubscriptionState) error
}

// MockGitHubinator implements the GitHubinator interface. The returned values from its methods can be controlled,
// allowing for it to be used for unit testing.
type MockGitHubinator struct {
	// CheckRepositoryRequests holds th parameters to calls to CheckRepository.
	CheckRepositoryRequests []GitHubRepository

	// CheckRepositoryError holds the returned error for CheckRepository.
	CheckRepositoryError error

	// WhoAmIRequests holds the number of times WhoAmI has been called
	WhoAmIRequests int

	// WhoAmIReturn holds the user returned from calls to WhoAmI.
	WhoAmIReturn string

	// WhoAmIError holds the errors that will be returned from WhoAmI.
	WhoAmIError error
}

func (t *MockGitHubinator) WithRetries(_ int) GitHubinator { return t }

func (t *MockGitHubinator) WithTimeout(_ time.Duration) GitHubinator { return t }

func (t *MockGitHubinator) WithToken(_ string) GitHubinator { return t }

func (t *MockGitHubinator) WhoAmI(_ context.Context) (string, error) {
	t.WhoAmIRequests += 1

	return t.WhoAmIReturn, t.WhoAmIError
}

func (t *MockGitHubinator) CheckRepository(ctx context.Context, ghr GitHubRepository) error {
	t.CheckRepositoryRequests = append(t.CheckRepositoryRequests, ghr)

	return t.CheckRepositoryError
}

func (t *MockGitHubinator) ListIssues(
	ctx context.Context, ghr GitHubRepository, filter *GitHubIssueFilter, matcher Matchinator,
) ([]*GitHubItem, error) {
	return nil, nil
}

func (t *MockGitHubinator) SetSubscription(
	ctx context.Context, id githubv4.ID, state githubv4.SubscriptionState,
) error {
	return nil
}

// NewMockGitHubinator creates a new MockGitHubinator instance with pre-populated, non-error return values.
func NewMockGitHubinator() *MockGitHubinator {
	return &MockGitHubinator{
		CheckRepositoryRequests: []GitHubRepository{},
		CheckRepositoryError:    nil,
		WhoAmIRequests:          0,
		WhoAmIReturn:            "user",
		WhoAmIError:             nil,
	}
}

// gitHubinator is the packages internal implementation of the GitHubinator interface.
// The With* builder functions will set the internal field 'client' to nil to signal that the client needs to be
// setup. Any function which uses the client must perform a nil check.
type gitHubinator struct {
	retries int
	timeout time.Duration
	token   oauth2.TokenSource
	client  *githubv4.Client
	logger  *slog.Logger
}

func (gh *gitHubinator) WithRetries(retries int) GitHubinator {
	gh.retries = retries
	gh.client = nil

	return gh
}

func (gh *gitHubinator) WithTimeout(timeout time.Duration) GitHubinator {
	gh.timeout = timeout
	gh.client = nil

	return gh
}

func (gh *gitHubinator) WithToken(token string) GitHubinator {
	gh.token = oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: token},
	)
	gh.client = nil

	return gh
}

func (gh *gitHubinator) setupClient() {
	oauthClient := oauth2.NewClient(context.TODO(), gh.token)

	rclient := retryablehttp.NewClient()
	rclient.RetryMax = gh.retries
	rclient.RetryWaitMax = gh.timeout
	rclient.HTTPClient = oauthClient

	gh.client = githubv4.NewClient(oauthClient)
}

func (gh *gitHubinator) WhoAmI(ctx context.Context) (string, error) {
	if gh.client == nil {
		gh.setupClient()
	}

	var query struct {
		Viewer struct {
			Login    githubv4.String
			IsViewer githubv4.Boolean
		}
	}

	gh.logger.Debug("executing whoami query")

	err := gh.client.Query(ctx, &query, nil)
	if err != nil {
		gh.logger.Debug("got error on whoami query", LogKeyError, err)

		return "", err
	}

	gh.logger.Debug("response on whoami query", "result", query)

	if !query.Viewer.IsViewer {
		return "", fmt.Errorf("unexpected result, returned user is not viewer")
	}

	return string(query.Viewer.Login), nil
}

func (gh *gitHubinator) CheckRepository(ctx context.Context, ghr GitHubRepository) error {
	if gh.client == nil {
		gh.setupClient()
	}

	var query struct {
		Repository struct {
			Name string
		} `graphql:"repository(owner: $owner, name: $name)"`
	}

	vars := map[string]any{
		"owner": githubv4.String(ghr.Owner),
		"name":  githubv4.String(ghr.Name),
	}

	queryLogger := gh.logger.With("vars", vars)
	queryLogger.Debug("executing check repository query")

	err := gh.client.Query(ctx, &query, vars)
	if err != nil {
		queryLogger.Debug("got error on check repository query", LogKeyError, err)

		if strings.HasPrefix(err.Error(), gitHubNotFoundErrStr) {
			return GitHubNotFoundError(err)
		}
	}

	queryLogger.Debug("response on check repository query", "result", query)

	return nil
}

// anyToString attempts to marshal the given value into json and return the result as a string.
// If marshalling fails, then the item is formatted using '%+v', with the marshal error attached.
// This function can be used to convert maps with pointers to readable strings or nested structs with pointers to
// strings for debug logs.
func anyToString(a any) string {
	j, err := json.MarshalWithOption(a)
	if err != nil {
		return fmt.Sprintf("%+v (%s)", a, err)
	}

	return string(j)
}

// listIssueLabels returns a list of labels for the given issue, performing pagination as needed.
func (gh *gitHubinator) listIssueLabels(
	ctx context.Context, ghr GitHubRepository, issueNumber int,
) ([]string, error) {
	var query struct {
		Repository struct {
			Issue struct {
				Labels struct {
					Nodes []struct {
						Name string
					}
					PageInfo struct {
						EndCursor   githubv4.String
						HasNextPage githubv4.Boolean
					}
				} `graphql:"labels(first: 100, after: $labelsCursor)"`
			} `graphql:"issue(number: $issueNumber)"`
		} `graphql:"repository(owner: $owner, name: $name)"`
	}

	vars := map[string]any{
		"owner":        githubv4.String(ghr.Owner),
		"name":         githubv4.String(ghr.Name),
		"issueNumber":  githubv4.Int(issueNumber),
		"labelsCursor": (*githubv4.String)(nil),
	}

	allLabels := []string{}

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
			queryLogger := gh.logger.With("vars", anyToString(vars))
			queryLogger.Debug("executing list issue labels query")

			err := gh.client.Query(ctx, &query, vars)
			if err != nil {
				queryLogger.Debug("got error on list issue labels query", LogKeyError, err)

				return nil, err
			}

			queryLogger.Debug("got response on list labels query", "response", query)

			for i := range query.Repository.Issue.Labels.Nodes {
				l := query.Repository.Issue.Labels.Nodes[i]
				allLabels = append(allLabels, l.Name)
			}

			if !query.Repository.Issue.Labels.PageInfo.HasNextPage {
				return allLabels, nil
			}

			vars["labelsCursor"] = query.Repository.Issue.Labels.PageInfo.EndCursor
		}
	}
}

func (gh *gitHubinator) ListIssues(
	ctx context.Context, ghr GitHubRepository, filter *GitHubIssueFilter,
	matcher Matchinator,
) ([]*GitHubItem, error) {
	if gh.client == nil {
		gh.setupClient()
	}

	var query struct {
		Repository struct {
			Issues struct {
				Nodes    []gitHubIssueQuery
				PageInfo struct {
					EndCursor   githubv4.String
					HasNextPage githubv4.Boolean
				}
			} `graphql:"issues(first:10, after: $issuesCursor, filterBy: $filters)"`
		} `graphql:"repository(owner: $owner, name: $name)"`
	}

	vars := map[string]any{
		"owner":        githubv4.String(ghr.Owner),
		"name":         githubv4.String(ghr.Name),
		"filters":      filter.asGithubv4IssueFilters(),
		"issuesCursor": (*githubv4.String)(nil), // nil after argument to get first page
	}

	allIssues := []*GitHubItem{}

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
			queryLogger := gh.logger.With("vars", anyToString(vars))
			queryLogger.Debug("executing list issues query")

			err := gh.client.Query(ctx, &query, vars)
			if err != nil {
				queryLogger.Debug("got error on list issues query", LogKeyError, err)

				return nil, err
			}

			queryLogger.Debug("got response on list issues query", "response", query)

			for _, i := range query.Repository.Issues.Nodes {
				item := &GitHubItem{
					Type:        GitHubItemIssue,
					Repo:        ghr,
					ID:          i.ID,
					GitHubIssue: i.AsGitHubIssue(),
				}

				labels, err := gh.listIssueLabels(ctx, ghr, i.Number)
				if err != nil {
					return nil, err
				}

				item.Labels = labels

				if matches, reason := matcher.Matches(item); !matches {
					queryLogger.Debug("item filtered out by the matcher", "item", anyToString(item), "reason", reason)

					continue
				}

				allIssues = append(allIssues, item)
			}

			if !query.Repository.Issues.PageInfo.HasNextPage {
				return allIssues, nil
			}

			vars["issuesCursor"] = query.Repository.Issues.PageInfo.EndCursor
		}
	}
}

func (gh *gitHubinator) SetSubscription(ctx context.Context, id githubv4.ID, state githubv4.SubscriptionState) error {
	if gh.client == nil {
		gh.setupClient()
	}

	var m struct {
		UpdateSubscription struct {
			Subscribable struct {
				ViewerSubscription githubv4.SubscriptionState
			}
		} `graphql:"updateSubscription(input: $input)"`
	}

	input := githubv4.UpdateSubscriptionInput{
		SubscribableID: id,
		State:          state,
	}

	mutateLogger := gh.logger.With("input.state", state).With("input.id", id)
	mutateLogger.Debug("executing update subscription mutation")

	err := gh.client.Mutate(ctx, &m, input, nil)
	if err != nil {
		mutateLogger.Debug("got error update subscription mutation", LogKeyError, err)

		return err
	}

	mutateLogger.Debug("got response on update subscription mutation", "response", m)

	return nil
}

// NewGitHubinator creates a new instance of a GitHubinator.
func NewGitHubinator(logger *slog.Logger) GitHubinator {
	return &gitHubinator{
		retries: 0,
		timeout: 0,
		token:   oauth2.StaticTokenSource(&oauth2.Token{AccessToken: ""}),
		client:  nil,
		logger:  logger,
	}
}
