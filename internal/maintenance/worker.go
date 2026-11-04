package maintenance

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

func Run(ctx context.Context, interval time.Duration, tick func(context.Context, time.Time) error) error {
	if interval <= 0 {
		return errors.New("maintenance interval must be positive")
	}
	timer := time.NewTicker(interval)
	defer timer.Stop()
	for {
		if ctx.Err() != nil {
			return nil
		}
		operation, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := tick(operation, time.Now().UTC())
		cancel()
		if err != nil && ctx.Err() == nil {
			slog.Error("maintenance iteration failed")
		}
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
		}
	}
}
