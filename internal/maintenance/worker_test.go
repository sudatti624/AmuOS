package maintenance

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestWorkerRetriesErrorsAndStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls atomic.Int32
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, time.Millisecond, func(ctx context.Context, _ time.Time) error {
			if _, ok := ctx.Deadline(); !ok {
				return errors.New("missing deadline")
			}
			if calls.Add(1) >= 2 {
				cancel()
			}
			return errors.New("private database error")
		})
	}()
	select {
	case err := <-done:
		if err != nil || calls.Load() < 2 {
			t.Fatal(err, calls.Load())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not stop")
	}
}

func TestWorkerCancellationReachesActiveIteration(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, time.Hour, func(ctx context.Context, _ time.Time) error { close(started); <-ctx.Done(); return ctx.Err() })
	}()
	<-started
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("iteration ignored shutdown")
	}
	if err := Run(context.Background(), 0, func(context.Context, time.Time) error { return nil }); err == nil {
		t.Fatal("invalid interval accepted")
	}
}
