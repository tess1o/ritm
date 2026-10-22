// Package migrations holds the embedded SQL schema migrations applied on startup: App for the
// shared accounts database and User for each user's health-data database.
package migrations

import (
	"embed"
	"io/fs"
)

//go:embed app/*.sql user/*.sql
var all embed.FS

var (
	App  = sub("app")
	User = sub("user")
)

func sub(dir string) fs.FS {
	f, err := fs.Sub(all, dir)
	if err != nil {
		panic(err)
	}
	return f
}
