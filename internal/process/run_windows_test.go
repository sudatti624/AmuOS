package process

import (
	"context"
	"errors"
	"golang.org/x/sys/windows/svc"
	"testing"
	"time"
)

func TestServiceStopCancelsAndWaitsForWorker(t *testing.T) {
	for _, command := range []svc.Cmd{svc.Stop, svc.Shutdown} {
		requests := make(chan svc.ChangeRequest, 1)
		status := make(chan svc.Status, 20)
		drained := make(chan struct{})
		h := &handler{run: func(ctx context.Context) error { <-ctx.Done(); close(drained); return ctx.Err() }}
		done := make(chan uint32, 1)
		go func() { _, code := h.Execute(nil, requests, status); done <- code }()
		<-status
		running := <-status
		if running.State != svc.Running {
			t.Fatal(running)
		}
		requests <- svc.ChangeRequest{Cmd: command}
		select {
		case code := <-done:
			if code != 0 {
				t.Fatal(code)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("stop did not finish")
		}
		select {
		case <-drained:
		default:
			t.Fatal("service stopped before worker")
		}
	}
}

func TestServiceFailureReportsNonzeroExit(t *testing.T) {
	h := &handler{run: func(context.Context) error { return errors.New("startup failed") }}
	status := make(chan svc.Status, 10)
	specific, code := h.Execute(nil, make(chan svc.ChangeRequest), status)
	if !specific || code == 0 || h.err == nil {
		t.Fatal("failure lost")
	}
}
