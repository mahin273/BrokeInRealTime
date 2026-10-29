package meta

import (
	"database/sql"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// SessionMeta holds metadata for a single Antigravity session.
type SessionMeta struct {
	ConversationID       string    `json:"conversation_id"`
	Title                string    `json:"title"`
	Preview              string    `json:"preview"`
	StepCount            int       `json:"step_count"`
	LastModifiedTime     time.Time `json:"last_modified_time"`
	Source               string    `json:"source"` // "CLI" or "APP"
	AgentName            string    `json:"agent_name"`
	ParentConversationID string    `json:"parent_conversation_id"`
	NestingDepth         int       `json:"nesting_depth"`
}

// Resolver resolves conversation metadata, sources, and subagent hierarchies.
type Resolver struct {
	dbPath       string
	mu           sync.RWMutex
	sourceCache  map[string]string
	sessionCache map[string]SessionMeta
}

// NewResolver creates a new Resolver for conversation_summaries.db.
func NewResolver(dbPath string) *Resolver {
	r := &Resolver{
		dbPath:       dbPath,
		sourceCache:  make(map[string]string),
		sessionCache: make(map[string]SessionMeta),
	}
	_ = r.Refresh()
	return r
}

func parseTime(s string) time.Time {
	s = strings.TrimSpace(s)
	formats := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05-07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
	}
	for _, f := range formats {
		if t, err := time.Parse(f, s); err == nil {
			return t.Local()
		}
	}
	return time.Now()
}

// Refresh re-reads conversation_summaries.db and updates internal caches.
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

	colRows, err := db.Query("PRAGMA table_info(conversation_summaries);")
	if err != nil {
		return err
	}
	hasCol := make(map[string]bool)
	for colRows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := colRows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err == nil {
			hasCol[strings.ToLower(name)] = true
		}
	}
	colRows.Close()

	if !hasCol["conversation_id"] || !hasCol["source"] {
		return nil
	}

	hasFullMeta := hasCol["title"] && hasCol["step_count"] && hasCol["last_modified_time"]

	newSourceMap := make(map[string]string)
	newSessionMap := make(map[string]SessionMeta)

	if hasFullMeta {
		query := `
			SELECT conversation_id, source, COALESCE(title, ''), COALESCE(preview, ''), 
			       COALESCE(step_count, 0), COALESCE(last_modified_time, ''), 
			       COALESCE(agent_name, ''), COALESCE(parent_conversation_id, ''), COALESCE(nesting_depth, 0)
			FROM conversation_summaries;
		`
		rows, err := db.Query(query)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var convID, rawSource, title, preview, rawTime, agentName, parentID string
				var stepCount, nestingDepth int

				if err := rows.Scan(&convID, &rawSource, &title, &preview, &stepCount, &rawTime, &agentName, &parentID, &nestingDepth); err == nil {
					src := "APP"
					if rawSource == "CORTEX_TRAJECTORY_SOURCE_CLI" {
						src = "CLI"
					}
					newSourceMap[convID] = src

					meta := SessionMeta{
						ConversationID:       convID,
						Title:                title,
						Preview:              preview,
						StepCount:            stepCount,
						LastModifiedTime:     parseTime(rawTime),
						Source:               src,
						AgentName:            agentName,
						ParentConversationID: parentID,
						NestingDepth:         nestingDepth,
					}
					newSessionMap[convID] = meta
				}
			}
		}
	} else {
		rows, err := db.Query("SELECT conversation_id, source FROM conversation_summaries;")
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var convID, rawSource string
				if err := rows.Scan(&convID, &rawSource); err == nil {
					src := "APP"
					if rawSource == "CORTEX_TRAJECTORY_SOURCE_CLI" {
						src = "CLI"
					}
					newSourceMap[convID] = src
					newSessionMap[convID] = SessionMeta{
						ConversationID:   convID,
						Source:           src,
						LastModifiedTime: time.Now(),
					}
				}
			}
		}
	}

	r.mu.Lock()
	for k, v := range newSourceMap {
		r.sourceCache[k] = v
	}
	for k, v := range newSessionMap {
		r.sessionCache[k] = v
	}
	r.mu.Unlock()

	return nil
}

// ResolveSource returns "CLI" or "APP" for a conversation ID.
func (r *Resolver) ResolveSource(conversationID string) string {
	r.mu.RLock()
	val, ok := r.sourceCache[conversationID]
	r.mu.RUnlock()

	if ok {
		return val
	}

	_ = r.Refresh()

	r.mu.RLock()
	defer r.mu.RUnlock()
	if val, ok := r.sourceCache[conversationID]; ok {
		return val
	}

	return "APP"
}

// GetSessionMeta returns metadata for a conversation ID.
func (r *Resolver) GetSessionMeta(conversationID string) (SessionMeta, bool) {
	r.mu.RLock()
	val, ok := r.sessionCache[conversationID]
	r.mu.RUnlock()

	if ok {
		return val, true
	}

	_ = r.Refresh()

	r.mu.RLock()
	defer r.mu.RUnlock()
	val, ok = r.sessionCache[conversationID]
	return val, ok
}

// GetAllSessions returns all cached sessions sorted by last modified descending.
func (r *Resolver) GetAllSessions() []SessionMeta {
	r.mu.RLock()
	defer r.mu.RUnlock()

	list := make([]SessionMeta, 0, len(r.sessionCache))
	for _, s := range r.sessionCache {
		list = append(list, s)
	}

	sort.Slice(list, func(i, j int) bool {
		return list[i].LastModifiedTime.After(list[j].LastModifiedTime)
	})

	return list
}

// GetSubagents returns child sessions whose ParentConversationID matches parentID.
func (r *Resolver) GetSubagents(parentID string) []SessionMeta {
	r.mu.RLock()
	defer r.mu.RUnlock()

	list := make([]SessionMeta, 0)
	for _, s := range r.sessionCache {
		if s.ParentConversationID == parentID && s.ConversationID != parentID {
			list = append(list, s)
		}
	}

	sort.Slice(list, func(i, j int) bool {
		return list[i].LastModifiedTime.Before(list[j].LastModifiedTime)
	})

	return list
}
