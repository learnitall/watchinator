package pkg

import (
	"context"
	"errors"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/exp/slog"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

var debugLogger = slog.New(
	slog.NewTextHandler(
		os.Stderr,
		&slog.HandlerOptions{
			Level: slog.LevelDebug,
		},
	),
)

func haveTestTimeout(t *testing.T, after time.Duration, done chan bool) {
	timer := time.NewTimer(after)
	select {
	case <-timer.C:
		t.Errorf("timed out after %s", after.String())
		os.Exit(1)
	case <-done:
		return
	}
}

func TestRunPollCreatesPollThatExecsCallbackOnInterval(t *testing.T) {
	testDoneChan := make(chan bool)

	go haveTestTimeout(t, time.Millisecond*100, testDoneChan)

	cancelChan := make(chan bool)

	var startTime time.Time

	seenInitialTick := false

	p := poll{
		cancelChan:      cancelChan,
		doneChan:        make(chan bool),
		ctx:             context.Background(),
		logger:          debugLogger,
		ticker:          time.NewTicker(50 * time.Millisecond),
		callbackOnStart: true,
		callback: func(callTime time.Time) {
			interval := callTime.Sub(startTime)

			if !seenInitialTick {
				assert.Assert(t, cmp.Equal(true, interval < time.Millisecond*5))

				seenInitialTick = true

				return
			}

			assert.Assert(t, cmp.Equal(true, interval < time.Millisecond*60))
			assert.Assert(t, cmp.Equal(true, interval > time.Millisecond*40))
			close(cancelChan)
		},
	}

	startTime = time.Now()

	runPoll(&p)

	close(testDoneChan)
}

func TestPollClosesDoneChanAfterCancelled(t *testing.T) {
	testDoneChan := make(chan bool)

	go haveTestTimeout(t, time.Millisecond*100, testDoneChan)

	gotTickChan := make(chan bool)
	doneChan := make(chan bool)
	cancelChan := make(chan bool)

	p := poll{
		cancelChan: cancelChan,
		doneChan:   doneChan,
		ctx:        context.Background(),
		logger:     debugLogger,
		ticker:     time.NewTicker(50 * time.Millisecond),
		callback: func(callTime time.Time) {
			close(gotTickChan)
		},
	}

	go runPoll(&p)

	<-gotTickChan
	close(cancelChan)
	<-doneChan

	close(testDoneChan)
}

func TestPollCanBeClosedUsingContext(t *testing.T) {
	testDoneChan := make(chan bool)

	go haveTestTimeout(t, time.Millisecond*100, testDoneChan)

	gotTick := make(chan bool)
	ctx, cancel := context.WithCancel(context.Background())
	doneChan := make(chan bool)

	p := poll{
		cancelChan: make(chan bool),
		doneChan:   doneChan,
		ctx:        ctx,
		logger:     slog.Default(),
		ticker:     time.NewTicker(50 * time.Millisecond),
		callback: func(callTime time.Time) {
			close(gotTick)
		},
	}

	go runPoll(&p)

	<-gotTick
	cancel()
	<-doneChan

	close(testDoneChan)
}

// tickCounter records callbacks on the poll goroutine and lets the test goroutine
// read the total without racing it.
type tickCounter struct {
	n      atomic.Int64
	ticked chan struct{}
}

func newTickCounter() *tickCounter {
	return &tickCounter{ticked: make(chan struct{}, 128)}
}

func (c *tickCounter) callback(_ time.Time) {
	c.n.Add(1)

	// Never block the poll goroutine; waitFor re-reads the count regardless of
	// whether this signal made it through.
	select {
	case c.ticked <- struct{}{}:
	default:
	}
}

func (c *tickCounter) count() int64 {
	return c.n.Load()
}

// waitFor blocks until at least n ticks have been seen. Waiting on the count is
// what makes these tests deterministic: sleeping for a multiple of the interval
// and asserting an exact total races the tick that lands on the boundary.
func (c *tickCounter) waitFor(t *testing.T, n int64, within time.Duration) {
	t.Helper()

	deadline := time.After(within)

	recheck := time.NewTicker(time.Millisecond)
	defer recheck.Stop()

	for c.count() < n {
		select {
		case <-c.ticked:
		case <-recheck.C:
		case <-deadline:
			t.Fatalf("saw %d of %d ticks within %s", c.count(), n, within)
		}
	}
}

func TestPollinatorCanStartAndStopNewPolls(t *testing.T) {
	p := NewPollinator(context.Background(), debugLogger)

	fast := newTickCounter()
	slow := newTickCounter()

	p.Add("test-1", time.Millisecond*50, fast.callback, false)
	p.Add("test-2", time.Millisecond*100, slow.callback, false)

	fast.waitFor(t, 3, time.Second)

	p.Delete("test-1")
	p.Delete("test-2")

	// Delete blocks until the poll goroutine has exited, so these are final.
	// That Delete actually stops a poll is covered by
	// TestPollinatorCanDeleteExistingTicker, which lets real time pass before
	// re-reading; asserting it here would only compare an atomic against itself.
	fastTotal := fast.count()
	slowTotal := slow.count()

	// The point is that each poll runs on its own interval, not that a given
	// number of ticks landed inside a sleep.
	assert.Assert(
		t, slowTotal >= 1, "slower poll never ran, got %d", slowTotal,
	)
	assert.Assert(
		t, slowTotal < fastTotal,
		"expected the 100ms poll to tick less than the 50ms poll, got %d vs %d",
		slowTotal, fastTotal,
	)
}

func TestPollinatorCanUpdateExistingTicker(t *testing.T) {
	p := NewPollinator(context.Background(), debugLogger)

	counter := newTickCounter()

	p.Add(
		"test-1", time.Millisecond*50,
		func(_ time.Time) {
			t.Error(errors.New("replaced callback was still invoked"))
		},
		false,
	)
	p.Add("test-1", time.Millisecond*50, counter.callback, false)

	counter.waitFor(t, 2, time.Second)

	p.Delete("test-1")
}

func TestPollinatorCanDeleteExistingTicker(t *testing.T) {
	p := NewPollinator(context.Background(), debugLogger)

	deleted := newTickCounter()
	kept := newTickCounter()

	p.Add("test-1", time.Millisecond*50, deleted.callback, false)
	p.Add("test-2", time.Millisecond*50, kept.callback, false)

	deleted.waitFor(t, 1, time.Second)
	p.Delete("test-1")

	// Delete waits for the poll's goroutine to exit, so nothing can increment
	// this afterwards.
	deletedTotal := deleted.count()

	// The surviving poll must keep ticking well past the point the other stopped.
	kept.waitFor(t, deletedTotal+2, time.Second)

	assert.Assert(
		t, cmp.Equal(deleted.count(), deletedTotal),
		"deleted poll ticked again after Delete returned",
	)
}

func TestPollinatorCanStopAllTickers(t *testing.T) {
	testDoneChan := make(chan bool)

	go haveTestTimeout(t, time.Millisecond*300, testDoneChan)

	p := NewPollinator(context.Background(), debugLogger)

	failNow := func(_ time.Time) {
		t.Error(errors.New("poll was not stopped"))
		t.FailNow()
		os.Exit(1)
	}

	p.Add("test-1", time.Millisecond*100, failNow, false)
	p.Add("test-2", time.Millisecond*100, failNow, false)
	p.Add("test-3", time.Millisecond*100, failNow, false)

	p.StopAll()

	close(testDoneChan)
}
