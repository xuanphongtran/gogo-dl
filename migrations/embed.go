// Package migrations embeds the SQL migration files in the application binary.
package migrations

import "embed"

// Files contains every versioned up and down migration.
//
//go:embed *.sql
var Files embed.FS
