package pkg

import (
	"context"
	"errors"
	"testing"

	"github.com/shurcooL/githubv4"
	"gotest.tools/v3/assert"
)

func TestSubscribeActionSubscribesToIssueIfNeeded(t *testing.T) {
	gh := NewMockGitHubinator()
	a := NewSubscribeAction(gh)
	item := *NewTestGitHubItem()
	ctx := context.Background()
	logger := NewLogger()

	// Subscribed issue, no error = no call to SetSubscription, no error
	item.Subscription = githubv4.SubscriptionStateSubscribed
	assert.NilError(t, a.Handle(ctx, item, logger), "unexpected error handling already subscribed issue")
	assert.Assert(
		t, len(gh.SetSubscriptionRequests) == 0,
		"expected no call to SetSubscription on already subscribed issue with no returned error",
	)

	// Subscribed issue, error = no call to SetSubscription, no error
	gh.SetSubscriptionError = errors.New("my test error")

	assert.NilError(
		t, a.Handle(ctx, item, logger),
		"unexpected error handling already subscribed issue with returned error",
	)

	// Unsubscribed issue, error = call to SetSubscription, error
	item.Subscription = githubv4.SubscriptionStateUnsubscribed
	assert.ErrorContains(t, a.Handle(ctx, item, logger), "my test error")
	assert.Assert(
		t, len(gh.SetSubscriptionRequests) == 1,
		"expected call to SetSubscription on unsubscribed issue with returned error",
	)

	gh.SetSubscriptionError = nil

	assert.NilError(t, a.Handle(ctx, item, logger), "unexpected error handling unsubscribed issue")
	assert.Assert(
		t, len(gh.SetSubscriptionRequests) == 2,
		"expected call to SetSubscription on unsubscribed issue with no returned error",
	)
}

func TestEmailActionSendsEmail(t *testing.T) {
	e := NewMockEmailinator()
	toAddress := "test@example.com"
	a := NewEmailAction(e, toAddress)
	item := *NewTestGitHubItem()
	ctx := context.Background()
	logger := NewLogger()

	// Item with no mocked errors, should be successful
	assert.NilError(t, a.Handle(ctx, item, logger), "unexpected error sending email for item")

	// Item with a mocked error, should fail
	e.SendError = errors.New("my test error")

	assert.ErrorContains(t, a.Handle(ctx, item, logger), "my test error")
}
