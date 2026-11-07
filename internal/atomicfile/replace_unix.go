//go:build !windows

package atomicfile

import "os"

func replace(src, dst string) error { return os.Rename(src, dst) }
