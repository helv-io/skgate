// Command skgate is a gateway for MCP servers and Grok: Grok OAuth to virtual API keys, plus an authenticated MCP proxy.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
	// Embeds the zone database so TZ works in images without tzdata.
	_ "time/tzdata"

	"github.com/helv-io/skgate/internal/app"
	"github.com/helv-io/skgate/internal/config"
	"github.com/helv-io/skgate/internal/store"
	"github.com/helv-io/skgate/internal/timefmt"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(healthcheck())
	}
	if err := refuseNobody(os.Geteuid(), os.Getegid()); err != nil {
		log.Fatalf("refusing to start: %v", err)
	}
	cfg := config.Load()
	// Container starts as root so the data dir can be chowned, then we drop to PUID:PGID.
	if err := prepareAndDrop(cfg.DBPath); err != nil {
		log.Fatalf("refusing to start: %v", err)
	}
	if err := refuseNobody(os.Geteuid(), os.Getegid()); err != nil {
		log.Fatalf("refusing to start: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(cfg.DBPath), 0o750); err != nil {
		log.Fatalf("data dir: %v", err)
	}
	db, err := store.OpenWith(cfg.DBPath, cfg.SecretsKey)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer db.Close()
	privateDB(cfg.DBPath)
	log.Printf("secrets: encryption key from %s", db.SecretsSource)
	for _, line := range startupNotes(cfg) {
		log.Print(line)
	}
	a := app.New(cfg, db) // also moves the old global upstream settings to the provider
	config.MigrateLegacyEnv(db)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	for _, p := range a.Providers.List() {
		go p.Run(ctx)
	}
	a.MCP.StartManaged()
	defer a.MCP.ShutdownManaged()
	go a.Keys.RunPurge(ctx, 24*time.Hour)
	go a.Keys.RunUsage(ctx, 5*time.Second)
	defer func() { _ = a.Keys.FlushUsage() }() // totals of the last seconds before exit
	srv := &http.Server{Addr: cfg.Listen, Handler: a.Handler(), ReadHeaderTimeout: 15 * time.Second, IdleTimeout: 120 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, c := context.WithTimeout(context.Background(), 10*time.Second)
		defer c()
		_ = srv.Shutdown(sctx)
		a.MCP.ShutdownManaged() // children stop even if a handler is still draining
	}()
	log.Printf("skgate %s listening on %s (public %s, log level %s, time zone %s, redirect origins: any https plus loopback http)", config.Version, cfg.Listen, cfg.PublicURL, cfg.LogLevel, timefmt.Zone())
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

// healthcheck lets a distroless image (no curl) probe itself: `skgate healthcheck`.
func healthcheck() int {
	addr := os.Getenv("LISTEN_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	if addr[0] == ':' {
		addr = "127.0.0.1" + addr
	}
	c := http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get("http://" + addr + "/healthz")
	if err != nil || resp.StatusCode != 200 {
		return 1
	}
	return 0
}
