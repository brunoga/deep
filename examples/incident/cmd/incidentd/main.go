// incidentd is commandpost's server: it owns the incidents, applies patches,
// keeps the audit log, and hosts the collaborative notes rooms.
//
//	incidentd -addr :8080 -data ./data -token sekrit
//
// Structured edits arrive as deep patches on the JSON API; notes sync over
// the websocket at /ws?room=<incident-id>.
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

	"github.com/brunoga/deep/examples/incident/server"
)

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	data := flag.String("data", "./data", "data directory")
	token := flag.String("token", "", "bearer token required on every request (empty: no auth)")
	evict := flag.Duration("evict", 5*time.Minute, "how long an empty notes room lingers before persisting to disk")
	defaultWeb, defaultPkg := server.DefaultWebDirs()
	webDir := flag.String("web", defaultWeb, "directory holding the browser client")
	pkgDir := flag.String("js", defaultPkg, "directory holding the JavaScript patch package (js/dist)")
	flag.Parse()

	store, err := server.Open(filepath.Join(*data, "incidents"))
	if err != nil {
		log.Fatalf("opening store: %v", err)
	}

	var apiOpts []server.APIOption
	if *token != "" {
		apiOpts = append(apiOpts, server.WithToken(*token))
	}
	api := server.NewAPI(store, apiOpts...)

	notes, err := server.NewNotes(filepath.Join(*data, "notes"), *evict,
		api.Authorized,
		func(id string) bool { _, err := store.Get(id); return err == nil },
	)
	if err != nil {
		log.Fatalf("opening notes: %v", err)
	}

	mux := http.NewServeMux()
	mux.Handle("/ws", notes)
	// The browser client lives under /app/; everything else is the API, which
	// both it and the Go clients speak.
	mux.Handle("/app/", http.StripPrefix("/app", server.WebHandler(*webDir, *pkgDir)))
	mux.Handle("/", api)

	srv := &http.Server{Addr: *addr, Handler: mux}
	go func() {
		log.Printf("incidentd listening on %s (data: %s)", *addr, *data)
		hint := *addr
		if strings.HasPrefix(hint, ":") {
			hint = "localhost" + hint
		}
		log.Printf("browser client at http://%s/app/", hint)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("serving: %v", err)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	<-ctx.Done()

	log.Print("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
	notes.Persist()
}
