package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/mahin273/BrokeInRealTime/internal/aggregator"
	"github.com/mahin273/BrokeInRealTime/internal/config"
	"github.com/mahin273/BrokeInRealTime/internal/meta"
	"github.com/mahin273/BrokeInRealTime/internal/server"
	"github.com/mahin273/BrokeInRealTime/internal/watcher"
)

const version = "1.2.0"

func main() {
	port := flag.Int("port", 8080, "Port to listen on (strictly bound to 127.0.0.1)")
	antigravityDir := flag.String("antigravity-dir", "", "Path to Antigravity data dir (defaults to ~/.gemini/antigravity)")
	configPath := flag.String("config", "", "Path to custom pricing.json")
	noBackfill := flag.Bool("no-backfill", false, "Do not backfill existing conversation history on launch")
	showVersion := flag.Bool("version", false, "Print version and exit")

	flag.Parse()

	if *showVersion {
		fmt.Printf("BrokeInRealTime daemon v%s (Antigravity Telemetry)\n", version)
		os.Exit(0)
	}

	log.Printf("==================================================")
	log.Printf("  BrokeInRealTime v%s — Local Telemetry Daemon", version)
	log.Printf("==================================================")

	baseDir := config.GetAntigravityDir(*antigravityDir)
	log.Printf("[Config] Target Antigravity directory: %s", baseDir)

	cfg := config.LoadConfig(*configPath)
	log.Printf("[Config] Loaded pricing matrix with %d models.", len(cfg.Models))

	dbPath := filepath.Join(baseDir, "conversation_summaries.db")
	resolver := meta.NewResolver(dbPath)

	agg := aggregator.NewAggregator(cfg)
	agg.SetResolver(resolver)

	w, err := watcher.NewWatcher(baseDir, cfg, agg, resolver)
	if err != nil {
		log.Fatalf("[Fatal] Failed to initialize file watcher: %v", err)
	}
	defer w.Stop()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	backfill := !*noBackfill
	if err := w.Start(ctx, backfill); err != nil {
		log.Fatalf("[Fatal] Failed to start telemetry watcher: %v", err)
	}

	srv := server.NewServer(*port, agg)

	// Server shutdown channel
	shutdownChan := make(chan os.Signal, 1)
	signal.Notify(shutdownChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		if err := srv.Start(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("[Fatal] HTTP server error: %v", err)
		}
	}()

	log.Printf("[Ready] Dashboard running at: http://127.0.0.1:%d", *port)
	log.Printf("[Ready] Press Ctrl+C to terminate.")

	<-shutdownChan
	log.Println("\n[Shutdown] Terminating BrokeInRealTime daemon...")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("[Shutdown] Server shutdown error: %v", err)
	}

	log.Println("[Shutdown] Cleanup completed. Goodbye!")
}
