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
	mux.HandleFunc("/api/sessions", func(w http.ResponseWriter, r *http.Request) {
		sessions := agg.GetSessionsList()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(sessions)
	})
	mux.HandleFunc("/api/consumption", func(w http.ResponseWriter, r *http.Request) {
		consumption := agg.GetConsumptionSummary()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(consumption)
	})
	mux.HandleFunc("/api/export", func(w http.ResponseWriter, r *http.Request) {
		snap := agg.Snapshot()
		md := GenerateMarkdownReport(snap)
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		_, _ = w.Write([]byte(md))
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

	// 2. Test /api/sessions
	respSessions, err := http.Get(server.URL + "/api/sessions")
	if err != nil {
		t.Fatalf("GET /api/sessions failed: %v", err)
	}
	defer respSessions.Body.Close()
	var sessions []parser.SessionSummaryItem
	if err := json.NewDecoder(respSessions.Body).Decode(&sessions); err != nil {
		t.Fatalf("failed to decode sessions: %v", err)
	}
	if len(sessions) == 0 {
		t.Errorf("expected at least 1 session in list, got 0")
	}

	// 3. Test /api/export
	respExport, err := http.Get(server.URL + "/api/export?session_id=c-1&format=markdown")
	if err != nil {
		t.Fatalf("GET /api/export failed: %v", err)
	}
	defer respExport.Body.Close()
	if respExport.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK for export, got %d", respExport.StatusCode)
	}

	// 4. Test /api/consumption
	respCons, err := http.Get(server.URL + "/api/consumption")
	if err != nil {
		t.Fatalf("GET /api/consumption failed: %v", err)
	}
	defer respCons.Body.Close()
	var cons parser.ConsumptionSummary
	if err := json.NewDecoder(respCons.Body).Decode(&cons); err != nil {
		t.Fatalf("failed to decode consumption summary: %v", err)
	}
	if cons.AllTime.TotalTokens != 5100 {
		t.Errorf("expected 5100 all time tokens in consumption summary, got %d", cons.AllTime.TotalTokens)
	}

	// 5. Test WebSocket connection
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
