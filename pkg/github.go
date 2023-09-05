package pkg

import (
	"context"
	"fmt"
	"strconv"
	"time"

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
	gitHubNotFoundErrStr string         = "Could not resolve to a"
)

// GitHubNotFoundError is raised when a GitHubinator cannot find the given item. It is a special error that can be
// used to debug why a request failed.
type GitHubNotFoundError error

// GitHubActor represents something that can take actions on GitHub (ie a user or bot).
// It is associated with the following GraphQL interface:
// https://docs.github.com/en/graphql/reference/interfaces#actor.
type GitHubActor struct {
	Login string `json:"login"`
}

func (a GitHubActor) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("login", a.Login),
	)
}

// GitHubRepository represents a repository on GitHub.
// It is associated with the following GraphQL object:
// https://docs.github.com/en/graphql/reference/objects#repository.
type GitHubRepository struct {
	Owner string `json:"owner" yaml:"owner"`
	Name  string `json:"name" yaml:"name"`
}

func (r GitHubRepository) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("owner", r.Owner),
		slog.String("name", r.Name),
	)
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
	Author       GitHubActor                `json:"author"`
	Body         string                     `json:"body"`
	Labels       []string                   `json:"labels"`
	Number       int                        `json:"number"`
	State        githubv4.IssueState        `json:"state"`
	Subscription githubv4.SubscriptionState `json:"Subscription"`
	Title        string                     `json:"title"`
	UpdatedAt    time.Time                  `json:"updatedAt"`
}

func (i GitHubIssue) LogValue() slog.Value {
	return slog.GroupValue(
		slog.Any("author", i.Author.LogValue()),
		slog.String("body", i.Body),
		slog.Int("number", i.Number),
		slog.String("state", string(i.State)),
		slog.String("subscription", string(i.Subscription)),
		slog.String("title", i.Title),
		slog.Time("updatedAt", i.UpdatedAt),
	)
}

// NewTestGitHubIssue creates a new GitHubIssue struct with pre-populated fields. It can be used in unit tests.
func NewTestGitHubIssue() *GitHubIssue {
	return &GitHubIssue{
		Author: GitHubActor{
			Login: "actor",
		},
		Body:         "issue body",
		Labels:       []string{"a/test/label", "another/label"},
		Number:       1,
		State:        "OPEN",
		Subscription: "UNSUBSCRIBED",
		Title:        "a test issue",
		UpdatedAt:    time.Now(),
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
func (f *GitHubIssueFilter) asGithubv4IssueFilters() githubv4.IssueFilters {
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

func (i GitHubItem) LogValue() slog.Value {
	var embeddedAttr slog.Attr

	switch i.Type {
	case GitHubItemIssue:
		embeddedAttr = slog.Any("issue", i.GitHubIssue.LogValue())
	default:
		embeddedAttr = slog.String("embedded", "<none>")
	}

	return slog.GroupValue(
		embeddedAttr,
		slog.String("type", string(i.Type)),
		slog.Any("repo", i.Repo.LogValue()),
		slog.Any("id", i.ID),
	)
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

// isGitHubItemField is used to validate if a label selector is targeting an actual field present in a GitHubItem.
// This function does not use reflect, and is therefore coupled with the GitHubItem definition.
func isGitHubItemField(f string) bool {
	switch f {
	case "type", "repo.owner", "repo.name", "author.login", "body", "number", "title", "state", "subscription":
		return true
	}

	return false
}

type gitHubViewerQuery struct {
	Viewer struct {
		Login    githubv4.String
		IsViewer githubv4.Boolean
	}
}

func (q gitHubViewerQuery) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("login", string(q.Viewer.Login)),
		slog.Bool("isViewer", bool(q.Viewer.IsViewer)),
	)
}

type gitHubRepositoryQuery struct {
	Repository struct {
		Name githubv4.String
	} `graphql:"repository(owner: $owner, name: $name)"`
}

func (q gitHubRepositoryQuery) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("name", string(q.Repository.Name)),
	)
}

type gitHubRepositoryQueryVars struct {
	Name  githubv4.String
	Owner githubv4.String
}

func (v gitHubRepositoryQueryVars) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("name", string(v.Name)),
		slog.String("owner", string(v.Owner)),
	)
}

func (v *gitHubRepositoryQueryVars) AsMap() map[string]any {
	return map[string]any{
		"name":  v.Name,
		"owner": v.Owner,
	}
}

// gitHubLabelQuery is used to query GitHub's graphql API for labels on an issue.
type gitHubLabelQuery struct {
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
			} `graphql:"labels(first: $n, after: $labelsCursor)"`
		} `graphql:"issue(number: $issueNumber)"`
	} `graphql:"repository(owner: $owner, name: $name)"`
}

func (q gitHubLabelQuery) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("endCursor", string(q.Repository.Issue.Labels.PageInfo.EndCursor)),
		slog.Bool("hasNextPage", bool(q.Repository.Issue.Labels.PageInfo.HasNextPage)),
		slog.Any("nodes", q.Repository.Issue.Labels.Nodes),
	)
}

// gitHubLabelQueryVars represents the variables that can be passed to a gitHubLabelQuery.
type gitHubLabelQueryVars struct {
	Owner        githubv4.String
	Name         githubv4.String
	IssueNumber  githubv4.Int
	N            githubv4.Int
	LabelsCursor *githubv4.String
}

func (v *gitHubLabelQueryVars) AsMap() map[string]any {
	return map[string]any{
		"owner":        v.Owner,
		"name":         v.Name,
		"issueNumber":  v.IssueNumber,
		"n":            v.N,
		"labelsCursor": v.LabelsCursor,
	}
}

func (v gitHubLabelQueryVars) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("owner", string(v.Owner)),
		slog.String("name", string(v.Name)),
		slog.Int("issueNumber", int(v.IssueNumber)),
		slog.Int("n", int(v.N)),
		slog.Any("labelsCursor", v.LabelsCursor),
	)
}

type gitHubIssueBodyQuery struct {
	Repository struct {
		Issue struct {
			BodyText githubv4.String
		} `graphql:"issue(number: $issueNumber)"`
	} `graphql:"repository(owner: $owner, name: $name)"`
}

func (q gitHubIssueBodyQuery) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("bodyText", string(q.Repository.Issue.BodyText)),
	)
}

type gitHubIssueBodyQueryVars struct {
	Owner       githubv4.String
	Name        githubv4.String
	IssueNumber githubv4.Int
}

func (v *gitHubIssueBodyQueryVars) AsMap() map[string]any {
	return map[string]any{
		"owner":       v.Owner,
		"name":        v.Name,
		"issueNumber": v.IssueNumber,
	}
}

func (v gitHubIssueBodyQueryVars) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("owner", string(v.Owner)),
		slog.String("name", string(v.Name)),
		slog.Int("issueNumber", int(v.IssueNumber)),
	)
}

// gitHubIssueQuery is used to query the GitHub graphql for an issue.
// Because the GitHub issue contains paginated labels, we need to do a special work around to query the issue and
// labels separately to fill in a GitHubIssue struct.
type gitHubIssueQuery struct {
	Repository struct {
		Issues struct {
			Nodes []struct {
				Author             GitHubActor
				ID                 githubv4.ID
				Number             githubv4.Int
				Title              githubv4.String
				State              githubv4.IssueState
				UpdatedAt          githubv4.DateTime
				ViewerSubscription githubv4.SubscriptionState
			}
			PageInfo struct {
				EndCursor   githubv4.String
				HasNextPage githubv4.Boolean
			}
		} `graphql:"issues(first: $n, after: $issuesCursor, filterBy: $filters)"`
	} `graphql:"repository(owner: $owner, name: $name)"`
}

// AsGitHubIssue converts the gitHubIssueQuery into a map of the contained issues to their IDs.
func (q *gitHubIssueQuery) AsGitHubIssues() map[githubv4.ID]*GitHubIssue {
	issues := map[githubv4.ID]*GitHubIssue{}

	for _, n := range q.Repository.Issues.Nodes {
		issues[n.ID] = &GitHubIssue{
			Author:       n.Author,
			Body:         "",
			Labels:       []string{},
			Number:       int(n.Number),
			State:        n.State,
			Subscription: n.ViewerSubscription,
			Title:        string(n.Title),
			UpdatedAt:    n.UpdatedAt.Time,
		}
	}

	return issues
}

func (q gitHubIssueQuery) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("endCursor", string(q.Repository.Issues.PageInfo.EndCursor)),
		slog.Bool("hasNextPage", bool(q.Repository.Issues.PageInfo.HasNextPage)),
		slog.Any("nodes", q.Repository.Issues.Nodes),
	)
}

// gitHubIssueQueryVars represents the variables that can be passed to a gitHubIssueQuery.
type gitHubIssueQueryVars struct {
	Owner        githubv4.String
	Name         githubv4.String
	Filters      githubv4.IssueFilters
	IssuesCursor *githubv4.String
	N            githubv4.Int
}

func (q gitHubIssueQueryVars) AsMap() map[string]any {
	return map[string]any{
		"owner":        q.Owner,
		"name":         q.Name,
		"filters":      q.Filters,
		"issuesCursor": q.IssuesCursor,
		"n":            q.N,
	}
}

func (q gitHubIssueQueryVars) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("owner", string(q.Owner)),
		slog.String("name", string(q.Name)),
		slog.Any("issuesCursor", q.IssuesCursor),
		slog.Int("n", int(q.N)),
		slog.Any("filters", q.Filters),
	)
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

	// SetSubscriptionRequests holds the issue IDs passed to SetSubscription.
	SetSubscriptionRequests []githubv4.ID

	// SetSubscriptionError holds the returned error for SetSubscription
	SetSubscriptionError error
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
	t.SetSubscriptionRequests = append(t.SetSubscriptionRequests, id)

	return t.SetSubscriptionError
}

// NewMockGitHubinator creates a new MockGitHubinator instance with pre-populated, non-error return values.
func NewMockGitHubinator() *MockGitHubinator {
	return &MockGitHubinator{
		CheckRepositoryRequests: []GitHubRepository{},
		CheckRepositoryError:    nil,
		WhoAmIRequests:          0,
		WhoAmIReturn:            "user",
		WhoAmIError:             nil,
		SetSubscriptionRequests: []githubv4.ID{},
		SetSubscriptionError:    nil,
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
	return &gitHubinator{
		retries: retries,
		timeout: gh.timeout,
		token:   gh.token,
		client:  nil,
		logger:  gh.logger,
	}
}

func (gh *gitHubinator) WithTimeout(timeout time.Duration) GitHubinator {
	return &gitHubinator{
		retries: gh.retries,
		timeout: timeout,
		token:   gh.token,
		client:  nil,
		logger:  gh.logger,
	}
}

func (gh *gitHubinator) WithToken(token string) GitHubinator {
	return &gitHubinator{
		retries: gh.retries,
		timeout: gh.timeout,
		token: oauth2.StaticTokenSource(
			&oauth2.Token{AccessToken: token},
		),
		client: nil,
		logger: gh.logger,
	}
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

	query := gitHubViewerQuery{}

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

	query := gitHubRepositoryQuery{}

	vars := gitHubRepositoryQueryVars{
		Name:  githubv4.String(ghr.Name),
		Owner: githubv4.String(ghr.Owner),
	}

	queryLogger := gh.logger.With("vars", vars)
	queryLogger.Debug("executing check repository query")

	MetricRepoQueryTotal.Inc()

	err := gh.client.Query(ctx, &query, vars.AsMap())
	if err != nil {
		queryLogger.Debug("got error on check repository query", LogKeyError, err)

		MetricRepoQueryErrorTotal.Inc()

		return err
	}

	queryLogger.Debug("response on check repository query", "result", query)

	return nil
}

// listIssueLabels returns a list of labels for the given issue, performing pagination as needed.
func (gh *gitHubinator) listIssueLabels(
	ctx context.Context, ghr GitHubRepository, issueNumber int,
) ([]string, error) {
	query := &gitHubLabelQuery{}

	vars := gitHubLabelQueryVars{
		Owner:        githubv4.String(ghr.Owner),
		Name:         githubv4.String(ghr.Name),
		IssueNumber:  githubv4.Int(issueNumber),
		N:            100,
		LabelsCursor: (*githubv4.String)(nil),
	}

	allLabels := []string{}

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
			queryLogger := gh.logger.With("vars", vars)
			queryLogger.Debug("executing list issue labels query")

			MetricIssueLabelQueryTotal.Inc()

			err := gh.client.Query(ctx, &query, vars.AsMap())
			if err != nil {
				queryLogger.Debug("got error on list issue labels query", LogKeyError, err)

				MetricIssueLabelQueryErrorTotal.Inc()

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

			*vars.LabelsCursor = query.Repository.Issue.Labels.PageInfo.EndCursor
		}
	}
}

func (gh *gitHubinator) getIssueBody(
	ctx context.Context, ghr GitHubRepository, issueNumber int,
) (string, error) {
	query := &gitHubIssueBodyQuery{}

	vars := gitHubIssueBodyQueryVars{
		Owner:       githubv4.String(ghr.Owner),
		Name:        githubv4.String(ghr.Name),
		IssueNumber: githubv4.Int(issueNumber),
	}

	queryLogger := gh.logger.With("vars", vars)
	queryLogger.Debug("executing get issue body text query")

	MetricIssueBodyQueryTotal.Inc()

	err := gh.client.Query(ctx, &query, vars.AsMap())
	if err != nil {
		queryLogger.Debug("got error on get issue body regex query", LogKeyError, err)

		MetricIssueLabelQueryErrorTotal.Inc()

		return "", err
	}

	queryLogger.Debug("got response on get issue body text query", "response", query)

	return string(query.Repository.Issue.BodyText), nil
}

func (gh *gitHubinator) ListIssues(
	ctx context.Context, ghr GitHubRepository, filter *GitHubIssueFilter,
	matcher Matchinator,
) ([]*GitHubItem, error) {
	if gh.client == nil {
		gh.setupClient()
	}

	query := &gitHubIssueQuery{}

	vars := &gitHubIssueQueryVars{
		Owner:        githubv4.String(ghr.Owner),
		Name:         githubv4.String(ghr.Name),
		Filters:      filter.asGithubv4IssueFilters(),
		IssuesCursor: (*githubv4.String)(nil),
		N:            100,
	}

	allIssues := []*GitHubItem{}

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
			queryLogger := gh.logger.With("vars", vars)
			queryLogger.Debug("executing list issues query")

			MetricIssueQueryTotal.Inc()

			err := gh.client.Query(ctx, &query, vars.AsMap())
			if err != nil {
				queryLogger.Debug("got error on list issues query", LogKeyError, err)

				MetricIssueQueryErrorTotal.Inc()

				return nil, err
			}

			queryLogger.Debug("got response on list issues query", "query", query)

			for id, issue := range query.AsGitHubIssues() {
				item := &GitHubItem{
					Type:        GitHubItemIssue,
					Repo:        ghr,
					ID:          id,
					GitHubIssue: *issue,
				}

				queryLogger.Debug("got item for list issues query", "issue", item)

				if matcher.HasRequiredLabels() {
					labels, err := gh.listIssueLabels(ctx, ghr, issue.Number)
					if err != nil {
						return nil, err
					}

					item.GitHubIssue.Labels = labels
				}

				if matcher.HasBodyRegex() {
					queryLogger.Debug("getting issue body for body regex matching")

					bodyText, err := gh.getIssueBody(ctx, ghr, issue.Number)
					if err != nil {
						return nil, err
					}

					item.GitHubIssue.Body = bodyText
				}

				if matches, reason := matcher.Matches(item); !matches {
					queryLogger.Debug("item filtered out by the matcher", "item", item, "reason", reason)
					MetricFilteredTotal.Inc()

					continue
				} else {
					queryLogger.Debug("item matched", "item", item, "reason", reason)
				}

				bodyText, err := gh.getIssueBody(ctx, ghr, issue.Number)
				if err != nil {
					return nil, err
				}

				item.GitHubIssue.Body = bodyText

				allIssues = append(allIssues, item)
			}

			if !query.Repository.Issues.PageInfo.HasNextPage {
				return allIssues, nil
			}

			vars.IssuesCursor = &query.Repository.Issues.PageInfo.EndCursor
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

	MetricNewSubscriptionTotal.Inc()

	err := gh.client.Mutate(ctx, &m, input, nil)
	if err != nil {
		mutateLogger.Debug("got error update subscription mutation", LogKeyError, err)

		MetricNewSubscriptionErrorTotal.Inc()

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
