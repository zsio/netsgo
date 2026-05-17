package server

import "embed"

//go:embed migrations/*.sql
var serverMigrationFS embed.FS
