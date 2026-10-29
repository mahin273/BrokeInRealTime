package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"time"

	"github.com/mahin273/BrokeInRealTime/internal/aggregator"
	"github.com/mahin273/BrokeInRealTime/web"
)

// Server coordinates HTTP & WebSocket endpoints.
type Server struct {
	port       int
	agg        *aggregator.Aggregator
	hub        *Hub
	httpServer *http.Server
}

// NewServer initializes the Server instance.
func NewServer(port int, agg *aggregator.Aggregator) *Server {
	hub := NewHub(agg)
	return &Server{
		port: port,
		agg:  agg,
		hub:  hub,
	}
}

// Start boots the WebSocket hub and HTTP server.
func (s *Server) Start() error {
	go s.hub.Run()

	mux := http.NewServeMux()

	// WebSocket route
	mux.HandleFunc("/ws", s.hub.HandleWS)

	// API Snapshot route for healthchecks and headless queries
	mux.HandleFunc("/api/snapshot", func(w http.ResponseWriter, r *http.Request) {
		snap := s.agg.Snapshot()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(snap)
	})

	// Static Assets route
	webFS, err := fs.Sub(web.Content, ".")
	if err != nil {
		return fmt.Errorf("failed to mount web assets: %w", err)
	}
	mux.Handle("/", http.FileServer(http.FS(webFS)))

	addr := fmt.Sprintf("127.0.0.1:%d", s.port)
	s.httpServer = &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Printf("[Server] BrokeInRealTime listening strictly on http://%s", addr)
	return s.httpServer.ListenAndServe()
}

// Shutdown gracefully terminates the server.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.httpServer != nil {
		return s.httpServer.Shutdown(ctx)
	}
	return nil
}
