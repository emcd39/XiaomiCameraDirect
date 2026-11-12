package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"xlg-diagconsole/internal/console"
)

func main() {
	path := flag.String("config", "config.json", "Path to local JSON configuration (no secrets in this file)")
	flag.Parse()
	cfg, err := console.LoadConfig(*path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "CONFIG ERROR:", err)
		os.Exit(2)
	}
	s, err := console.NewServer(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "STARTUP ERROR:", err)
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		timeoutCtx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		defer cancel()
		if err := s.Shutdown(timeoutCtx); err != nil {
			log.Printf("graceful shutdown incomplete: %v", err)
		}
	}()

	if err := s.Run(); err != nil {
		log.Fatal(err)
	}
}
