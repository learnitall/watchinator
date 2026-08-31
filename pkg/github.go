package pkg

import (
	"context"
	"fmt"
	"strconv"
	"strings"
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
	Number       int32                      `json:"number"`
	State        githubv4.IssueState        `json:"state"`
	Subscription githubv4.SubscriptionState `json:"Subscription"`
	Title        string                     `json:"title"`
	UpdatedAt    time.Time                  `json:"updatedAt"`
}

func (i GitHubIssue) LogValue() slog.Value {
	return slog.GroupValue(
		slog.Any("author", i.Author.LogValue()),
		slog.String("body", i.Body),
		slog.Int("number", int(i.Number)),
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

// GitHubIssueFilter narrows which items GitHub returns before any client-side
// matching runs. It is rendered into a GitHub search query string.
type GitHubIssueFilter struct {
	Labels []string
	States []string
}

// quoteSearchTerm wraps a value in quotes when it holds characters that would
// otherwise terminate the qualifier.
func quoteSearchTerm(s string) string {
	if !strings.ContainsAny(s, " \t\"") {
		return s
	}

	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}

// asSearchQuery renders the filter as a GitHub search query scoped to ghr.
// Values comma-joined inside a single qualifier are ORed by GitHub, which is the
// semantic SearchLabels documents ("at least one of these labels"). Note that
// the repository.issues connection this replaced ANDed them instead.
func (f *GitHubIssueFilter) asSearchQuery(ghr GitHubRepository) string {
	terms := []string{
		"repo:" + quoteSearchTerm(ghr.Owner+"/"+ghr.Name),
		// search type ISSUE covers pull requests too; this keeps it to issues.
		"is:issue",
	}

	if len(f.Labels) > 0 {
		quoted := make([]string, 0, len(f.Labels))
		for _, l := range f.Labels {
			quoted = append(quoted, quoteSearchTerm(l))
		}

		terms = append(terms, "label:"+strings.Join(quoted, ","))
	}

	// Repeated state qualifiers AND into a contradiction, and naming every state
	// is the same as not filtering, so only a lone state narrows anything.
	if len(f.States) == 1 {
		terms = append(terms, "state:"+strings.ToLower(f.States[0]))
	}

	return strings.Join(terms, " ")
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
		"number":       strconv.Itoa(int(i.Number)),
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

// searchResultLimit is the ceiling GitHub's search API paginates to, regardless
// of how many items actually matched.
const searchResultLimit = 1000

// gitHubIssueNode is the `... on Issue` fragment of a search result. Search
// returns the body and labels inline, so unlike the repository.issues connection
// this replaces, listing costs one request per page instead of one per page plus
// two per item.
type gitHubIssueNode struct {
	Author             GitHubActor
	ID                 githubv4.ID
	Number             githubv4.Int
	Title              githubv4.String
	BodyText           githubv4.String
	State              githubv4.IssueState
	UpdatedAt          githubv4.DateTime
	ViewerSubscription githubv4.SubscriptionState
	// Inline labels are not paginated, so an item carrying more than this loses
	// the tail and requiredLabels could miss. No repository we watch is close.
	Labels struct {
		Nodes []struct {
			Name githubv4.String
		}
	} `graphql:"labels(first: 100)"`
	Repository struct {
		Name  githubv4.String
		Owner struct {
			Login githubv4.String
		}
	}
}

// asGitHubItem converts the node into a GitHubItem. It returns nil for a zero
// node, which is what an unmatched inline fragment in the result union decodes to.
func (n *gitHubIssueNode) asGitHubItem() *GitHubItem {
	if n.ID == nil || n.ID == "" {
		return nil
	}

	labels := make([]string, 0, len(n.Labels.Nodes))
	for _, l := range n.Labels.Nodes {
		labels = append(labels, string(l.Name))
	}

	return &GitHubItem{
		Type: GitHubItemIssue,
		Repo: GitHubRepository{
			Owner: string(n.Repository.Owner.Login),
			Name:  string(n.Repository.Name),
		},
		ID: n.ID,
		GitHubIssue: GitHubIssue{
			Author:       n.Author,
			Body:         string(n.BodyText),
			Labels:       labels,
			Number:       int32(n.Number),
			State:        n.State,
			Subscription: n.ViewerSubscription,
			Title:        string(n.Title),
			UpdatedAt:    n.UpdatedAt.Time,
		},
	}
}

// gitHubSearchQuery queries GitHub's search connection for items matching a
// rendered query string.
type gitHubSearchQuery struct {
	Search struct {
		IssueCount githubv4.Int
		PageInfo   struct {
			EndCursor   githubv4.String
			HasNextPage githubv4.Boolean
		}
		Nodes []struct {
			Issue gitHubIssueNode `graphql:"... on Issue"`
		}
	} `graphql:"search(query: $q, type: $searchType, first: $n, after: $searchCursor)"`
}

func (q gitHubSearchQuery) LogValue() slog.Value {
	return slog.GroupValue(
		slog.Int("issueCount", int(q.Search.IssueCount)),
		slog.String("endCursor", string(q.Search.PageInfo.EndCursor)),
		slog.Bool("hasNextPage", bool(q.Search.PageInfo.HasNextPage)),
		slog.Int("numNodes", len(q.Search.Nodes)),
	)
}

// gitHubSearchQueryVars represents the variables that can be passed to a gitHubSearchQuery.
type gitHubSearchQueryVars struct {
	Q            githubv4.String
	Type         githubv4.SearchType
	SearchCursor *githubv4.String
	N            githubv4.Int
}

func (q gitHubSearchQueryVars) AsMap() map[string]any {
	return map[string]any{
		"q":            q.Q,
		"searchType":   q.Type,
		"searchCursor": q.SearchCursor,
		"n":            q.N,
	}
}

func (q gitHubSearchQueryVars) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("q", string(q.Q)),
		slog.String("searchType", string(q.Type)),
		slog.Any("searchCursor", q.SearchCursor),
		slog.Int("n", int(q.N)),
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

func (gh *gitHubinator) ListIssues(
	ctx context.Context, ghr GitHubRepository, filter *GitHubIssueFilter,
	matcher Matchinator,
) ([]*GitHubItem, error) {
	if gh.client == nil {
		gh.setupClient()
	}

	query := &gitHubSearchQuery{}

	vars := &gitHubSearchQueryVars{
		Q:            githubv4.String(filter.asSearchQuery(ghr)),
		Type:         githubv4.SearchTypeIssue,
		SearchCursor: (*githubv4.String)(nil),
		N:            100,
	}

	allItems := []*GitHubItem{}

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
			queryLogger := gh.logger.With("vars", vars)
			queryLogger.Debug("executing search query")

			MetricSearchQueryTotal.Inc()

			err := gh.client.Query(ctx, &query, vars.AsMap())
			if err != nil {
				queryLogger.Debug("got error on search query", LogKeyError, err)

				MetricSearchQueryErrorTotal.Inc()

				return nil, err
			}

			queryLogger.Debug("got response on search query", "query", query)

			// Search stops paginating at searchResultLimit no matter how many matched,
			// so a filter this broad is silently losing its tail.
			if count := int(query.Search.IssueCount); count > searchResultLimit {
				queryLogger.Warn(
					"search matched more items than GitHub will page through; narrow the watch",
					"matched", count, "limit", searchResultLimit,
				)
			}

			for i := range query.Search.Nodes {
				item := query.Search.Nodes[i].Issue.asGitHubItem()
				if item == nil {
					continue
				}

				queryLogger.Debug("got item for search query", "item", item)

				if matches, reason := matcher.Matches(item); !matches {
					queryLogger.Debug("item filtered out by the matcher", "item", item, "reason", reason)
					MetricFilteredTotal.Inc()

					continue
				}

				queryLogger.Debug("item matched", "item", item)

				allItems = append(allItems, item)
			}

			if !query.Search.PageInfo.HasNextPage {
				return allItems, nil
			}

			vars.SearchCursor = &query.Search.PageInfo.EndCursor
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
