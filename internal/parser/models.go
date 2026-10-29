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
	IsCompaction    bool      `json:"is_compaction"`
	CompactionNote  string    `json:"compaction_note,omitempty"`
}

// QuotaCooldown tracks server rate limit exhaustion (HTTP 429).
type QuotaCooldown struct {
	Active        bool      `json:"active"`
	ResetDuration string    `json:"reset_duration"`
	TriggeredAt   time.Time `json:"triggered_at"`
	ExpiresAt     time.Time `json:"expires_at"`
	Message       string    `json:"message"`
}

// ToolStat records cumulative invocations and token impact for a specific tool.
type ToolStat struct {
	Name         string  `json:"name"`
	Calls        int     `json:"calls"`
	InputTokens  int64   `json:"input_tokens"`
	CacheTokens  int64   `json:"cache_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	TotalTokens  int64   `json:"total_tokens"`
	EstCost      float64 `json:"est_cost"`
	Percentage   float64 `json:"percentage"`
}

// SubagentInfo details child sessions spawned by parent workflows.
type SubagentInfo struct {
	ConversationID string  `json:"conversation_id"`
	ShortID        string  `json:"short_id"`
	AgentName      string  `json:"agent_name"`
	NestingDepth   int     `json:"nesting_depth"`
	Tokens         int64   `json:"tokens"`
	Cost           float64 `json:"cost"`
	StepCount      int     `json:"step_count"`
}

// SessionSummaryItem is returned by /api/sessions for historical browsing.
type SessionSummaryItem struct {
	ID              string    `json:"id"`
	ShortID         string    `json:"short_id"`
	Title           string    `json:"title"`
	Source          string    `json:"source"`
	AgentName       string    `json:"agent_name,omitempty"`
	ParentID        string    `json:"parent_id,omitempty"`
	NestingDepth    int       `json:"nesting_depth"`
	TotalTokens     int64     `json:"total_tokens"`
	TotalCost       float64   `json:"total_cost"`
	StepCount       int       `json:"step_count"`
	CacheRatio      float64   `json:"cache_ratio"`
	LastActivity    time.Time `json:"last_activity"`
	LastActivityStr string    `json:"last_activity_str"`
	IsActive        bool      `json:"is_active"`
}

// PeriodStat represents aggregated consumption for a time period (day, month, or all-time).
type PeriodStat struct {
	PeriodKey    string  `json:"period_key"`   // e.g. "2026-10-30" or "2026-10"
	Label        string  `json:"label"`        // e.g. "Today (Oct 30)", "October 2026", "All-Time"
	TotalTokens  int64   `json:"total_tokens"`
	InputTokens  int64   `json:"input_tokens"`
	CacheTokens  int64   `json:"cache_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	CacheRatio   float64 `json:"cache_ratio"`
	Cost         float64 `json:"cost"`
	Savings      float64 `json:"savings"`
	TurnCount    int     `json:"turn_count"`
}

// ConsumptionSummary provides multi-horizon token expenditure analytics (All-time, Monthly, Daily).
type ConsumptionSummary struct {
	AllTime   PeriodStat   `json:"all_time"`
	ThisMonth PeriodStat   `json:"this_month"`
	Today     PeriodStat   `json:"today"`
	Daily     []PeriodStat `json:"daily"`    // Descending by day
	Monthly   []PeriodStat `json:"monthly"`  // Descending by month
}
