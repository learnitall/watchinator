package pkg

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
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
	GitHubItemIssue       GitHubItemType = "issue"
	GitHubItemPullRequest GitHubItemType = "pullRequest"
)

// gitHubNotFoundErrStr is how GitHub opens the message for an entity it cannot
// resolve. The response also carries type: NOT_FOUND, but shurcooL/graphql keeps
// only Message when it decodes the error array, so the string is all that
// survives to be matched on.
const gitHubNotFoundErrStr = "Could not resolve to a"

// GitHubNotFoundError reports that GitHub could not resolve a requested entity.
//
// It is a struct rather than the `type GitHubNotFoundError error` it replaces:
// that declared an interface with the same method set as error, so every non-nil
// error satisfied it and a `case GitHubNotFoundError` type switch matched
// auth failures, rate limiting and network errors alike. Match it with errors.As.
type GitHubNotFoundError struct {
	err error
}

func (e *GitHubNotFoundError) Error() string {
	return e.err.Error()
}

func (e *GitHubNotFoundError) Unwrap() error {
	return e.err
}

// asNotFoundError wraps err when GitHub is reporting a missing entity, so callers
// can tell "does not exist" apart from "could not ask".
func asNotFoundError(err error) error {
	if err == nil || !strings.Contains(err.Error(), gitHubNotFoundErrStr) {
		return err
	}

	return &GitHubNotFoundError{err: err}
}

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

// GitHubItemState is the state of an item. Issues are OPEN or CLOSED; pull
// requests add MERGED, so this cannot be githubv4.IssueState.
type GitHubItemState string

const (
	GitHubItemStateOpen   GitHubItemState = "OPEN"
	GitHubItemStateClosed GitHubItemState = "CLOSED"
	GitHubItemStateMerged GitHubItemState = "MERGED"
)

// asSearchTerm renders the state as a search qualifier. MERGED is not a value
// `state:` accepts: `state:merged` matches nothing rather than erroring, so it
// has to go through `is:`.
func (s GitHubItemState) asSearchTerm() string {
	if s == GitHubItemStateMerged {
		return "is:merged"
	}

	return "state:" + strings.ToLower(string(s))
}

// GitHubSearchFilter describes what GitHub should return before any client-side
// matching runs: which repositories or organizations to look in, which item
// types to consider, and how to narrow within them. It is rendered into a GitHub
// search query string.
type GitHubSearchFilter struct {
	Repositories   []GitHubRepository
	Organizations  []string
	Types          []GitHubItemType
	AnyLabels      []string
	RequiredLabels []string
	States         []string
}

// quoteSearchTerm wraps a value in quotes when it holds characters that would
// otherwise terminate the qualifier, or, in the case of a comma, split one value
// into several: comma is the OR separator inside a comma-joined label:
// qualifier, so an unquoted comma silently widens the search.
func quoteSearchTerm(s string) string {
	if !strings.ContainsAny(s, " \t\",") {
		return s
	}

	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}

// asSearchQuery renders the filter as a GitHub search query.
//
// Repeated scope qualifiers are ORed by GitHub, including when repo: and org:
// are mixed, so every scope belongs in one query rather than one query each.
//
// Labels use both forms deliberately: values comma-joined inside one label:
// qualifier are ORed, and repeated label: qualifiers are ANDed. So AnyLabels
// becomes one qualifier and each RequiredLabels entry becomes its own.
//
// Anything this cannot express server-side is left to the Matchinator, which is
// why states beyond a single value are not rendered here.
func (f *GitHubSearchFilter) asSearchQuery() string {
	terms := []string{}

	for _, r := range f.Repositories {
		terms = append(terms, "repo:"+quoteSearchTerm(r.Owner+"/"+r.Name))
	}

	for _, o := range f.Organizations {
		terms = append(terms, "org:"+quoteSearchTerm(o))
	}

	// search type ISSUE spans both kinds, so a lone type is a real narrowing and
	// naming both is the same as saying nothing.
	if len(f.Types) == 1 {
		switch f.Types[0] {
		case GitHubItemIssue:
			terms = append(terms, "is:issue")
		case GitHubItemPullRequest:
			terms = append(terms, "is:pr")
		}
	}

	// An empty value renders a bare label: qualifier, which GitHub cannot act on,
	// so the whole query would fail or match nothing every poll.
	quoted := make([]string, 0, len(f.AnyLabels))

	for _, l := range f.AnyLabels {
		if l == "" {
			continue
		}

		quoted = append(quoted, quoteSearchTerm(l))
	}

	if len(quoted) > 0 {
		terms = append(terms, "label:"+strings.Join(quoted, ","))
	}

	for _, l := range f.RequiredLabels {
		if l == "" {
			continue
		}

		terms = append(terms, "label:"+quoteSearchTerm(l))
	}

	// Repeated state qualifiers AND into a contradiction, so only a lone state
	// narrows the query; the matcher enforces the rest.
	if len(f.States) == 1 {
		terms = append(terms, GitHubItemState(f.States[0]).asSearchTerm())
	}

	return strings.Join(terms, " ")
}

// GitHubItem is an issue or a pull request. It provides a common format for
// label selectors.
type GitHubItem struct {
	Type         GitHubItemType             `json:"type"`
	Repo         GitHubRepository           `json:"repo"`
	ID           githubv4.ID                `json:"id"`
	Author       GitHubActor                `json:"author"`
	Body         string                     `json:"body"`
	Labels       []string                   `json:"labels"`
	Number       int32                      `json:"number"`
	State        GitHubItemState            `json:"state"`
	Subscription githubv4.SubscriptionState `json:"Subscription"`
	Title        string                     `json:"title"`
	UpdatedAt    time.Time                  `json:"updatedAt"`
}

// NewTestGitHubItem creates a new instance of a GitHubItem with pre-populated fields. It can be used in unit tests.
func NewTestGitHubItem() *GitHubItem {
	return &GitHubItem{
		Type: GitHubItemIssue,
		Repo: GitHubRepository{
			Owner: "owner",
			Name:  "repo",
		},
		Author: GitHubActor{
			Login: "actor",
		},
		Body:         "issue body",
		Labels:       []string{"a/test/label", "another/label"},
		Number:       1,
		State:        GitHubItemStateOpen,
		Subscription: "UNSUBSCRIBED",
		Title:        "a test issue",
		UpdatedAt:    time.Now(),
	}
}

func (i GitHubItem) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("type", string(i.Type)),
		slog.Any("repo", i.Repo.LogValue()),
		slog.Any("id", i.ID),
		slog.Any("author", i.Author.LogValue()),
		slog.String("body", i.Body),
		slog.Int("number", int(i.Number)),
		slog.String("state", string(i.State)),
		slog.String("subscription", string(i.Subscription)),
		slog.String("title", i.Title),
		slog.Time("updatedAt", i.UpdatedAt),
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

// gitHubOrganizationQuery checks an organization exists.
//
// It goes through repositoryOwner rather than organization because
// Organization.login requires the read:org scope, which nothing else here needs:
// using org: as a search qualifier does not. repositoryOwner resolves users too,
// so __typename is what confirms the login really is an organization.
type gitHubOrganizationQuery struct {
	RepositoryOwner struct {
		Typename githubv4.String `graphql:"__typename"`
		Login    githubv4.String
	} `graphql:"repositoryOwner(login: $login)"`
}

func (q gitHubOrganizationQuery) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("typename", string(q.RepositoryOwner.Typename)),
		slog.String("login", string(q.RepositoryOwner.Login)),
	)
}

type gitHubOrganizationQueryVars struct {
	Login githubv4.String
}

func (v gitHubOrganizationQueryVars) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("login", string(v.Login)),
	)
}

func (v *gitHubOrganizationQueryVars) AsMap() map[string]any {
	return map[string]any{
		"login": v.Login,
	}
}

// searchResultLimit is the ceiling GitHub's search API paginates to, regardless
// of how many items actually matched.
const searchResultLimit = 1000

// searchNodeLabels is the inline label set shared by both fragments. It is not
// paginated, so an item carrying more than this loses the tail. Filtering does
// not depend on it (GitHub applies requiredLabels), but selectors and reported
// labels do.
type searchNodeLabels struct {
	Nodes []struct {
		Name githubv4.String
	}
}

func (l searchNodeLabels) names() []string {
	names := make([]string, 0, len(l.Nodes))
	for _, n := range l.Nodes {
		names = append(names, string(n.Name))
	}

	return names
}

// searchNodeRepository is the repository each fragment reports itself under. It
// comes off the node rather than the caller so one search can span scopes.
type searchNodeRepository struct {
	Name  githubv4.String
	Owner struct {
		Login githubv4.String
	}
}

func (r searchNodeRepository) asGitHubRepository() GitHubRepository {
	return GitHubRepository{
		Owner: string(r.Owner.Login),
		Name:  string(r.Name),
	}
}

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
	Labels             searchNodeLabels `graphql:"labels(first: 100)"`
	Repository         searchNodeRepository
}

// asGitHubItem converts the node into a GitHubItem. It returns nil for a zero
// node, which is what an unmatched inline fragment in the result union decodes to.
func (n *gitHubIssueNode) asGitHubItem() *GitHubItem {
	if n.ID == nil || n.ID == "" {
		return nil
	}

	return &GitHubItem{
		Type:         GitHubItemIssue,
		Repo:         n.Repository.asGitHubRepository(),
		ID:           n.ID,
		Author:       n.Author,
		Body:         string(n.BodyText),
		Labels:       n.Labels.names(),
		Number:       int32(n.Number),
		State:        GitHubItemState(n.State),
		Subscription: n.ViewerSubscription,
		Title:        string(n.Title),
		UpdatedAt:    n.UpdatedAt.Time,
	}
}

// gitHubPullRequestNode is the `... on PullRequest` fragment of a search result.
// It mirrors gitHubIssueNode except for the state enum, which adds MERGED.
type gitHubPullRequestNode struct {
	Author             GitHubActor
	ID                 githubv4.ID
	Number             githubv4.Int
	Title              githubv4.String
	BodyText           githubv4.String
	State              githubv4.PullRequestState
	UpdatedAt          githubv4.DateTime
	ViewerSubscription githubv4.SubscriptionState
	Labels             searchNodeLabels `graphql:"labels(first: 100)"`
	Repository         searchNodeRepository
}

func (n *gitHubPullRequestNode) asGitHubItem() *GitHubItem {
	if n.ID == nil || n.ID == "" {
		return nil
	}

	return &GitHubItem{
		Type:         GitHubItemPullRequest,
		Repo:         n.Repository.asGitHubRepository(),
		ID:           n.ID,
		Author:       n.Author,
		Body:         string(n.BodyText),
		Labels:       n.Labels.names(),
		Number:       int32(n.Number),
		State:        GitHubItemState(n.State),
		Subscription: n.ViewerSubscription,
		Title:        string(n.Title),
		UpdatedAt:    n.UpdatedAt.Time,
	}
}

// gitHubSearchNode is one result of a search. type ISSUE spans issues and pull
// requests, so both fragments are always requested.
//
// The concrete type has to come from __typename. GraphQL returns a flat object
// for the matching fragment, and the decoder assigns by field name, so an Issue
// and a PullRequest selecting the same field names both populate *both* structs.
// Deciding by which one looks filled in reports every pull request as an issue.
type gitHubSearchNode struct {
	Typename    githubv4.String       `graphql:"__typename"`
	Issue       gitHubIssueNode       `graphql:"... on Issue"`
	PullRequest gitHubPullRequestNode `graphql:"... on PullRequest"`
}

// asGitHubItem converts whichever fragment __typename names.
func (n *gitHubSearchNode) asGitHubItem() *GitHubItem {
	switch n.Typename {
	case "Issue":
		return n.Issue.asGitHubItem()
	case "PullRequest":
		return n.PullRequest.asGitHubItem()
	default:
		return nil
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
		Nodes []gitHubSearchNode
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

	// CheckOrganization checks if the given organization exists.
	CheckOrganization(ctx context.Context, login string) error

	// ListIssues returns the issues matching the given filter across every
	// repository and organization the filter scopes to.
	ListIssues(
		ctx context.Context, filter *GitHubSearchFilter, matcher Matchinator,
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

	// CheckOrganizationRequests holds the parameters to calls to CheckOrganization.
	CheckOrganizationRequests []string

	// CheckOrganizationError holds the returned error for CheckOrganization.
	CheckOrganizationError error

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

func (t *MockGitHubinator) CheckOrganization(ctx context.Context, login string) error {
	t.CheckOrganizationRequests = append(t.CheckOrganizationRequests, login)

	return t.CheckOrganizationError
}

func (t *MockGitHubinator) ListIssues(
	ctx context.Context, filter *GitHubSearchFilter, matcher Matchinator,
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
		CheckRepositoryRequests:   []GitHubRepository{},
		CheckRepositoryError:      nil,
		CheckOrganizationRequests: []string{},
		CheckOrganizationError:    nil,
		WhoAmIRequests:            0,
		WhoAmIReturn:              "user",
		WhoAmIError:               nil,
		SetSubscriptionRequests:   []githubv4.ID{},
		SetSubscriptionError:      nil,
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

		return asNotFoundError(err)
	}

	queryLogger.Debug("response on check repository query", "result", query)

	return nil
}

func (gh *gitHubinator) CheckOrganization(ctx context.Context, login string) error {
	if gh.client == nil {
		gh.setupClient()
	}

	query := gitHubOrganizationQuery{}

	vars := gitHubOrganizationQueryVars{
		Login: githubv4.String(login),
	}

	queryLogger := gh.logger.With("vars", vars)
	queryLogger.Debug("executing check organization query")

	MetricOrgQueryTotal.Inc()

	err := gh.client.Query(ctx, &query, vars.AsMap())
	if err != nil {
		queryLogger.Debug("got error on check organization query", LogKeyError, err)

		MetricOrgQueryErrorTotal.Inc()

		return asNotFoundError(err)
	}

	queryLogger.Debug("response on check organization query", "result", query)

	// A login that resolves to nothing comes back as a null owner, not an error,
	// so this is the not-found case rather than a failure to ask.
	if len(query.RepositoryOwner.Login) == 0 {
		return &GitHubNotFoundError{
			err: fmt.Errorf("no such organization '%s'", login),
		}
	}

	if query.RepositoryOwner.Typename != "Organization" {
		return fmt.Errorf(
			"'%s' is a %s, not an organization; use repos to watch a user's repositories",
			login, query.RepositoryOwner.Typename,
		)
	}

	return gh.probeOrganizationAccess(ctx, login)
}

// gitHubRESTBaseURL is the REST host used by probeOrganizationAccess. Overridden
// in tests to point at an httptest server.
var gitHubRESTBaseURL = "https://api.github.com"

// probeOrganizationAccess asks REST whether a policy refuses this token for the
// organization, because GraphQL will not say.
//
// A token that is not SSO-authorized for a SAML-protected org gets zeros rather
// than errors from every GraphQL field that matters: search returns
// issueCount 0, organization.repositories returns totalCount 0, and
// repositoryOwner resolves normally. So an org-scoped watch passes validation,
// polls forever, matches nothing and reports no error — the exact blindness
// watchinator exists to avoid. REST answers with 403 and names the fix.
//
// What a 200 does not prove: org metadata is public, so an anonymous request
// gets 200 even for an org whose every repository is private. A token that is
// SSO-authorized but lacks the scope to see the content therefore still passes
// here and still polls zero items. Catching that would mean counting visible
// repositories, which cannot be told apart from an org that legitimately has
// none — a false failure on a valid config is worse than this gap.
//
// Only a verdict about this organization fails the check: 403 (a policy refuses
// the token) and 404 (the org is invisible to it). Everything else means the
// question could not be asked, which is not an answer, so the check passes
// rather than making validation depend on REST being up. That includes 401:
// credentials that bad already failed WhoAmI and the GraphQL query above, and
// reporting them as an organization problem would point at the wrong thing.
func (gh *gitHubinator) probeOrganizationAccess(ctx context.Context, login string) error {
	probeLogger := gh.logger.With("login", login)

	endpoint := gitHubRESTBaseURL + "/orgs/" + url.PathEscape(login)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		probeLogger.Debug("unable to build organization access probe", LogKeyError, err)

		return nil
	}

	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := oauth2.NewClient(ctx, gh.token).Do(req)
	if err != nil {
		probeLogger.Debug("organization access probe failed to send", LogKeyError, err)

		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		return nil
	}

	// GitHub explains the refusal in the body, including which action would fix
	// it, so pass its wording through rather than inventing a summary.
	var body struct {
		Message string `json:"message"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		probeLogger.Debug("unable to decode organization access probe", LogKeyError, err)
	}

	if body.Message == "" {
		body.Message = resp.Status
	}

	probeLogger.Debug("organization access probe denied", "status", resp.StatusCode)

	switch resp.StatusCode {
	case http.StatusNotFound:
		return &GitHubNotFoundError{
			err: fmt.Errorf("organization '%s' is not visible to this token: %s", login, body.Message),
		}
	case http.StatusForbidden:
		return fmt.Errorf(
			"organization '%s' cannot be read by this token: %s"+
				" (GraphQL would report no items rather than failing, so this watch is rejected here)",
			login, body.Message,
		)
	default:
		return nil
	}
}

func (gh *gitHubinator) ListIssues(
	ctx context.Context, filter *GitHubSearchFilter, matcher Matchinator,
) ([]*GitHubItem, error) {
	if gh.client == nil {
		gh.setupClient()
	}

	query := &gitHubSearchQuery{}

	vars := &gitHubSearchQueryVars{
		Q:            githubv4.String(filter.asSearchQuery()),
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
				item := query.Search.Nodes[i].asGitHubItem()
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
