//go:build windows

package atomicfile

import (
	"errors"
	"strings"
	"syscall"
)

func writeAttempts() int { return 8 }

func writeTransient(err error) bool {
	return windowsSharing(err)
}

func windowsSharing(err error) bool {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return errno == 5 || errno == 32 || errno == 33
	}
	msg := err.Error()
	return strings.Contains(msg, "Access is denied") || strings.Contains(msg, "being used by another process")
}
