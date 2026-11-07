//go:build !windows

package process

import "context"

func Run(name string, run func(context.Context) error) error { return console(run) }
