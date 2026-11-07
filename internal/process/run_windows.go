package process

import (
	"context"
	"errors"
	"fmt"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/eventlog"
	"log/slog"
	"time"
)

type handler struct {
	run func(context.Context) error
	err error
}

type eventWriter struct{ log *eventlog.Log }

func (w eventWriter) Write(p []byte) (int, error) {
	if err := w.log.Info(1, string(p)); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (h *handler) Execute(_ []string, requests <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	status <- svc.Status{State: svc.StartPending, WaitHint: 30000}
	done := make(chan error, 1)
	go func() { done <- h.run(ctx) }()
	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for {
		select {
		case h.err = <-done:
			if errors.Is(h.err, context.Canceled) {
				h.err = nil
			}
			if h.err != nil {
				return true, 1
			}
			return false, 0
		case request := <-requests:
			switch request.Cmd {
			case svc.Interrogate:
				status <- request.CurrentStatus
			case svc.Stop, svc.Shutdown:
				cancel()
				ticker := time.NewTicker(time.Second)
				defer ticker.Stop()
				for checkpoint := uint32(1); ; checkpoint++ {
					status <- svc.Status{State: svc.StopPending, WaitHint: 20000, CheckPoint: checkpoint}
					select {
					case h.err = <-done:
						if errors.Is(h.err, context.Canceled) {
							h.err = nil
						}
						if h.err != nil {
							return true, 1
						}
						return false, 0
					case <-ticker.C:
					}
				}
			}
		}
	}
}

func Run(name string, run func(context.Context) error) error {
	service, err := svc.IsWindowsService()
	if err != nil {
		return err
	}
	if !service {
		return console(run)
	}
	log, err := eventlog.Open(name)
	if err != nil {
		return fmt.Errorf("open service event log: %w", err)
	}
	defer log.Close()
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(eventWriter{log}, nil)))
	defer slog.SetDefault(previous)
	_ = log.Info(1, "Service starting")
	h := &handler{run: run}
	err = svc.Run(name, h)
	if err = errors.Join(err, h.err); err != nil {
		_ = log.Error(1, "Service stopped: "+err.Error())
	} else {
		_ = log.Info(1, "Service stopped")
	}
	return err
}
