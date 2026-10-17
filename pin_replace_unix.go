//go:build !windows

package main

import "os"

func replacePinFile(src, dst string) error { return os.Rename(src, dst) }
