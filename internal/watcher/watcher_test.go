package watcher

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/mahin273/BrokeInRealTime/internal/aggregator"
	"github.com/mahin273/BrokeInRealTime/internal/config"
	"github.com/mahin273/BrokeInRealTime/internal/meta"
)

func TestWatcherLiveIngestion(t *testing.T) {
	baseDir := t.TempDir()
	dbPath := filepath.Join(baseDir, "conversation_summaries.db")

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	_, err = db.Exec(`CREATE TABLE conversation_summaries (conversation_id TEXT PRIMARY KEY, source TEXT);
INSERT INTO conversation_summaries VALUES ('c-cli-test', 'CORTEX_TRAJECTORY_SOURCE_CLI');
`)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}
	db.Close()

	cfg := config.DefaultConfig()
	agg := aggregator.NewAggregator(cfg)
	resolver := meta.NewResolver(dbPath)

	w, err := NewWatcher(baseDir, cfg, agg, resolver)
	if err != nil {
		t.Fatalf("NewWatcher failed: %v", err)
	}
	defer w.Stop()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := w.Start(ctx, true); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Create conversation folder
	convDir := filepath.Join(baseDir, "brain", "c-cli-test", ".system_generated", "logs")
	if err := os.MkdirAll(convDir, 0755); err != nil {
		t.Fatalf("failed to create logs dir: %v", err)
	}

	transcriptPath := filepath.Join(convDir, "transcript.jsonl")
	stepLine := fmt.Sprintf(`{"step_index":1,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"%s","input_tokens":5000,"cache_read_tokens":15000,"output_tokens":300}
`, time.Now().Format(time.RFC3339))

	if err := os.WriteFile(transcriptPath, []byte(stepLine), 0644); err != nil {
		t.Fatalf("failed to write transcript: %v", err)
	}

	// Wait up to 3 seconds for watcher or periodic poll to ingest
	var totalTokens int64
	for i := 0; i < 30; i++ {
		time.Sleep(100 * time.Millisecond)
		snap := agg.Snapshot()
		if snap.TotalTokens > 0 {
			totalTokens = snap.TotalTokens
			break
		}
	}

	if totalTokens != 20300 { // 5000 + 15000 + 300
		t.Fatalf("expected 20300 total tokens ingested by watcher, got %d", totalTokens)
	}

	snap := agg.Snapshot()
	if snap.CliTokens != 20300 {
		t.Errorf("expected 20300 CLI tokens, got %d", snap.CliTokens)
	}
	if snap.CacheRatio < 74.0 || snap.CacheRatio > 76.0 { // 15000 / 20000 = 75%
		t.Errorf("expected ~75%% cache ratio, got %f", snap.CacheRatio)
	}
}
