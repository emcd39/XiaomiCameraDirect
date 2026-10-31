package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/xiaomi-local-gateway/xiaomi-local-gateway/internal/config"
	"github.com/xiaomi-local-gateway/xiaomi-local-gateway/internal/httpapi"
	"github.com/xiaomi-local-gateway/xiaomi-local-gateway/internal/onvif"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	path := flag.String("config", "config.json", "Gateway configuration file")
	check := flag.Bool("check-config", false, "Validate and exit")
	flag.Parse()
	cfg, err := config.Load(*path)
	if err != nil {
		log.Fatal(err)
	}
	if *check {
		fmt.Printf("valid config: %d cameras\n", len(cfg.Cameras))
		return
	}
	token := os.Getenv("XLG_API_TOKEN")
	if token == "" {
		log.Fatal("XLG_API_TOKEN is required")
	}
	if !config.Loopback(cfg.Listen) && len(token) < 24 {
		log.Fatal("XLG_API_TOKEN must be >=24 characters on LAN listeners")
	}
	server := &http.Server{Addr: cfg.Listen, Handler: httpapi.New(cfg, token, onvif.New()), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 20 * time.Second, IdleTimeout: 45 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		timeout, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		defer cancel()
		_ = server.Shutdown(timeout)
	}()
	log.Printf("Xiaomi Local Gateway listening on %s, cameras=%d", cfg.Listen, len(cfg.Cameras))
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
