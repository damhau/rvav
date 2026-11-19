//go:build unix

package fips

import (
	"fmt"
	"syscall"
)

func startAgain(exe string, args, env []string) error {
	err := syscall.Exec(exe, args, env)
	return fmt.Errorf("%s: start again in another FIPS 140 mode: %w", Env, err)
}
