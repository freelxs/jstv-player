//go:build unix

package webserver

import (
	"os"
	"os/signal"
	"syscall"
)

func shutdownNotify() <-chan os.Signal {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	return ch
}
