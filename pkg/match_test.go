package pkg

import (
	"regexp"
	"testing"

	"gotest.tools/v3/assert"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
)

func TestSelectorAsGitHubItemMatcherCreatesWorkingMatcher(t *testing.T) {
	item := NewTestGitHubItem()
	item.Type = "a_test_type"

	req, err := labels.NewRequirement(
		"type", selection.Equals, []string{"a_test_type"},
	)
	assert.NilError(t, err)

	matcher := SelectorAsGitHubItemMatcher(labels.NewSelector().Add(*req))
	assert.Equal(t, matcher.Matcher(item), true)

	item.Type = "another_test_type"
	assert.Equal(t, matcher.Matcher(item), false)
}

func TestBodyRegexAsGitHubItemMatcherCreatesWorkingMatcher(t *testing.T) {
	item := NewTestGitHubItem()
	item.Body = "this is my issue description"

	matcher := BodyRegexAsGitHubItemMatcher(regexp.MustCompile("^this"))
	assert.Equal(t, matcher.Matcher(item), true)

	item.Body = "a description"
	assert.Equal(t, matcher.Matcher(item), false)
}

func TestTitleRegexAsGitHubItemMatcherCreatesWorkingMatcher(t *testing.T) {
	item := NewTestGitHubItem()
	item.Title = "this is my issue title"

	matcher := TitleRegexAsGitHubItemMatcher(regexp.MustCompile("^this"))
	assert.Equal(t, matcher.Matcher(item), true)

	item.Title = "a title"
	assert.Equal(t, matcher.Matcher(item), false)
}

func TestRegexMatchersAreCaseSensitive(t *testing.T) {
	item := NewTestGitHubItem()
	item.Body = "Can the watchinator subscribe to this issue?"
	item.Title = "This is a Test Issue"

	match := func(m GitHubItemMatcher) bool { return m.Matcher(item) }

	assert.Equal(t, match(BodyRegexAsGitHubItemMatcher(regexp.MustCompile("^Can the"))), true)
	assert.Equal(t, match(BodyRegexAsGitHubItemMatcher(regexp.MustCompile("^can the"))), false)

	assert.Equal(t, match(TitleRegexAsGitHubItemMatcher(regexp.MustCompile("^This is"))), true)
	assert.Equal(t, match(TitleRegexAsGitHubItemMatcher(regexp.MustCompile("^this is"))), false)

	// Case insensitivity is opt-in, through the regex rather than the matcher.
	assert.Equal(t, match(BodyRegexAsGitHubItemMatcher(regexp.MustCompile("(?i)^can the"))), true)
	assert.Equal(t, match(TitleRegexAsGitHubItemMatcher(regexp.MustCompile("(?i)^this is"))), true)
}

func TestRequiredLabelAsGitHubItemMatcherCreatesWorkingMatcher(t *testing.T) {
	item := NewTestGitHubItem()
	item.Labels = []string{"a cool label"}

	matcher := RequiredLabelAsGitHubItemMatcher("a cool label")
	assert.Equal(t, matcher.Matcher(item), true)

	item.Labels = []string{}
	assert.Equal(t, matcher.Matcher(item), false)
}

func TestEmptyMatchinatorAlwaysMatches(t *testing.T) {
	item := NewTestGitHubItem()
	matchinator := NewMatchinator()

	matches, _ := matchinator.Matches(item)
	assert.Equal(t, matches, true)
}

func TestCanAddMatcherToMatchinator(t *testing.T) {
	item := NewTestGitHubItem()

	matchinator := NewMatchinator().WithMatchFunc(
		GitHubItemMatcher{
			Name: "never",
			Matcher: func(i *GitHubItem) bool {
				return false
			},
		},
	)

	matches, _ := matchinator.Matches(item)
	assert.Equal(t, matches, false)
}
