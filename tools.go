//go:build tools

// Pins code generators in go.mod so `go run` uses a fixed version.
package tools

import (
	_ "github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen"
)
