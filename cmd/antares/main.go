// Command antares is the Antares backend: API, static bundle and SQLite.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/raufimusaddiq/antares/internal/config"
	"github.com/raufimusaddiq/antares/internal/server"
	"github.com/raufimusaddiq/antares/internal/store"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}
	db, err := store.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer db.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	applied, err := store.Migrate(ctx, db, store.Migrations())
	if err != nil {
		return err
	}
	log.Printf("migrations applied: %v", applied)

	if cfg.BootstrapAdmin() {
		created, err := store.BootstrapAdmin(ctx, db, cfg.AdminIssuer, cfg.AdminSub, cfg.AdminEmail, time.Now())
		if err != nil {
			return err
		}
		log.Printf("admin bootstrap: created=%v", created)
	}

	ready := func(ctx context.Context) error {
		err := store.SchemaCurrent(ctx, db, store.Migrations())
		if err != nil {
			log.Printf("readyz: %v", err)
		}
		return err
	}
	// No WriteTimeout: SSE streams (later slices) outlive any fixed write deadline.
	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           server.New(ready, cfg.StaticDir).Routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 16,
	}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	log.Printf("antares listening on %s", cfg.Addr)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
