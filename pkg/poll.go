package pkg

import (
	"context"
	"time"

	"golang.org/x/exp/slog"
)

// Pollinator handles executing functions on a ticker.
type Pollinator interface {
	// Add creates a new poll, whose callback will be executed on the given interval.
	// If the given poll already exists, then it is updated with the given interval and callback.
	// The argument doInitialCallback can be used to toggle if the poll is executed for the first time after the
	// call to Add, or if the poll is executed for the first time after the given interval.
	Add(name string, interval time.Duration, callback func(t time.Time), doInitialCallback bool)

	// Delete removes the poll by the given name. If it doesn't exist, then this is a no-op.
	Delete(name string)

	// List returns a slice containing the names of all the polls currently running.
	List() []string

	// StopAll stops all the added polls, blocking until all exit. Use this as a cleanup.
	StopAll()
}

// poll holds information necessary for running a new ticker in a separate go-routine.
type poll struct {
	// cancelChan can be closed to cancel the poll.
	cancelChan chan bool
	// doneChan signals that the poll has been cleaned up.
	doneChan chan bool
	// ctx is a universal context given to all poll structs that can be used to cancel all polls at once.
	ctx context.Context
	// callbackOnStart determines if the callback should be executed as soon as the poll starts or if the
	// callback should only be executed after an initial interval.
	callbackOnStart bool
	logger          *slog.Logger
	ticker          *time.Ticker
	callback        func(t time.Time)
}

// runPoll is meant to be started within a go-routine. On every tick, the poll's callback is executed. If the
// poll's context is cancelled or cancelChan is closed, the function returns.
func runPoll(p *poll) {
	p.logger.Debug("starting poller")

	if p.callbackOnStart {
		p.logger.Debug("running initial callback on start")
		p.callback(time.Now())
	}

	for {
		select {
		case <-p.ctx.Done():
			p.logger.Debug("closing poll, context closed", LogKeyError, p.ctx.Err())
			close(p.doneChan)

			return
		case <-p.cancelChan:
			p.logger.Debug("closing poll, cancel chan closed")
			close(p.doneChan)

			return
		case t := <-p.ticker.C:
			p.logger.Debug("new tick", "time", t)
			p.callback(t)
		}
	}
}

// stopPoll stops the given poll by closing its cancelChan. It blocks until the poll's doneChan is closed.
func stopPoll(p *poll) {
	p.logger.Debug("stopping poller")
	p.ticker.Stop()
	close(p.cancelChan)

	p.logger.Debug("waiting for done")
	<-p.doneChan

	p.logger.Debug("poll successfully stopped")
}

// pollinator is the internal implementation of the Pollinator interface.
type pollinator struct {
	// ctx is given to all polls, to be used as a universal stop.
	ctx context.Context
	// cancelCtx is the cancel function for the above context.
	cancelCtx func()
	// polls is a map from a poll's name to its struct.
	polls map[string]*poll
	// logger is the base logger passed to all polls.
	logger *slog.Logger
}

func (p *pollinator) Add(name string, interval time.Duration, callback func(t time.Time), doInitialCallback bool) {
	_, ok := p.polls[name]
	if ok {
		p.Delete(name)
	}

	newPoll := &poll{
		cancelChan:      make(chan bool),
		doneChan:        make(chan bool),
		ctx:             p.ctx,
		logger:          p.logger.With("name", name),
		ticker:          time.NewTicker(interval),
		callbackOnStart: doInitialCallback,
		callback:        callback,
	}

	p.polls[name] = newPoll

	go runPoll(newPoll)
}

func (p *pollinator) Delete(name string) {
	oldPoll, ok := p.polls[name]
	if !ok {
		p.logger.Warn("got delete on non-existent poll", "name", name)

		return
	}

	stopPoll(oldPoll)
	delete(p.polls, name)
}

func (p *pollinator) List() []string {
	polls := []string{}

	for k := range p.polls {
		polls = append(polls, k)
	}

	return polls
}

func (p *pollinator) StopAll() {
	p.cancelCtx()

	for n, poll := range p.polls {
		p.logger.Info("waiting for poll to finish", "name", n)
		<-poll.doneChan
	}
}

// NewPollinator creates a new pollinator. The given baseLogger and context will be used as the parent logger and
// context for all poll's created.
func NewPollinator(ctx context.Context, baseLogger *slog.Logger) Pollinator {
	pollCtx, cancelPollCtx := context.WithCancel(ctx)

	return &pollinator{
		ctx:       pollCtx,
		cancelCtx: cancelPollCtx,
		polls:     map[string]*poll{},
		logger:    baseLogger,
	}
}
