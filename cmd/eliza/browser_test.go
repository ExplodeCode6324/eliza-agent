package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

func TestBrowserRuntimeRunHonorsOperationTimeout(t *testing.T) {
	runtime := newTestBrowserRuntime(20 * time.Millisecond)
	started := time.Now()

	err := runtime.run(context.Background())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline exceeded, got %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("browser run ignored timeout; elapsed=%s", elapsed)
	}
	if runtime.browserCtx != nil {
		t.Fatalf("timed out browser session was not reset")
	}
}

func TestBrowserRuntimeRunHonorsParentCancellation(t *testing.T) {
	runtime := newTestBrowserRuntime(time.Minute)
	parent, cancel := context.WithCancel(context.Background())
	time.AfterFunc(20*time.Millisecond, cancel)
	started := time.Now()

	err := runtime.run(parent)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context canceled, got %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("browser run ignored parent cancellation; elapsed=%s", elapsed)
	}
	if runtime.browserCtx != nil {
		t.Fatalf("cancelled browser session was not reset")
	}
}

func TestBrowserRuntimeRunKeepsSessionOnActionError(t *testing.T) {
	sentinel := errors.New("action failed")
	runtime := newTestBrowserRuntime(time.Minute)
	runtime.runActions = func(context.Context, ...chromedp.Action) error {
		return sentinel
	}

	err := runtime.run(context.Background())
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel error, got %v", err)
	}
	if runtime.browserCtx == nil {
		t.Fatalf("ordinary action error should not reset browser session")
	}
}

func newTestBrowserRuntime(timeout time.Duration) *BrowserRuntime {
	browserCtx, browserCancel := context.WithCancel(context.Background())
	return &BrowserRuntime{
		timeout:       timeout,
		browserCtx:    browserCtx,
		browserCancel: browserCancel,
		runActions: func(ctx context.Context, _ ...chromedp.Action) error {
			<-ctx.Done()
			return ctx.Err()
		},
	}
}
