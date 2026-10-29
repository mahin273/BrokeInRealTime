package meta

import (
	"database/sql"
	"fmt"
	"os"
	"sync"

	_ "github.com/mattn/go-sqlite3"
)

// Resolver resolves conversation ID to origin source ("CLI" vs "APP").
type Resolver struct {
	dbPath string
	mu     sync.RWMutex
	cache  map[string]string
}

// NewResolver creates a new Resolver for conversation_summaries.db.
func NewResolver(dbPath string) *Resolver {
	r := &Resolver{
		dbPath: dbPath,
		cache:  make(map[string]string),
	}
	r.Refresh()
	return r
}

// Refresh re-reads conversation_summaries.db and updates the cache.
func (r *Resolver) Refresh() error {
	if _, err := os.Stat(r.dbPath); os.IsNotExist(err) {
		return nil // DB does not exist yet; not an error
	}

	dsn := fmt.Sprintf("file:%s?mode=ro&_busy_timeout=2000", r.dbPath)
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return err
	}
	defer db.Close()

	rows, err := db.Query("SELECT conversation_id, source FROM conversation_summaries;")
	if err != nil {
		return err
	}
	defer rows.Close()

	newMap := make(map[string]string)
	for rows.Next() {
		var convID, src string
		if err := rows.Scan(&convID, &src); err == nil {
			if src == "CORTEX_TRAJECTORY_SOURCE_CLI" {
				newMap[convID] = "CLI"
			} else {
				newMap[convID] = "APP"
			}
		}
	}

	r.mu.Lock()
	for k, v := range newMap {
		r.cache[k] = v
	}
	r.mu.Unlock()

	return nil
}

// ResolveSource returns "CLI" or "APP" for a conversation ID.
func (r *Resolver) ResolveSource(conversationID string) string {
	r.mu.RLock()
	val, ok := r.cache[conversationID]
	r.mu.RUnlock()

	if ok {
		return val
	}

	// Try quick refresh if not found
	_ = r.Refresh()

	r.mu.RLock()
	defer r.mu.RUnlock()
	if val, ok := r.cache[conversationID]; ok {
		return val
	}

	return "APP" // default fallback
}
