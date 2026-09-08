package server

import (
	"net/http"
	"os"
	"path/filepath"
)

// The browser client. It is served rather than bundled: the page imports the
// JavaScript patch package by name through an import map, and the package is
// mounted beside it, so there is no build step between editing a file and
// reloading the tab.
//
// What it demonstrates is worth stating plainly: the page sends *patches*,
// with the same paths and the same conditions the Go CLI sends, and the
// server cannot tell the two apart. That is the interop claim, running.

// WebHandler serves the browser client from dir, with the JavaScript patch
// package mounted at deep-patch/ from pkgDir.
//
// Both directories are served read-only and by path, so a missing build of
// the package shows up as a 404 in the browser console rather than as a
// server that refuses to start.
func WebHandler(dir, pkgDir string) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/deep-patch/", http.StripPrefix("/deep-patch/", http.FileServer(http.Dir(pkgDir))))
	mux.Handle("/", http.FileServer(http.Dir(dir)))
	return mux
}

// DefaultWebDirs locates the browser client and the JavaScript package
// relative to this source tree, for running the example straight from a
// checkout. Both may be overridden on the command line.
func DefaultWebDirs() (web string, pkg string) {
	root := repoRoot()
	if root == "" {
		return "web", filepath.Join("..", "..", "js", "dist")
	}
	return filepath.Join(root, "examples", "incident", "web"),
		filepath.Join(root, "js", "dist")
}

// repoRoot walks up from the working directory looking for the checkout, so
// `go run ./cmd/incidentd` works from anywhere inside it.
func repoRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			if _, err := os.Stat(filepath.Join(dir, "js", "dist")); err == nil {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}
