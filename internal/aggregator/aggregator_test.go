package aggregator

import (
	"testing"
	"time"

	"github.com/mahin273/BrokeInRealTime/internal/config"
	"github.com/mahin273/BrokeInRealTime/internal/parser"
)

func TestAggregatorIngestAndSnapshot(t *testing.T) {
	cfg := config.DefaultConfig()
	agg := NewAggregator(cfg)

	now := time.Now()

	// Ingest CLI event: 10k in, 40k cache, 1k out
	agg.IngestEvent(parser.TokenEvent{
		ID:              "cli-1",
		ConversationID:  "c-1",
		StepIndex:       1,
		OriginSource:    "CLI",
		Model:           "gemini-3.8-flash",
		InputTokens:     10000,
		CacheReadTokens: 40000,
		OutputTokens:    1000,
		TotalTokens:     51000,
		CacheRatio:      80.0,
		IncrementalCost: 0.0036,
		RawCost:         0.0081,
		CacheSavings:    0.0045,
		CreatedAt:       now,
		ToolSummary:     "run_command",
	})

	// Ingest APP event: 20k in, 0 cache, 500 out
	agg.IngestEvent(parser.TokenEvent{
		ID:              "app-1",
		ConversationID:  "c-2",
		StepIndex:       2,
		OriginSource:    "APP",
		Model:           "gemini-3.8-flash",
		InputTokens:     20000,
		CacheReadTokens: 0,
		OutputTokens:    500,
		TotalTokens:     20500,
		CacheRatio:      0.0,
		IncrementalCost: 0.0033,
		RawCost:         0.0033,
		CacheSavings:    0.0,
		CreatedAt:       now,
	})

	snap := agg.Snapshot()

	if snap.AllTimeTokens != 71500 {
		t.Errorf("expected 71500 all time tokens, got %d", snap.AllTimeTokens)
	}
	if snap.AllTimeInput != 30000 {
		t.Errorf("expected 30000 all time input tokens, got %d", snap.AllTimeInput)
	}
	if snap.AllTimeCached != 40000 {
		t.Errorf("expected 40000 all time cached tokens, got %d", snap.AllTimeCached)
	}
	if snap.TotalTokens != 20500 && snap.TotalTokens != 51000 {
		t.Errorf("expected active session tokens, got %d", snap.TotalTokens)
	}
	if snap.CliTokens != 51000 {
		t.Errorf("expected 51000 CLI tokens, got %d", snap.CliTokens)
	}
	if snap.AppTokens != 20500 {
		t.Errorf("expected 20500 APP tokens, got %d", snap.AppTokens)
	}
	if len(snap.RecentEvents) != 2 {
		t.Errorf("expected 2 recent events, got %d", len(snap.RecentEvents))
	}
	if len(snap.Throughput60s) != 60 {
		t.Errorf("expected 60 throughput slots, got %d", len(snap.Throughput60s))
	}

	// Verify tool breakdown
	snapForC1 := agg.SnapshotForSession("c-1")
	if len(snapForC1.ToolStats) == 0 {
		t.Errorf("expected tool stats for c-1, got 0")
	} else if snapForC1.ToolStats[0].Name != "run_command" {
		t.Errorf("expected run_command tool stat, got %s", snapForC1.ToolStats[0].Name)
	}

	// Verify sessions list
	sessions := agg.GetSessionsList()
	if len(sessions) < 2 {
		t.Errorf("expected at least 2 sessions, got %d", len(sessions))
	}

	// Verify Multi-Horizon Consumption Summary
	cons := agg.GetConsumptionSummary()
	if cons.AllTime.TotalTokens != 71500 {
		t.Errorf("expected 71500 all time tokens in consumption, got %d", cons.AllTime.TotalTokens)
	}
	if cons.Today.TotalTokens != 71500 {
		t.Errorf("expected 71500 today tokens, got %d", cons.Today.TotalTokens)
	}
	if cons.ThisMonth.TotalTokens != 71500 {
		t.Errorf("expected 71500 this month tokens, got %d", cons.ThisMonth.TotalTokens)
	}
	if len(cons.Daily) != 1 {
		t.Errorf("expected 1 daily entry, got %d", len(cons.Daily))
	}
	if len(cons.Monthly) != 1 {
		t.Errorf("expected 1 monthly entry, got %d", len(cons.Monthly))
	}
	if cons.AllTime.TurnCount != 2 {
		t.Errorf("expected 2 turns in consumption summary, got %d", cons.AllTime.TurnCount)
	}
}
