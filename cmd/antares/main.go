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
	"github.com/raufimusaddiq/antares/internal/chat"
	"github.com/raufimusaddiq/antares/internal/config"
	"github.com/raufimusaddiq/antares/internal/hermes"
	"github.com/raufimusaddiq/antares/internal/kenari"
	"github.com/raufimusaddiq/antares/internal/push"
	"github.com/raufimusaddiq/antares/internal/secrets"
	"github.com/raufimusaddiq/antares/internal/store"
	"github.com/raufimusaddiq/antares/internal/tenant"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "healthcheck" {
		addr := os.Getenv("ANTARES_ADDR")
		if addr == "" {
			addr = ":8080"
		}
		if err := healthcheck(context.Background(), addr); err != nil {
			log.Fatal(err)
		}
		return
	}
	if isAdminCmd(os.Args) {
		if err := runAdminCmd(context.Background(), os.Args, os.Getenv, os.Stdout, os.Stderr); err != nil {
			log.Fatal(err)
		}
		return
	}
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}
	// Prove the master key is usable now, not at the first credential write.
	// A bad key refuses startup: running without it would only defer the failure.
	box, err := secrets.New(cfg.SecretKey)
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
	helper, err := hermes.NewHelper(cfg.ProvisionerURL, string(cfg.ProvisionerToken), nil)
	if err != nil {
		return err
	}
	verifier, err := hermes.NewVerifier(cfg.HermesURL, cfg.TenantToolsets, nil)
	if err != nil {
		return err
	}
	tenants, err := tenant.New(tenant.Options{DB: db, Box: box, Helper: helper, Verifier: verifier})
	if err != nil {
		return err
	}
	// Deferred after db.Close above, so it runs first: in-flight provisioning
	// is cancelled and drained before the database closes.
	defer tenants.Close()
	go tenants.Run(ctx)

	// A run that was in flight when the last process stopped has no worker now.
	// Record that before serving: RUNNING becomes DISCONNECTED (resumable),
	// PENDING becomes FAILED (never replayed).
	if d, f, err := store.RecoverRuns(ctx, db, time.Now()); err != nil {
		return err
	} else if d+f > 0 {
		log.Printf("runs recovered: disconnected=%d failed=%d", d, f)
	}
	hermesClient, err := hermes.NewClient(cfg.HermesURL, hermes.ClientOptions{})
	if err != nil {
		return err
	}
	var vapid *push.VAPID
	if cfg.PushConfigured() {
		if vapid, err = push.NewVAPID(cfg.VAPIDPrivateKey, cfg.VAPIDSubject); err != nil {
			return err
		}
	}
	pusher, err := push.New(push.Options{DB: db, Box: box, VAPID: vapid})
	if err != nil {
		return err
	}
	defer pusher.Close() // after chats.Close below has stopped the workers that call it
	chats, err := chat.New(chat.Options{DB: db, Box: box, Hermes: hermesClient, Kenari: kenari.New(kenari.DefaultURL, nil), Notifier: pusher})
	if err != nil {
		return err
	}
	defer chats.Close() // after the server drains (serve returns first), before db.Close

	// No WriteTimeout: SSE run streams outlive any fixed write deadline.
	identity, err := auth.New(auth.Options{
		DB:           db,
		Endpoints:    auth.GoogleEndpoints(),
		ClientID:     cfg.GoogleClientID,
		ClientSecret: string(cfg.GoogleClientSecret),
		PublicOrigin: cfg.PublicOrigin,
		Tenants:      tenants,
		Chat:         chats,
		Push:         pusher,
		Limits:       &cfg.Limits,
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
	// Open SSE streams never end by themselves: on shutdown, stop the run workers
	// so the streams close and the drain below can finish. Unfinished runs are
	// DISCONNECTED on the next start, never reported as completed.
	srv.RegisterOnShutdown(chats.Close)
	ln, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		return err
	}
	log.Printf("antares listening on %s", cfg.Addr)
	return serve(ctx, srv, ln, 10*time.Second)
}
