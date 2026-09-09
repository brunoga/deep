// editd serves the collaborative editor: a websocket room per document, a
// listing, and the browser client.
//
//	editd -addr :8100 -data ./documents
//
// Open the printed address in two windows and type in both.
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/brunoga/deep/examples/editor/server"
)

func main() {
	addr := flag.String("addr", ":8100", "listen address")
	data := flag.String("data", "./documents", "directory holding the documents")
	evict := flag.Duration("evict", 5*time.Minute,
		"how long an empty room lingers before being written out and dropped")
	web, pkg := defaultDirs()
	webDir := flag.String("web", web, "directory holding the browser client")
	pkgDir := flag.String("js", pkg, "directory holding the JavaScript package (js/dist)")
	flag.Parse()

	srv, err := server.New(*data, *evict)
	if err != nil {
		log.Fatalf("opening the document store: %v", err)
	}
	// A document to land in, so the editor is never staring at nothing.
	_ = srv.Create("welcome")

	mux := http.NewServeMux()
	mux.Handle("/ws", srv.Hub())
	mux.Handle("/documents", srv.API())
	mux.Handle("/documents/", srv.API()) // /documents/events, the listing as it changes
	mux.Handle("/deep-patch/", http.StripPrefix("/deep-patch/", http.FileServer(http.Dir(*pkgDir))))
	mux.Handle("/", http.FileServer(http.Dir(*webDir)))

	httpSrv := &http.Server{Addr: *addr, Handler: mux}
	go func() {
		hint := *addr
		if strings.HasPrefix(hint, ":") {
			hint = "localhost" + hint
		}
		log.Printf("editd listening on %s (documents: %s)", *addr, *data)
		log.Printf("open http://%s/ — in two windows, ideally", hint)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("serving: %v", err)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	<-ctx.Done()

	log.Print("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutdownCtx)
	// Documents typed into a moment ago should not have to wait for the
	// eviction timer to survive a restart.
	srv.Persist()
}

// defaultDirs locates the client and the JavaScript package relative to this
// checkout, so `go run ./cmd/editd` works from anywhere inside it.
func defaultDirs() (web string, pkg string) {
	dir, err := os.Getwd()
	if err != nil {
		return "web", filepath.Join("..", "..", "js", "dist")
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "js", "dist")); err == nil {
			return filepath.Join(dir, "examples", "editor", "web"), filepath.Join(dir, "js", "dist")
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "web", filepath.Join("..", "..", "js", "dist")
		}
		dir = parent
	}
}
