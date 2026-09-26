package web

import (
	"embed"
	"io/fs"
)

//go:embed templates/* static/*
var EmbeddedFS embed.FS

// TemplatesFS returns the sub-filesystem containing templates.
func TemplatesFS() (fs.FS, error) {
	return fs.Sub(EmbeddedFS, "templates")
}

// StaticFS returns the sub-filesystem containing static assets.
func StaticFS() (fs.FS, error) {
	return fs.Sub(EmbeddedFS, "static")
}
