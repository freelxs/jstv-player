//go:build !windows

package app

import "os"

func replacePinFile(src, dst string) error { return os.Rename(src, dst) }
