package server

import (
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/mahin273/BrokeInRealTime/internal/aggregator"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 4096,
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true // Native/local tools or same-origin
		}
		u, err := url.Parse(origin)
		if err != nil {
			return false
		}
		host := strings.ToLower(u.Hostname())
		// Strictly allow localhost and 127.0.0.1
		return host == "127.0.0.1" || host == "localhost"
	},
}

// Hub manages active WebSocket client connections and snapshot broadcasting.
type Hub struct {
	agg        *aggregator.Aggregator
	mu         sync.Mutex
	clients    map[*websocket.Conn]bool
	broadcast  chan []byte
	register   chan *websocket.Conn
	unregister chan *websocket.Conn
}

// NewHub initializes a new WebSocket Hub.
func NewHub(agg *aggregator.Aggregator) *Hub {
	return &Hub{
		agg:        agg,
		clients:    make(map[*websocket.Conn]bool),
		broadcast:  make(chan []byte, 32),
		register:   make(chan *websocket.Conn),
		unregister: make(chan *websocket.Conn),
	}
}

// Run starts the WebSocket event pump and periodic 1 Hz snapshot broadcaster.
func (h *Hub) Run() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case client := <-h.register:
			h.mu.Lock()
			h.clients[client] = true
			h.mu.Unlock()
			// Send immediate snapshot upon connect
			snap := h.agg.Snapshot()
			if data, err := json.Marshal(snap); err == nil {
				_ = client.WriteMessage(websocket.TextMessage, data)
			}

		case client := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				client.Close()
			}
			h.mu.Unlock()

		case message := <-h.broadcast:
			h.mu.Lock()
			for client := range h.clients {
				err := client.WriteMessage(websocket.TextMessage, message)
				if err != nil {
					client.Close()
					delete(h.clients, client)
				}
			}
			h.mu.Unlock()

		case <-ticker.C:
			snap := h.agg.Snapshot()
			data, err := json.Marshal(snap)
			if err == nil {
				h.mu.Lock()
				for client := range h.clients {
					err := client.WriteMessage(websocket.TextMessage, data)
					if err != nil {
						client.Close()
						delete(h.clients, client)
					}
				}
				h.mu.Unlock()
			}
		}
	}
}

// HandleWS handles incoming WebSocket connections.
func (h *Hub) HandleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[WebSocket] Upgrade error: %v", err)
		return
	}
	h.register <- conn

	// Keep-alive read pump
	go func() {
		defer func() {
			h.unregister <- conn
		}()
		conn.SetReadLimit(512)
		_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		conn.SetPongHandler(func(string) error {
			_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))
			return nil
		})

		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				break
			}
		}
	}()
}
