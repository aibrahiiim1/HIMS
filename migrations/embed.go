// Package migrations embeds the SQL migration files so they ship inside the
// hims-migrate binary — no external files needed at deploy time.
package migrations

import "embed"

// FS holds every up-migration, embedded at build time.
//
//go:embed *.up.sql
var FS embed.FS
