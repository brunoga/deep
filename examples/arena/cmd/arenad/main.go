// arenad runs one arena: the authoritative world, the tick loop, and the
// websocket door. Every tick it broadcasts a patch of exactly what changed.
//
//	arenad -addr :7777 -size 24x16 -tick 100ms -replay match.tape
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/brunoga/deep/examples/arena/replay"
	"github.com/brunoga/deep/examples/arena/server"
	"github.com/brunoga/deep/examples/arena/world"
)

func main() {
	addr := flag.String("addr", ":7777", "listen address")
	size := flag.String("size", "24x16", "arena size, WxH")
	tick := flag.Duration("tick", 100*time.Millisecond, "tick interval")
	gems := flag.Int("gems", 12, "how many gems the spawner keeps on the floor")
	tape := flag.String("replay", "", "record the match to this file (play it back with arenatape)")
	flag.Parse()

	var w, h int
	if _, err := fmt.Sscanf(*size, "%dx%d", &w, &h); err != nil || w < 4 || h < 4 {
		log.Fatalf("bad -size %q (want e.g. 24x16, at least 4x4)", *size)
	}

	opts := []server.Option{server.WithTick(*tick), server.WithGemTarget(*gems)}
	if *tape != "" {
		f, err := os.Create(*tape)
		if err != nil {
			log.Fatalf("creating replay file: %v", err)
		}
		defer f.Close()
		opts = append(opts, server.WithReplay(replay.NewWriter(f)))
	}

	s := server.New(world.New(w, h), opts...)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	go func() {
		if err := s.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			log.Fatalf("game loop: %v", err)
		}
	}()

	httpSrv := &http.Server{Addr: *addr, Handler: s}
	go func() {
		log.Printf("arenad listening on %s (%dx%d, tick %s)", *addr, w, h, *tick)
		hint := *addr
		if strings.HasPrefix(hint, ":") {
			hint = "localhost" + hint
		}
		log.Printf("join with: arena -server ws://%s -name yourname", hint)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("serving: %v", err)
		}
	}()

	<-ctx.Done()
	log.Print("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutdownCtx)
}
