package pkg

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/shurcooL/githubv4"
	"github.com/wneessen/go-mail"
	"golang.org/x/exp/slog"
	"golang.org/x/sync/errgroup"
)

type GitHubItemAction struct {
	Handle func(ctx context.Context, i GitHubItem, logger *slog.Logger) error
	Name   string
}

func NewSubscribeAction(gh GitHubinator) GitHubItemAction {
	return GitHubItemAction{
		Handle: func(ctx context.Context, i GitHubItem, logger *slog.Logger) error {
			if i.Subscription == githubv4.SubscriptionStateSubscribed {
				logger.Debug("not subscribing to issue, user is already subscribed")

				return nil
			}

			logger.Info("subscribing to new issue")
			MetricActionHandleTotal.WithLabelValues("subscribe").Inc()

			if err := gh.SetSubscription(ctx, i.ID, githubv4.SubscriptionStateSubscribed); err != nil {
				logger.Error("unable to update subscription for issue", LogKeyError, err)

				return err
			}

			return nil
		},
		Name: "subscribe",
	}
}

func NewEmailAction(emailinator Emailinator, to string) GitHubItemAction {
	return GitHubItemAction{
		Handle: func(ctx context.Context, i GitHubItem, logger *slog.Logger) error {
			if i.Subscription == githubv4.SubscriptionStateSubscribed {
				logger.Debug("not emailing issue, user is already subscribed")

				return nil
			}

			logger.Info("Emailing item", "to", to)
			MetricActionHandleTotal.WithLabelValues("email").Inc()

			asJson, err := json.MarshalIndent(i, "", "\t")
			if err != nil {
				return fmt.Errorf("unable to marshal item to json: %w", err)
			}

			m, err := emailinator.NewMsg()
			if err != nil {
				return fmt.Errorf("unable to create new message: %w", err)
			}

			if err := m.To(to); err != nil {
				return fmt.Errorf("unable to set To address: %w", err)
			}

			subjectLine := strings.Builder{}
			subjectLine.WriteString("watchinator: ")
			subjectLine.WriteString(i.Repo.Owner)
			subjectLine.WriteString("/")
			subjectLine.WriteString(i.Repo.Name)

			switch i.Type {
			case GitHubItemIssue:
				subjectLine.WriteString("#")
				subjectLine.WriteString(strconv.Itoa(i.Number))
				subjectLine.WriteString(": ")
				subjectLine.WriteString(i.Title)
			default:
				subjectLine.WriteString(": unknown")
			}

			subjectLineString := subjectLine.String()
			logger.Debug("using the following subject line", "subject", subjectLineString)
			m.Subject(subjectLineString)

			body := string(asJson)
			logger.Debug("using the following body line", "body", body)
			m.SetBodyString(mail.TypeTextPlain, body)

			if err := m.SetAddrHeader("To", to); err != nil {
				return fmt.Errorf("unable to set to address: %w", err)
			}

			if err := emailinator.Send(ctx, m); err != nil {
				return fmt.Errorf("unable to send message: %w", err)
			}

			return nil
		},
		Name: "email",
	}
}

type Actioninator interface {
	WithAction(action GitHubItemAction) Actioninator
	Handle(ctx context.Context, item GitHubItem, logger *slog.Logger) error
}

type actioninator struct {
	actions    []GitHubItemAction
	actionLock *sync.Mutex
}

func (a *actioninator) WithAction(action GitHubItemAction) Actioninator {
	a.actions = append(a.actions, action)

	return a
}

func (a *actioninator) Handle(ctx context.Context, item GitHubItem, logger *slog.Logger) error {
	a.actionLock.Lock()
	defer a.actionLock.Unlock()

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(len(a.actions))

	for _, _action := range a.actions {
		action := _action
		g.Go(
			func(action GitHubItemAction) func() error {
				return func() error {
					if err := action.Handle(gctx, item, logger); err != nil {
						MetricActionHandleErrorTotal.WithLabelValues(action.Name).Inc()

						return err
					}

					return nil
				}
			}(action),
		)
	}

	if err := g.Wait(); err != nil {
		return err
	}

	return nil
}

func NewActioninator() Actioninator {
	return &actioninator{
		actions:    []GitHubItemAction{},
		actionLock: &sync.Mutex{},
	}
}
