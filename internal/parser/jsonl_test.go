package parser

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mahin273/BrokeInRealTime/internal/config"
)

func TestParseResetDuration(t *testing.T) {
	cases := []struct {
		input    string
		expected time.Duration
	}{
		{"3h43m18s", 3*time.Hour + 43*time.Minute + 18*time.Second},
		{"45m20s", 45*time.Minute + 20*time.Second},
		{"30s", 30 * time.Second},
		{"2h", 2 * time.Hour},
	}

	for _, tc := range cases {
		got := ParseResetDuration(tc.input)
		if got != tc.expected {
			t.Errorf("for %q, expected %v, got %v", tc.input, tc.expected, got)
		}
	}
}

func TestParseIncremental(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "transcript.jsonl")

	nowStr := time.Now().UTC().Format(time.RFC3339)
	lines := `{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","created_at":"` + nowStr + `","content":"hello"}
{"step_index":1,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"` + nowStr + `","input_tokens":1000,"cache_read_tokens":4000,"output_tokens":200,"tool_calls":[{"name":"read_file"}]}
{"step_index":2,"source":"SYSTEM","type":"SYSTEM_MESSAGE","status":"DONE","created_at":"` + nowStr + `","content":"[Message] RESOURCE_EXHAUSTED (code 429): Individual quota reached. Resets in 2h15m30s."}
`
	if err := os.WriteFile(logPath, []byte(lines), 0644); err != nil {
		t.Fatalf("failed to write test log: %v", err)
	}

	cfg := config.DefaultConfig()
	cursor := &FileCursor{
		Path:           logPath,
		Offset:         0,
		ConversationID: "test-convo-123",
		OriginSource:   "CLI",
		DefaultModel:   "gemini-3.8-flash",
	}

	result, err := ParseIncremental(cursor, cfg)
	if err != nil {
		t.Fatalf("ParseIncremental failed: %v", err)
	}

	if len(result.Events) != 1 {
		t.Fatalf("expected 1 token event, got %d", len(result.Events))
	}

	evt := result.Events[0]
	if evt.InputTokens != 1000 || evt.CacheReadTokens != 4000 || evt.OutputTokens != 200 {
		t.Errorf("unexpected tokens: in=%d, cache=%d, out=%d", evt.InputTokens, evt.CacheReadTokens, evt.OutputTokens)
	}
	if evt.CacheRatio != 80.0 { // 4000 / (1000 + 4000) = 80%
		t.Errorf("expected 80.0%% cache ratio, got %f", evt.CacheRatio)
	}
	if evt.OriginSource != "CLI" {
		t.Errorf("expected source CLI, got %s", evt.OriginSource)
	}
	if evt.ToolSummary != "read_file" {
		t.Errorf("expected tool read_file, got %s", evt.ToolSummary)
	}

	if result.Cooling == nil || !result.Cooling.Active {
		t.Fatalf("expected active quota cooldown")
	}
	if result.Cooling.ResetDuration != "2h15m30s" {
		t.Errorf("expected reset duration 2h15m30s, got %s", result.Cooling.ResetDuration)
	}

	// Now append a second line and parse again incrementally from previous offset
	moreLines := `{"step_index":3,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-10-29T17:22:00Z","input_tokens":500,"cache_read_tokens":500,"output_tokens":50}
`
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("failed to open log for append: %v", err)
	}
	f.WriteString(moreLines)
	f.Close()

	result2, err := ParseIncremental(cursor, cfg)
	if err != nil {
		t.Fatalf("second ParseIncremental failed: %v", err)
	}
	if len(result2.Events) != 1 {
		t.Fatalf("expected 1 new event on second parse, got %d", len(result2.Events))
	}
	if result2.Events[0].StepIndex != 3 {
		t.Errorf("expected step_index 3, got %d", result2.Events[0].StepIndex)
	}
}
