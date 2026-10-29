package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"strings"
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
		sessionID := r.URL.Query().Get("session_id")
		snap := s.agg.SnapshotForSession(sessionID)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(snap)
	})

	// API Sessions route listing historical conversations
	mux.HandleFunc("/api/sessions", func(w http.ResponseWriter, r *http.Request) {
		sessions := s.agg.GetSessionsList()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(sessions)
	})

	// API Export route for Markdown & JSON telemetry reports
	mux.HandleFunc("/api/export", func(w http.ResponseWriter, r *http.Request) {
		sessionID := r.URL.Query().Get("session_id")
		format := strings.ToLower(r.URL.Query().Get("format"))
		if format == "" {
			format = "markdown"
		}

		snap := s.agg.SnapshotForSession(sessionID)
		fileSession := snap.SessionID
		if fileSession == "" || fileSession == "standby" {
			fileSession = "all"
		}

		if format == "json" {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"antigravity-%s-telemetry.json\"", fileSession))
			_ = json.NewEncoder(w).Encode(snap)
			return
		}

		// Markdown format
		md := GenerateMarkdownReport(snap)
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"antigravity-%s-report.md\"", fileSession))
		_, _ = w.Write([]byte(md))
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
