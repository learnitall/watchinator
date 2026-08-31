package pkg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shurcooL/githubv4"
	"golang.org/x/exp/slog"
	"gotest.tools/v3/assert"
)

var (
	issueOnly = []GitHubItemType{GitHubItemIssue}
	prOnly    = []GitHubItemType{GitHubItemPullRequest}
	bothTypes = []GitHubItemType{GitHubItemIssue, GitHubItemPullRequest}
)

func TestAsSearchQueryScopesToRepoAndIssues(t *testing.T) {
	f := &GitHubSearchFilter{
		Repositories: []GitHubRepository{{Owner: "learnitall", Name: "watchinator"}},
		Types:        issueOnly,
	}

	assert.Equal(t, f.asSearchQuery(), "repo:learnitall/watchinator is:issue")
}

// search type ISSUE spans both kinds, so naming both types is the same as naming
// neither and no type qualifier should be emitted.
func TestAsSearchQueryOmitsTypeWhenBothWanted(t *testing.T) {
	repos := []GitHubRepository{{Owner: "o", Name: "n"}}

	both := &GitHubSearchFilter{Repositories: repos, Types: bothTypes}
	assert.Equal(t, both.asSearchQuery(), "repo:o/n")

	none := &GitHubSearchFilter{Repositories: repos}
	assert.Equal(t, none.asSearchQuery(), "repo:o/n")
}

func TestAsSearchQueryScopesToPullRequests(t *testing.T) {
	f := &GitHubSearchFilter{
		Repositories: []GitHubRepository{{Owner: "o", Name: "n"}},
		Types:        prOnly,
	}

	assert.Equal(t, f.asSearchQuery(), "repo:o/n is:pr")
}

// Repeated scope qualifiers are ORed by GitHub, including repo: mixed with org:,
// which is why every scope goes into a single query.
func TestAsSearchQueryCombinesEveryScope(t *testing.T) {
	f := &GitHubSearchFilter{
		Repositories: []GitHubRepository{
			{Owner: "golang", Name: "go"},
			{Owner: "learnitall", Name: "watchinator"},
		},
		Organizations: []string{"ngrok", "kubernetes"},
	}

	assert.Equal(
		t,
		f.asSearchQuery(),
		"repo:golang/go repo:learnitall/watchinator org:ngrok org:kubernetes",
	)
}

func TestAsSearchQueryScopesToOrgAlone(t *testing.T) {
	f := &GitHubSearchFilter{Organizations: []string{"ngrok"}, Types: issueOnly}

	assert.Equal(t, f.asSearchQuery(), "org:ngrok is:issue")
}

func TestAsSearchQueryOrsLabels(t *testing.T) {
	f := &GitHubSearchFilter{
		Repositories: []GitHubRepository{{Owner: "o", Name: "n"}},
		AnyLabels:    []string{"bug", "needs triage"},
	}

	// Comma-joined values inside one qualifier are ORed by GitHub; the label with
	// a space has to be quoted or it would terminate the qualifier early.
	assert.Equal(t, f.asSearchQuery(), `repo:o/n label:bug,"needs triage"`)
}

func TestAsSearchQueryOnlyConstrainsASingleState(t *testing.T) {
	repos := []GitHubRepository{{Owner: "o", Name: "n"}}

	single := &GitHubSearchFilter{Repositories: repos, States: []string{"OPEN"}}
	assert.Equal(t, single.asSearchQuery(), "repo:o/n state:open")

	// Two state qualifiers would AND into a contradiction, so the matcher takes
	// over instead.
	both := &GitHubSearchFilter{Repositories: repos, States: []string{"OPEN", "CLOSED"}}
	assert.Equal(t, both.asSearchQuery(), "repo:o/n")
}

// `state:merged` is not valid syntax: GitHub matches nothing rather than
// erroring, so MERGED has to render through `is:`.
func TestAsSearchQueryRendersMergedThroughIs(t *testing.T) {
	f := &GitHubSearchFilter{
		Repositories: []GitHubRepository{{Owner: "o", Name: "n"}},
		Types:        prOnly,
		States:       []string{"MERGED"},
	}

	assert.Equal(t, f.asSearchQuery(), "repo:o/n is:pr is:merged")
}

func TestIssueNodeAsGitHubItemMapsFields(t *testing.T) {
	n := &gitHubIssueNode{
		Author:             GitHubActor{Login: "someone"},
		ID:                 githubv4.ID("abc123"),
		Number:             7,
		Title:              "a title",
		BodyText:           "a body",
		State:              githubv4.IssueStateOpen,
		ViewerSubscription: githubv4.SubscriptionStateUnsubscribed,
	}
	n.Labels.Nodes = []struct{ Name githubv4.String }{{Name: "bug"}, {Name: "wontfix"}}
	n.Repository.Name = "watchinator"
	n.Repository.Owner.Login = "learnitall"

	item := n.asGitHubItem()
	assert.Assert(t, item != nil)
	assert.Equal(t, item.Type, GitHubItemIssue)
	// The repo comes off the node, not the caller, so one search can span scopes.
	assert.Equal(t, item.Repo.Owner, "learnitall")
	assert.Equal(t, item.Repo.Name, "watchinator")
	assert.Equal(t, item.Number, int32(7))
	assert.Equal(t, item.Title, "a title")
	// Body arrives inline now, rather than needing a follow-up query per item.
	assert.Equal(t, item.Body, "a body")
	assert.Equal(t, item.State, GitHubItemStateOpen)
	assert.DeepEqual(t, item.Labels, []string{"bug", "wontfix"})
}

func TestPullRequestNodeAsGitHubItemCarriesMergedState(t *testing.T) {
	n := &gitHubPullRequestNode{
		Author:   GitHubActor{Login: "someone"},
		ID:       githubv4.ID("pr1"),
		Number:   9,
		Title:    "a pr",
		BodyText: "a pr body",
		State:    githubv4.PullRequestStateMerged,
	}
	n.Repository.Name = "watchinator"
	n.Repository.Owner.Login = "learnitall"

	item := n.asGitHubItem()
	assert.Assert(t, item != nil)
	assert.Equal(t, item.Type, GitHubItemPullRequest)
	assert.Equal(t, item.Number, int32(9))
	// MERGED has no equivalent in githubv4.IssueState, which is why items carry
	// their own state type.
	assert.Equal(t, item.State, GitHubItemStateMerged)
}

// GraphQL returns a flat object for a union node and the decoder assigns by
// field name, so a PullRequest result populates the Issue struct too: both
// fragments select the same field names. Only __typename distinguishes them, and
// picking the first non-zero fragment reported every PR as an issue. A
// hand-built node cannot catch a regression here, because the bug lives in what
// the decoder does to the wire payload, so this drives real JSON through it.
func TestSearchNodeUsesTypenameNotWhichFragmentLooksFilled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":{"search":{"issueCount":2,`+
			`"pageInfo":{"endCursor":"","hasNextPage":false},"nodes":[`+
			`{"__typename":"PullRequest","id":"PR_1","number":9,"state":"MERGED",`+
			`"title":"a pr","bodyText":"b","repository":{"name":"n","owner":{"login":"o"}}},`+
			`{"__typename":"Issue","id":"I_1","number":1,"state":"OPEN",`+
			`"title":"an issue","bodyText":"b","repository":{"name":"n","owner":{"login":"o"}}}`+
			`]}}}`)
	}))
	defer srv.Close()

	vars := &gitHubSearchQueryVars{
		Q:            githubv4.String("repo:o/n"),
		Type:         githubv4.SearchTypeIssue,
		SearchCursor: (*githubv4.String)(nil),
		N:            100,
	}

	query := gitHubSearchQuery{}
	err := githubv4.NewEnterpriseClient(srv.URL, srv.Client()).
		Query(context.Background(), &query, vars.AsMap())
	assert.NilError(t, err)
	assert.Equal(t, len(query.Search.Nodes), 2)

	pr := query.Search.Nodes[0].asGitHubItem()
	assert.Assert(t, pr != nil)
	assert.Equal(t, pr.Type, GitHubItemPullRequest)
	// Taking the wrong fragment would also report the wrong state.
	assert.Equal(t, pr.State, GitHubItemStateMerged)

	issue := query.Search.Nodes[1].asGitHubItem()
	assert.Assert(t, issue != nil)
	assert.Equal(t, issue.Type, GitHubItemIssue)
	assert.Equal(t, issue.State, GitHubItemStateOpen)
}

// search type ISSUE can also return other node kinds; anything unrecognised is
// dropped rather than guessed at.
func TestSearchNodeDropsUnknownTypename(t *testing.T) {
	node := &gitHubSearchNode{Typename: "Discussion"}
	node.Issue.ID = githubv4.ID("x")

	assert.Assert(t, node.asGitHubItem() == nil)
	assert.Assert(t, (&gitHubSearchNode{}).asGitHubItem() == nil)
}

// An inline fragment that does not match the node's concrete type decodes to a
// zero value, which must not be mistaken for a real result.
func TestIssueNodeAsGitHubItemRejectsZeroNode(t *testing.T) {
	assert.Assert(t, (&gitHubIssueNode{}).asGitHubItem() == nil)
}

// The search document is assembled by githubv4 from the struct, so a dropped
// fragment would not fail to compile: PR results would just silently stop
// arriving. Assert both fragments and the variable types reach the wire.
func TestSearchQueryRequestsBothFragments(t *testing.T) {
	var body []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":{"search":{"issueCount":0,`+
			`"pageInfo":{"endCursor":"","hasNextPage":false},"nodes":[]}}}`)
	}))
	defer srv.Close()

	vars := &gitHubSearchQueryVars{
		Q:            githubv4.String("repo:o/n"),
		Type:         githubv4.SearchTypeIssue,
		SearchCursor: (*githubv4.String)(nil),
		N:            100,
	}

	query := &gitHubSearchQuery{}
	err := githubv4.NewEnterpriseClient(srv.URL, srv.Client()).
		Query(context.Background(), &query, vars.AsMap())
	assert.NilError(t, err)

	var parsed struct {
		Query string `json:"query"`
	}
	assert.NilError(t, json.Unmarshal(body, &parsed))

	assert.Assert(t, strings.Contains(parsed.Query, "... on Issue{"), parsed.Query)
	assert.Assert(t, strings.Contains(parsed.Query, "... on PullRequest{"), parsed.Query)
	// Without __typename the two fragments are indistinguishable once decoded.
	assert.Assert(t, strings.Contains(parsed.Query, "__typename"), parsed.Query)
	assert.Assert(t, strings.Contains(parsed.Query, "$searchType:SearchType!"), parsed.Query)
	// Labels and body come inline; a regression to per-item queries would show up
	// as these disappearing from the document.
	assert.Assert(t, strings.Contains(parsed.Query, "labels(first: 100)"), parsed.Query)
	assert.Assert(t, strings.Contains(parsed.Query, "bodyText"), parsed.Query)
}

// Repeated label: qualifiers are ANDed by GitHub while comma-joined values in one
// qualifier are ORed, so requiredLabels and searchLabels render differently and
// compose as (any of these) AND (each of those).
func TestAsSearchQueryAndsRequiredLabels(t *testing.T) {
	f := &GitHubSearchFilter{
		Repositories:   []GitHubRepository{{Owner: "o", Name: "n"}},
		RequiredLabels: []string{"kind/bug", "sig/node"},
	}

	assert.Equal(t, f.asSearchQuery(), "repo:o/n label:kind/bug label:sig/node")
}

func TestAsSearchQueryCombinesAnyAndRequiredLabels(t *testing.T) {
	f := &GitHubSearchFilter{
		Repositories:   []GitHubRepository{{Owner: "o", Name: "n"}},
		AnyLabels:      []string{"bug", "regression"},
		RequiredLabels: []string{"needs triage"},
	}

	assert.Equal(
		t, f.asSearchQuery(),
		`repo:o/n label:bug,regression label:"needs triage"`,
	)
}

// Comma is the OR separator inside a comma-joined label: qualifier, so a label
// that holds one has to be quoted or it becomes two ORed labels: a wider search
// than the one that was configured.
func TestAsSearchQueryQuotesLabelsContainingCommas(t *testing.T) {
	repos := []GitHubRepository{{Owner: "o", Name: "n"}}

	anyOf := &GitHubSearchFilter{Repositories: repos, AnyLabels: []string{"kind/bug,urgent"}}
	assert.Equal(t, anyOf.asSearchQuery(), `repo:o/n label:"kind/bug,urgent"`)

	required := &GitHubSearchFilter{
		Repositories: repos, RequiredLabels: []string{"kind/bug,urgent"},
	}
	assert.Equal(t, required.asSearchQuery(), `repo:o/n label:"kind/bug,urgent"`)
}

// Comma plays two roles in one qualifier: the separator between ORed values, and
// a literal inside a single value. Quoting is per value and the join happens
// after, so the separator is never quoted. GitHub accepts a quoted member of a
// comma list and treats it identically to an unquoted one.
func TestAsSearchQueryQuotesOnlyTheValueNotTheSeparator(t *testing.T) {
	f := &GitHubSearchFilter{
		Repositories: []GitHubRepository{{Owner: "o", Name: "n"}},
		AnyLabels:    []string{"kind/bug,urgent", "sig/node"},
	}

	// One label literally named "kind/bug,urgent", ORed with "sig/node" — not
	// three labels, and not one label named `kind/bug,urgent,sig/node`.
	assert.Equal(t, f.asSearchQuery(), `repo:o/n label:"kind/bug,urgent",sig/node`)
}

// A bare label: qualifier is not a query GitHub can act on, so an empty value is
// dropped rather than rendered.
func TestAsSearchQueryDropsEmptyLabels(t *testing.T) {
	repos := []GitHubRepository{{Owner: "o", Name: "n"}}

	onlyEmpty := &GitHubSearchFilter{Repositories: repos, AnyLabels: []string{""}}
	assert.Equal(t, onlyEmpty.asSearchQuery(), "repo:o/n")

	someEmpty := &GitHubSearchFilter{Repositories: repos, AnyLabels: []string{"bug", ""}}
	assert.Equal(t, someEmpty.asSearchQuery(), "repo:o/n label:bug")

	required := &GitHubSearchFilter{Repositories: repos, RequiredLabels: []string{"", "x"}}
	assert.Equal(t, required.asSearchQuery(), "repo:o/n label:x")
}

// checkOrgAgainst stands up a GraphQL server returning the given repositoryOwner
// payload, so CheckOrganization's branches can be exercised without a PAT.
func checkOrgAgainst(t *testing.T, ownerJSON string) error {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"data":{"repositoryOwner":%s}}`, ownerJSON)
	}))
	defer srv.Close()

	gh := &gitHubinator{
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		client: githubv4.NewEnterpriseClient(srv.URL, srv.Client()),
	}

	return gh.CheckOrganization(context.Background(), "some-login")
}

// A login that resolves to nothing comes back as a null owner rather than an
// error, so a typo would otherwise validate clean.
func TestCheckOrganizationRejectsMissingOwner(t *testing.T) {
	assert.ErrorContains(t, checkOrgAgainst(t, `null`), "no such organization")
}

// repositoryOwner also resolves users, but org: as a search qualifier does not
// match them, so a username here would silently return nothing.
func TestCheckOrganizationRejectsUser(t *testing.T) {
	err := checkOrgAgainst(t, `{"__typename":"User","login":"some-login"}`)
	assert.ErrorContains(t, err, "is a User, not an organization")
}

func TestCheckOrganizationAcceptsOrganization(t *testing.T) {
	err := checkOrgAgainst(t, `{"__typename":"Organization","login":"some-login"}`)
	assert.NilError(t, err)
}

// GitHubNotFoundError used to be declared `type GitHubNotFoundError error`, an
// interface with error's method set, so every error matched a type switch on it
// and check reported "does not exist" for auth and network failures too.
func TestNotFoundErrorDoesNotSwallowUnrelatedErrors(t *testing.T) {
	var notFound *GitHubNotFoundError

	transient := errors.New("some transient network failure")
	assert.Assert(t, !errors.As(asNotFoundError(transient), &notFound))

	// shurcooL/graphql keeps only the message, so this string is the only signal
	// GitHub's type: NOT_FOUND leaves behind.
	missing := errors.New("Could not resolve to a Repository with the name 'o/n'.")
	wrapped := asNotFoundError(missing)
	assert.Assert(t, errors.As(wrapped, &notFound))
	// The original message has to survive for check to print something useful.
	assert.Equal(t, wrapped.Error(), missing.Error())
	assert.Equal(t, errors.Unwrap(wrapped), missing)

	assert.Assert(t, asNotFoundError(nil) == nil)
}

// A login that resolves to nothing is a genuine not-found, so it must carry the
// same type as a GraphQL NOT_FOUND rather than being an opaque error.
func TestCheckOrganizationMissingOwnerIsNotFound(t *testing.T) {
	var notFound *GitHubNotFoundError
	assert.Assert(t, errors.As(checkOrgAgainst(t, `null`), &notFound))

	// A user is a real owner, just the wrong kind: not a not-found.
	err := checkOrgAgainst(t, `{"__typename":"User","login":"some-login"}`)
	assert.Assert(t, !errors.As(err, &notFound))
}
