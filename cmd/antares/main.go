// Command antares is the Antares backend: API, static bundle and SQLite.
package main

import (
	"context"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/raufimusaddiq/antares/internal/auth"
	"github.com/raufimusaddiq/antares/internal/config"
	"github.com/raufimusaddiq/antares/internal/secrets"
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
	// Prove the master key is usable now, not when slice 2 first seals a key.
	// A bad key refuses startup: running without it would only defer the failure
	// to the first credential write.
	if _, err := secrets.New(cfg.SecretKey); err != nil {
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

	set, err := store.LoadMigrations(store.Migrations())
	if err != nil {
		return err
	}
	// Log readiness transitions, not every probe: a proxy health check would
	// otherwise write a line every few seconds for as long as the DB is down.
	var wasReady atomic.Bool
	wasReady.Store(true)
	ready := func(ctx context.Context) error {
		err := store.SchemaCurrent(ctx, db, set)
		if ok := err == nil; wasReady.Swap(ok) != ok {
			if ok {
				log.Printf("readyz: recovered")
			} else {
				log.Printf("readyz: not ready: %v", err)
			}
		}
		return err
	}
	// No WriteTimeout: SSE streams (later slices) outlive any fixed write deadline.
	identity, err := auth.New(auth.Options{
		DB:           db,
		Endpoints:    auth.GoogleEndpoints(),
		ClientID:     cfg.GoogleClientID,
		ClientSecret: string(cfg.GoogleClientSecret),
		PublicOrigin: cfg.PublicOrigin,
	})
	if err != nil {
		return err
	}
	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           newServer(ready, cfg.StaticDir).WithAuth(identity).Routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 16,
	}
	ln, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		return err
	}
	log.Printf("antares listening on %s", cfg.Addr)
	return serve(ctx, srv, ln, 10*time.Second)
}
