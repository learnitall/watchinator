package pkg

import (
	"fmt"
	"regexp"
	"strings"

	"k8s.io/apimachinery/pkg/labels"
)

// GitHubItemMatcher is used to select GitHubItem structs based on a specific criteria encoded in the Matcher field.
type GitHubItemMatcher struct {
	// Matcher is a function that takes in a GitHubItem and returns a boolean specifying if the item was matched.
	Matcher func(i *GitHubItem) bool
	// Name is used to identify the above Matcher; helpful for debug logs.
	Name string
}

// SelectorAsGitHubItemMatcher creates a new GitHubItemMatcher from the given k8s.io/apimachinery/pkg/labels.Selector.
// If the given selector matches the GitHubItem as a label set (see GitHubItemAsLabelSet), then the matcher returns
// true.
func SelectorAsGitHubItemMatcher(s labels.Selector) GitHubItemMatcher {
	return GitHubItemMatcher{
		Matcher: func(i *GitHubItem) bool {
			m := GitHubItemAsLabelSet(i)

			return s.Matches(m)
		},
		Name: fmt.Sprintf("selector: '%s'", s.String()),
	}
}

// BodyRegexAsGitHubItemMatcher creates a new GitHubItemMatcher from the given bodyRegex. If the given bodyRegex
// matches on the GitHubItem's Body field, then the matcher returns true.
func BodyRegexAsGitHubItemMatcher(bodyRegex *regexp.Regexp) GitHubItemMatcher {
	return GitHubItemMatcher{
		Matcher: func(i *GitHubItem) bool {
			return bodyRegex.Match([]byte(strings.ToLower(i.Body)))
		},
		Name: fmt.Sprintf("bodyRegex: '%s'", bodyRegex.String()),
	}
}

// TitleRegexAsGitHubItemMatcher creates a new GitHubItemMatcher from the given titleRegex. If the given titleRegex
// matches on the GitHubItem's Title field, then the matcher returns true.
func TitleRegexAsGitHubItemMatcher(titleRegex *regexp.Regexp) GitHubItemMatcher {
	return GitHubItemMatcher{
		Matcher: func(i *GitHubItem) bool {
			return titleRegex.Match([]byte(strings.ToLower(i.Title)))
		},
		Name: fmt.Sprintf("titleRegex: '%s'", titleRegex.String()),
	}
}

// RequiredLabelAsGitHubItemMatcher creates a new GitHubItemMatcher from the givne requiredLabel. If the given
// requiredLabel is present in the GitHubItem's labels, then the matcher returns true.
func RequiredLabelAsGitHubItemMatcher(requiredLabel string) GitHubItemMatcher {
	return GitHubItemMatcher{
		Matcher: func(i *GitHubItem) bool {
			for _, itemLabel := range i.Labels {
				if itemLabel == requiredLabel {
					return true
				}
			}

			return false
		},
		Name: fmt.Sprintf("requiredLabel: '%s'", requiredLabel),
	}
}

// Matchinator is used to provide custom criteria for filtering GitHubItems that may not be built in to GitHub's
// GraphQL API. It specifies a list of critieria which the GitHubItem MUST match in order to be selected. If any
// of the criteria is not met, then the GitHubItem is not matched.
// The With* builder methods always return a pointer to the same Matchinator.
type Matchinator interface {
	// WithMatchFunc as a new GitHubItemMatcher to the list of critieria.
	WithMatchFunc(match GitHubItemMatcher) Matchinator

	// WithSelectors adds the given k8s.io/apimachinery/pkg/labels.Selectors to the match criteria.
	WithSelectors(selectors ...labels.Selector) Matchinator

	// WithBodyRegexes adds the given bodyRegexes to the match critieria.
	WithBodyRegexes(bodyRegexes ...*regexp.Regexp) Matchinator

	// WithTitleRegexes adds the given titleRegexes to the match critieria.
	WithTitleRegexes(titleRegexes ...*regexp.Regexp) Matchinator

	// HasBodyRegex returns if a bodyRegex is part of the match criteria.
	HasBodyRegex() bool

	// WithRequiredLabels adds the given labels to the match criteria.
	WithRequiredLabels(labels ...string) Matchinator

	// HasRequiredLabels returns if a label is part of the match criteria.
	HasRequiredLabels() bool

	// Matches returns a boolean specifying if the GitHubItem matched the configured criteria. If no criteria is
	// configured, then this function always returns true.
	Matches(item *GitHubItem) (bool, string)
}

// matchinator is the internal implementation of the Matchinator interface.
type matchinator struct {
	matchFuncs        []GitHubItemMatcher
	hasBodyRegex      bool
	hasRequiredLabels bool
}

func (m *matchinator) WithMatchFunc(match GitHubItemMatcher) Matchinator {
	m.matchFuncs = append(m.matchFuncs, match)

	return m
}

func (m *matchinator) WithSelectors(selectors ...labels.Selector) Matchinator {
	if len(selectors) == 0 {
		return m
	}

	for _, s := range selectors {
		m.matchFuncs = append(m.matchFuncs, SelectorAsGitHubItemMatcher(s))
	}

	return m
}

func (m *matchinator) WithBodyRegexes(bodyRegexes ...*regexp.Regexp) Matchinator {
	if len(bodyRegexes) == 0 {
		return m
	}

	m.hasBodyRegex = true

	for _, r := range bodyRegexes {
		m.matchFuncs = append(m.matchFuncs, BodyRegexAsGitHubItemMatcher(r))
	}

	return m
}

func (m *matchinator) WithTitleRegexes(titleRegexes ...*regexp.Regexp) Matchinator {
	if len(titleRegexes) == 0 {
		return m
	}

	for _, r := range titleRegexes {
		m.matchFuncs = append(m.matchFuncs, TitleRegexAsGitHubItemMatcher(r))
	}

	return m
}

func (m *matchinator) HasBodyRegex() bool {
	return m.hasBodyRegex
}

func (m *matchinator) WithRequiredLabels(labels ...string) Matchinator {
	if len(labels) == 0 {
		return m
	}

	m.hasRequiredLabels = true

	for _, l := range labels {
		m.matchFuncs = append(m.matchFuncs, RequiredLabelAsGitHubItemMatcher(l))
	}

	return m
}

func (m *matchinator) HasRequiredLabels() bool {
	return m.hasRequiredLabels
}

func (m *matchinator) Matches(item *GitHubItem) (bool, string) {
	for _, m := range m.matchFuncs {
		if !m.Matcher(item) {
			return false, fmt.Sprintf("did not match %s", m.Name)
		}
	}

	return true, ""
}

// NewMatchinator creates a new Matchinator instance.
func NewMatchinator() Matchinator {
	return &matchinator{
		matchFuncs: []GitHubItemMatcher{},
	}
}
