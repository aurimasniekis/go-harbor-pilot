// Package migrations embeds the SQL schema migrations for each database.
package migrations

import (
	"embed"
	"io/fs"
)

//go:embed main/*.sql
var mainFS embed.FS

//go:embed traces/*.sql
var tracesFS embed.FS

// Main returns migrations for harbor.db.
func Main() fs.FS { sub, _ := fs.Sub(mainFS, "main"); return sub }

// Traces returns migrations for traces.db.
func Traces() fs.FS { sub, _ := fs.Sub(tracesFS, "traces"); return sub }
