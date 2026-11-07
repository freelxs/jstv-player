//go:build windows

package webserver

import (
	"os"
	"os/signal"
)

func shutdownNotify() <-chan os.Signal {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt)
	return ch
}
