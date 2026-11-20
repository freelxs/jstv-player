//go:build !windows

package atomicfile

func writeAttempts() int { return 1 }

func writeTransient(error) bool { return false }
