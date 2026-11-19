//go:build !unix

package fips

import "fmt"

func startAgain(string, []string, []string) error {
	return fmt.Errorf("%s: on this system, set GODEBUG=fips140=on or only instead", Env)
}
