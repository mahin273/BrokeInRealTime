package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/mahin273/BrokeInRealTime/internal/aggregator"
	"github.com/mahin273/BrokeInRealTime/internal/config"
	"github.com/mahin273/BrokeInRealTime/internal/parser"
)

func TestServerEndpoints(t *testing.T) {
	cfg := config.DefaultConfig()
	agg := aggregator.NewAggregator(cfg)
	agg.IngestEvent(parser.TokenEvent{
		ID:              "t-1",
		ConversationID:  "c-1",
		OriginSource:    "CLI",
		Model:           "gemini-3.8-flash",
		InputTokens:     1000,
		CacheReadTokens: 4000,
		OutputTokens:    100,
		TotalTokens:     5100,
		IncrementalCost: 0.00036,
		CreatedAt:       time.Now(),
	})

	hub := NewHub(agg)
	go hub.Run()

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", hub.HandleWS)
	mux.HandleFunc("/api/snapshot", func(w http.ResponseWriter, r *http.Request) {
		snap := agg.Snapshot()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(snap)
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	// 1. Test /api/snapshot
	resp, err := http.Get(server.URL + "/api/snapshot")
	if err != nil {
		t.Fatalf("GET /api/snapshot failed: %v", err)
	}
	defer resp.Body.Close()

	var snap aggregator.TelemetrySnapshot
	if err := json.NewDecoder(resp.Body).Decode(&snap); err != nil {
		t.Fatalf("failed to decode snapshot: %v", err)
	}
	if snap.TotalTokens != 5100 {
		t.Errorf("expected 5100 total tokens, got %d", snap.TotalTokens)
	}

	// 2. Test WebSocket connection
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"
	header := http.Header{}
	header.Set("Origin", "http://127.0.0.1:8080")

	ws, _, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		t.Fatalf("WebSocket connection failed: %v", err)
	}
	defer ws.Close()

	// Receive first snapshot
	_, msg, err := ws.ReadMessage()
	if err != nil {
		t.Fatalf("failed to read WS message: %v", err)
	}

	var wsSnap aggregator.TelemetrySnapshot
	if err := json.Unmarshal(msg, &wsSnap); err != nil {
		t.Fatalf("failed to unmarshal WS message: %v", err)
	}
	if wsSnap.TotalTokens != 5100 {
		t.Errorf("expected 5100 tokens from WS message, got %d", wsSnap.TotalTokens)
	}
}

func TestWebSocketOriginRejection(t *testing.T) {
	cfg := config.DefaultConfig()
	agg := aggregator.NewAggregator(cfg)
	hub := NewHub(agg)
	go hub.Run()

	server := httptest.NewServer(http.HandlerFunc(hub.HandleWS))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	header := http.Header{}
	header.Set("Origin", "http://malicious-external-site.com")

	_, resp, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err == nil {
		t.Fatalf("expected WS connection with foreign origin to fail, but it succeeded")
	}
	if resp != nil && resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden, got %d", resp.StatusCode)
	}
}
