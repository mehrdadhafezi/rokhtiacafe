// Package db embeds the SQL migrations so the binary carries its own schema.
package db

import "embed"

// Migrations holds goose SQL migrations under "migrations/".
//
//go:embed migrations/*.sql
var Migrations embed.FS
