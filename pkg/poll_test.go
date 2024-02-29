package pkg

import (
	"context"
	"errors"
	"fmt"
	"os"
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

func TestPollinatorCanStartAndStopNewPolls(t *testing.T) {
	testDoneChan := make(chan bool)

	go haveTestTimeout(t, time.Millisecond*300, testDoneChan)

	p := NewPollinator(context.Background(), debugLogger)

	numTickOne := 0
	numTickTwo := 0

	p.Add(
		"test-1", time.Millisecond*50,
		func(_ time.Time) {
			numTickOne += 1
		},
		false,
	)
	p.Add(
		"test-2", time.Millisecond*100,
		func(_ time.Time) {
			numTickTwo += 1
		},
		false,
	)

	time.Sleep(time.Millisecond * 150)

	p.Delete("test-1")
	p.Delete("test-2")

	fmt.Println(numTickOne)
	assert.Equal(t, numTickOne == 3, true)
	assert.Equal(t, numTickTwo == 1, true)

	close(testDoneChan)
}

func TestPollinatorCanUpdateExistingTicker(t *testing.T) {
	testDoneChan := make(chan bool)

	go haveTestTimeout(t, time.Millisecond*300, testDoneChan)

	p := NewPollinator(context.Background(), debugLogger)
	numTickOne := 0

	p.Add(
		"test-1", time.Millisecond*50,
		func(_ time.Time) {
			t.Error(errors.New("ticker was not updated within 50 ms"))
			t.FailNow()
		},
		false,
	)
	p.Add(
		"test-1", time.Millisecond*50,
		func(t time.Time) {
			numTickOne += 1
		},
		false,
	)

	time.Sleep(time.Millisecond * 105)

	p.Delete("test-1")

	assert.Equal(t, numTickOne == 2, true)

	close(testDoneChan)
}

func TestPollinatorCanDeleteExistingTicker(t *testing.T) {
	testDoneChan := make(chan bool)

	go haveTestTimeout(t, time.Millisecond*300, testDoneChan)

	p := NewPollinator(context.Background(), debugLogger)

	numTickOne := 0
	numTickTwo := 0

	p.Add(
		"test-1", time.Millisecond*50,
		func(t time.Time) {
			numTickOne += 1
		},
		false,
	)
	p.Add(
		"test-2", time.Millisecond*50,
		func(_ time.Time) {
			numTickTwo += 1
		},
		false,
	)

	time.Sleep(time.Millisecond * 55)
	p.Delete("test-1")
	time.Sleep(time.Millisecond * 55)

	assert.Equal(t, numTickOne == 1, true)
	assert.Equal(t, numTickTwo == 2, true)

	close(testDoneChan)
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
