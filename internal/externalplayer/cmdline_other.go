//go:build !windows

package externalplayer

import "os/exec"

func setRawCmdLine(*exec.Cmd, string) {}

func hiddenCommand(name string, args ...string) *exec.Cmd { return exec.Command(name, args...) }
