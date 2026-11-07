package app

import "os"

func newPlayer(dir string) *Player { return newConfiguredPlayer(dir, os.DirFS("../..")) }
