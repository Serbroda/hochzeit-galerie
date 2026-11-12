// Package ui bettet Templates und statische Dateien ins Binary ein. Das CSS in static/css/app.css wird aus
// frontend/app.css erzeugt (make css) und liegt fertig im Repo, damit go build ohne Node auskommt.
package ui

import (
	"embed"
	"io/fs"
)

//go:embed templates static
var files embed.FS

func Templates() fs.FS {
	return sub("templates")
}

func Static() fs.FS {
	return sub("static")
}

func sub(dir string) fs.FS {
	f, err := fs.Sub(files, dir)
	if err != nil {
		panic(err) // das embed-Muster oben garantiert das Verzeichnis
	}
	return f
}
