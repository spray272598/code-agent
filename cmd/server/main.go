package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spray272598/code-agent/internal/bootstrap"
	"github.com/spray272598/code-agent/internal/infrastructure/config"
	"github.com/spray272598/code-agent/internal/observability"
	httpserver "github.com/spray272598/code-agent/internal/trigger/http"
)

func main() {
	cfgPath := flag.String("config", "configs/config.yaml", "config path")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatal(err)
	}

	// OTLP tracing (optional)
	ctx := context.Background()
	shutdownTrace, err := observability.SetupTracer(ctx, observability.OTLPConfig{
		Enabled:  cfg.OTLP.Enabled,
		Endpoint: cfg.OTLP.Endpoint,
		Insecure: cfg.OTLP.Insecure,
		Service:  cfg.OTLP.Service,
	})
	if err != nil {
		log.Printf("[otel] setup failed: %v (continue without export)\n", err)
		shutdownTrace = func(context.Context) error { return nil }
	}
	defer func() {
		cctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = shutdownTrace(cctx)
	}()

	app, err := bootstrap.Build(cfg)
	if err != nil {
		log.Fatal(err)
	}
	defer app.Closer()

	srv := httpserver.New(app.Chat, cfg.Addr()).WithHost(app.HostHub, app.Bridge)
	go func() {
		if err := srv.Start(); err != nil {
			log.Printf("server stopped: %v", err)
		}
	}()

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	<-ch
	sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(sctx)
}
