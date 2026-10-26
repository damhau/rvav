// Package migrations embeds the goose SQL migrations so the rvav binary can
// apply them with `rvav migrate`.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
