package meta

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func TestResolver(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "conversation_summaries.db")

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("failed to create sqlite db: %v", err)
	}
	defer db.Close()

	_, err = db.Exec(`CREATE TABLE conversation_summaries (conversation_id TEXT PRIMARY KEY, source TEXT);
INSERT INTO conversation_summaries VALUES ('convo-cli-1', 'CORTEX_TRAJECTORY_SOURCE_CLI');
INSERT INTO conversation_summaries VALUES ('convo-app-1', '');
`)
	if err != nil {
		t.Fatalf("failed to populate test db: %v", err)
	}

	resolver := NewResolver(dbPath)

	if src := resolver.ResolveSource("convo-cli-1"); src != "CLI" {
		t.Errorf("expected CLI, got %s", src)
	}
	if src := resolver.ResolveSource("convo-app-1"); src != "APP" {
		t.Errorf("expected APP, got %s", src)
	}
	if src := resolver.ResolveSource("unknown-id"); src != "APP" {
		t.Errorf("expected fallback APP, got %s", src)
	}
}
