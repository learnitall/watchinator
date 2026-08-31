package pkg

import (
	"testing"

	"github.com/shurcooL/githubv4"
	"gotest.tools/v3/assert"
)

func TestAsSearchQueryScopesToRepoAndIssues(t *testing.T) {
	f := &GitHubSearchFilter{
		Repositories: []GitHubRepository{{Owner: "learnitall", Name: "watchinator"}},
	}

	assert.Equal(t, f.asSearchQuery(), "repo:learnitall/watchinator is:issue")
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
		"repo:golang/go repo:learnitall/watchinator org:ngrok org:kubernetes is:issue",
	)
}

func TestAsSearchQueryScopesToOrgAlone(t *testing.T) {
	f := &GitHubSearchFilter{Organizations: []string{"ngrok"}}

	assert.Equal(t, f.asSearchQuery(), "org:ngrok is:issue")
}

func TestAsSearchQueryOrsLabels(t *testing.T) {
	f := &GitHubSearchFilter{
		Repositories: []GitHubRepository{{Owner: "o", Name: "n"}},
		Labels:       []string{"bug", "needs triage"},
	}

	// Comma-joined values inside one qualifier are ORed by GitHub; the label with
	// a space has to be quoted or it would terminate the qualifier early.
	assert.Equal(t, f.asSearchQuery(), `repo:o/n is:issue label:bug,"needs triage"`)
}

func TestAsSearchQueryOnlyConstrainsASingleState(t *testing.T) {
	repos := []GitHubRepository{{Owner: "o", Name: "n"}}

	single := &GitHubSearchFilter{Repositories: repos, States: []string{"OPEN"}}
	assert.Equal(t, single.asSearchQuery(), "repo:o/n is:issue state:open")

	// Every state named is the same as no constraint, and two state qualifiers
	// would AND into a contradiction.
	both := &GitHubSearchFilter{Repositories: repos, States: []string{"OPEN", "CLOSED"}}
	assert.Equal(t, both.asSearchQuery(), "repo:o/n is:issue")
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
	assert.DeepEqual(t, item.Labels, []string{"bug", "wontfix"})
}

// An inline fragment that does not match the node's concrete type decodes to a
// zero value, which must not be mistaken for a real result.
func TestIssueNodeAsGitHubItemRejectsZeroNode(t *testing.T) {
	assert.Assert(t, (&gitHubIssueNode{}).asGitHubItem() == nil)
}
