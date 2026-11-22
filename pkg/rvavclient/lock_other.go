//go:build !(darwin || dragonfly || freebsd || linux || netbsd || openbsd || windows)

package rvavclient

import "os"

func lockFile(*os.File) error { return nil }

func unlockFile(*os.File) error { return nil }

func privateDir(string) error { return nil }
