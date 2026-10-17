//go:build !windows

package catvod

import "os"

func replaceFile(src, dst string) error { return os.Rename(src, dst) }
