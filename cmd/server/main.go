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
	httpserver "github.com/spray272598/code-agent/internal/trigger/http"
)

func main() {
	cfgPath := flag.String("config", "configs/config.yaml", "config path")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatal(err)
	}
	app, err := bootstrap.Build(cfg)
	if err != nil {
		log.Fatal(err)
	}
	defer app.Closer()

	srv := httpserver.New(app.Chat, cfg.Addr())
	go func() {
		if err := srv.Start(); err != nil {
			log.Printf("server stopped: %v", err)
		}
	}()

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	<-ch
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}
