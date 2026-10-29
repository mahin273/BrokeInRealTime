package parser

import (
	"time"
)

// RawStepRecord represents a raw line from transcript.jsonl.
type RawStepRecord struct {
	StepIndex       int64  `json:"step_index"`
	Source          string `json:"source"`
	Type            string `json:"type"`
	Status          string `json:"status"`
	CreatedAt       string `json:"created_at"`
	InputTokens     int64  `json:"input_tokens"`
	CacheReadTokens int64  `json:"cache_read_tokens"`
	OutputTokens    int64  `json:"output_tokens"`
	Content         string `json:"content"`
	Model           string `json:"model,omitempty"`
	Thinking        string `json:"thinking,omitempty"`
	ToolCalls       []struct {
		Name string `json:"name"`
	} `json:"tool_calls,omitempty"`
}

// TokenEvent is a normalized, enriched telemetry event.
type TokenEvent struct {
	ID              string    `json:"id"`
	ConversationID  string    `json:"conversation_id"`
	StepIndex       int64     `json:"step_index"`
	OriginSource    string    `json:"origin_source"` // "CLI" or "APP"
	Model           string    `json:"model"`
	InputTokens     int64     `json:"input_tokens"`
	CacheReadTokens int64     `json:"cache_read_tokens"`
	OutputTokens    int64     `json:"output_tokens"`
	TotalTokens     int64     `json:"total_tokens"`
	CacheRatio      float64   `json:"cache_ratio"` // 0.0 - 100.0%
	IncrementalCost float64   `json:"incremental_cost"`
	RawCost         float64   `json:"raw_cost"`
	CacheSavings    float64   `json:"cache_savings"`
	CreatedAt       time.Time `json:"created_at"`
	ToolsCount      int       `json:"tools_count"`
	ToolSummary     string    `json:"tool_summary,omitempty"`
}

// QuotaCooldown tracks server rate limit exhaustion (HTTP 429).
type QuotaCooldown struct {
	Active        bool      `json:"active"`
	ResetDuration string    `json:"reset_duration"`
	TriggeredAt   time.Time `json:"triggered_at"`
	ExpiresAt     time.Time `json:"expires_at"`
	Message       string    `json:"message"`
}
