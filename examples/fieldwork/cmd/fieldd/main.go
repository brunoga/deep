// fieldd is fieldwork's server: versioned asset records, a canonical patch
// log, and the sync endpoint that reconciles offline devices.
//
//	fieldd -addr :8090 -token sekrit -seed
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/brunoga/deep/examples/fieldwork/model"
	"github.com/brunoga/deep/examples/fieldwork/server"
)

func main() {
	addr := flag.String("addr", ":8090", "listen address")
	token := flag.String("token", "", "bearer token required on every request (empty: no auth)")
	seed := flag.Bool("seed", false, "populate a few demo assets at startup")
	flag.Parse()

	store := server.NewStore()
	if *seed {
		for _, a := range demoAssets() {
			if _, _, err := store.Create(a); err != nil {
				log.Fatalf("seeding %s: %v", a.ID, err)
			}
		}
		log.Printf("seeded %d assets", len(demoAssets()))
	}

	var opts []server.APIOption
	if *token != "" {
		opts = append(opts, server.WithToken(*token))
	}
	srv := &http.Server{Addr: *addr, Handler: server.NewAPI(store, opts...)}

	go func() {
		log.Printf("fieldd listening on %s", *addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("serving: %v", err)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	<-ctx.Done()
	log.Print("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}

func demoAssets() []model.Asset {
	return []model.Asset{
		{
			ID: "pump-7", Name: "Intake pump 7", Site: "riverside", Status: model.StatusOK,
			Assignee: "ana",
			Readings: map[string]model.Reading{
				"ph":   {Value: 7.1, Unit: "pH", By: "scada"},
				"flow": {Value: 122, Unit: "l/min", By: "scada"},
			},
			Checks: []model.Check{
				{ID: "seals", Label: "inspect seals"},
				{ID: "filter", Label: "replace filter"},
			},
		},
		{
			ID: "valve-2", Name: "Outflow valve 2", Site: "riverside", Status: model.StatusOK,
			Checks: []model.Check{{ID: "actuator", Label: "cycle actuator"}},
		},
		{
			ID: "tank-1", Name: "Settling tank 1", Site: "hilltop", Status: model.StatusAttention,
			Readings: map[string]model.Reading{"level": {Value: 2.4, Unit: "m", By: "scada"}},
		},
	}
}
