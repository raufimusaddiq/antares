//go:build capacity

package main

// The scratch Antares: the wiring of cmd/antares/main.go (same packages, same
// http.Server settings, same embedded bundle) with one difference, the OIDC
// endpoints point at the fake provider instead of Google, because the production
// binary hardcodes Google and a headless run cannot complete that flow. Web Push
// is left unconfigured, as it is in a deployment without VAPID keys.

import (
	"context"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/raufimusaddiq/antares/internal/auth"
	"github.com/raufimusaddiq/antares/internal/chat"
	"github.com/raufimusaddiq/antares/internal/config"
	"github.com/raufimusaddiq/antares/internal/hermes"
	"github.com/raufimusaddiq/antares/internal/push"
	"github.com/raufimusaddiq/antares/internal/secrets"
	"github.com/raufimusaddiq/antares/internal/server"
	"github.com/raufimusaddiq/antares/internal/store"
	"github.com/raufimusaddiq/antares/internal/tenant"
	"github.com/raufimusaddiq/antares/internal/webui"
)

func runServer(idpURL string) error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}
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
	if _, err := store.Migrate(ctx, db, store.Migrations()); err != nil {
		return err
	}
	if cfg.BootstrapAdmin() {
		if _, err := store.BootstrapAdmin(ctx, db, cfg.AdminIssuer, cfg.AdminSub, cfg.AdminEmail, time.Now()); err != nil {
			return err
		}
	}
	set, err := store.LoadMigrations(store.Migrations())
	if err != nil {
		return err
	}
	ready := func(ctx context.Context) error { return store.SchemaCurrent(ctx, db, set) }
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
	defer tenants.Close()
	go tenants.Run(ctx)
	if _, _, err := store.RecoverRuns(ctx, db, time.Now()); err != nil {
		return err
	}
	hc, err := hermes.NewClient(cfg.HermesURL, hermes.ClientOptions{})
	if err != nil {
		return err
	}
	pusher, err := push.New(push.Options{DB: db, Box: box})
	if err != nil {
		return err
	}
	defer pusher.Close()
	chats, err := chat.New(chat.Options{DB: db, Box: box, Hermes: hc, Notifier: pusher})
	if err != nil {
		return err
	}
	defer chats.Close()
	identity, err := auth.New(auth.Options{DB: db,
		Endpoints: auth.Endpoints{Issuer: idpURL, Auth: idpURL + "/authorize", Token: idpURL + "/token", JWKS: idpURL + "/jwks", Issued: []string{idpURL}},
		ClientID:  cfg.GoogleClientID, ClientSecret: string(cfg.GoogleClientSecret), PublicOrigin: cfg.PublicOrigin,
		Tenants: tenants, Chat: chats, Push: pusher, Limits: &cfg.Limits})
	if err != nil {
		return err
	}
	srv := &http.Server{Addr: cfg.Addr, Handler: server.New(ready, "").WithAssets(webui.FS()).WithAuth(identity).Routes(),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, IdleTimeout: 120 * time.Second, MaxHeaderBytes: 1 << 16}
	srv.RegisterOnShutdown(chats.Close)
	ln, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		return err
	}
	log.Printf("scratch antares listening on %s", cfg.Addr)
	go func() {
		<-ctx.Done()
		sc, c := context.WithTimeout(context.Background(), 10*time.Second)
		defer c()
		_ = srv.Shutdown(sc)
	}()
	if err := srv.Serve(ln); err != http.ErrServerClosed {
		return err
	}
	return nil
}
