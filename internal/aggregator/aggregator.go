package aggregator

import (
	"sync"
	"time"

	"github.com/mahin273/BrokeInRealTime/internal/config"
	"github.com/mahin273/BrokeInRealTime/internal/parser"
)

// TimePoint records token and cost delta at a second.
type TimePoint struct {
	Timestamp int64   `json:"timestamp"` // Unix second
	Tokens    int64   `json:"tokens"`
	Cost      float64 `json:"cost"`
}

// StepData represents a single turn in the timeline and ledger.
type StepData struct {
	StepIndex       int64  `json:"step_index"`
	TimeStr         string `json:"time_str"` // e.g. "17:27:24"
	InputTokens     int64  `json:"input_tokens"`
	CacheReadTokens int64  `json:"cache_read_tokens"`
	OutputTokens    int64  `json:"output_tokens"`
	ToolsCount      int    `json:"tools_count"`
	OriginSource    string `json:"origin_source"` // "CLI" or "APP"
	Model           string `json:"model"`
	ToolSummary     string `json:"tool_summary,omitempty"`
}

// SessionStats tracks totals for an individual conversation.
type SessionStats struct {
	InputTokens  int64
	CacheTokens  int64
	OutputTokens int64
	TotalTokens  int64
	TotalCost    float64
	LastActivity time.Time
}

// TelemetrySnapshot is the JSON payload broadcasted to web clients.
type TelemetrySnapshot struct {
	Timestamp           int64                `json:"timestamp"`
	SessionID           string               `json:"session_id"`
	
	// Active Session Scope (What is currently happening in active conversation)
	TotalTokens         int64                `json:"total_tokens"` // Active session tokens
	TotalInput          int64                `json:"total_input"`
	TotalCached         int64                `json:"total_cached"`
	TotalOutput         int64                `json:"total_output"`
	CacheRatio          float64              `json:"cache_ratio"` // 0.0 - 100.0%
	ActiveContextTokens int64                `json:"active_context_tokens"`
	ContextPct          float64              `json:"context_pct"`
	TotalCost           float64              `json:"total_cost"`
	TotalRawCost        float64              `json:"total_raw_cost"`
	TotalSavings        float64              `json:"total_savings"`
	StepCount           int                  `json:"step_count"`
	Steps               []StepData           `json:"steps"` // Sequential steps of active conversation

	// Global Machine Scope (All conversations on this workstation)
	AllTimeTokens       int64                `json:"all_time_tokens"`
	AllTimeCost         float64              `json:"all_time_cost"`
	AllTimeInput        int64                `json:"all_time_input"`
	AllTimeCached       int64                `json:"all_time_cached"`
	AllTimeOutput       int64                `json:"all_time_output"`
	TotalSessions       int                  `json:"total_sessions"`

	BurnRatePerHour     float64              `json:"burn_rate_per_hour"`
	TokensPerSec        float64              `json:"tokens_per_sec"`
	AppTokens           int64                `json:"app_tokens"`
	CliTokens           int64                `json:"cli_tokens"`
	AppPercent          float64              `json:"app_percent"`
	CliPercent          float64              `json:"cli_percent"`
	Quota               parser.QuotaCooldown `json:"quota"`
	Throughput60s       []int64              `json:"throughput_60s"` // 60 values (tokens/sec)
	RecentEvents        []parser.TokenEvent  `json:"recent_events"`  // Last 30 events
}

// Aggregator coordinates all telemetry accumulation and snapshot production.
type Aggregator struct {
	mu        sync.RWMutex
	cfg       *config.Config
	startTime time.Time

	activeSessionID string
	latestEventTime time.Time

	// All-time Totals across all sessions on disk
	globalInput  int64
	globalCached int64
	globalOutput int64
	globalCost   float64
	globalRaw    float64
	globalSave   float64

	// Source attribution totals
	appTokens int64
	cliTokens int64
	appCost   float64
	cliCost   float64

	lastContextTokens int64

	// Per-session tracking
	sessionStats map[string]*SessionStats
	sessionSteps map[string][]StepData

	// Rolling window (last 60 seconds, keyed by unix second)
	rollingSeconds map[int64]*TimePoint

	// Recent events buffer (FIFO up to 50)
	recentEvents []parser.TokenEvent

	// Quota status
	quota parser.QuotaCooldown
}

// NewAggregator creates an initialized Aggregator.
func NewAggregator(cfg *config.Config) *Aggregator {
	return &Aggregator{
		cfg:            cfg,
		startTime:      time.Now(),
		sessionStats:   make(map[string]*SessionStats),
		sessionSteps:   make(map[string][]StepData),
		rollingSeconds: make(map[int64]*TimePoint),
		recentEvents:   make([]parser.TokenEvent, 0, 50),
		quota: parser.QuotaCooldown{
			Active: false,
		},
	}
}

// IngestEvent records a new token event.
func (a *Aggregator) IngestEvent(evt parser.TokenEvent) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.globalInput += evt.InputTokens
	a.globalCached += evt.CacheReadTokens
	a.globalOutput += evt.OutputTokens
	a.globalCost += evt.IncrementalCost
	a.globalRaw += evt.RawCost
	a.globalSave += evt.CacheSavings

	// Per-session accumulation
	convID := evt.ConversationID
	ss, ok := a.sessionStats[convID]
	if !ok {
		ss = &SessionStats{}
		a.sessionStats[convID] = ss
	}
	ss.InputTokens += evt.InputTokens
	ss.CacheTokens += evt.CacheReadTokens
	ss.OutputTokens += evt.OutputTokens
	ss.TotalTokens += evt.TotalTokens
	ss.TotalCost += evt.IncrementalCost
	ss.LastActivity = evt.CreatedAt

	// Mark active session as the conversation with the most recent event timestamp
	if a.activeSessionID == "" || evt.CreatedAt.After(a.latestEventTime) {
		a.activeSessionID = convID
		a.latestEventTime = evt.CreatedAt
		a.lastContextTokens = evt.InputTokens + evt.CacheReadTokens
	}

	if evt.OriginSource == "CLI" {
		a.cliTokens += evt.TotalTokens
		a.cliCost += evt.IncrementalCost
	} else {
		a.appTokens += evt.TotalTokens
		a.appCost += evt.IncrementalCost
	}

	sec := evt.CreatedAt.Unix()
	tp, ok := a.rollingSeconds[sec]
	if !ok {
		tp = &TimePoint{Timestamp: sec}
		a.rollingSeconds[sec] = tp
	}
	tp.Tokens += evt.TotalTokens
	tp.Cost += evt.IncrementalCost

	// Prepend to recent events (FIFO limit 30)
	a.recentEvents = append([]parser.TokenEvent{evt}, a.recentEvents...)
	if len(a.recentEvents) > 30 {
		a.recentEvents = a.recentEvents[:30]
	}

	// Append to this session's step history (up to 1500 per session)
	stepItem := StepData{
		StepIndex:       evt.StepIndex,
		TimeStr:         evt.CreatedAt.Format("15:04:05"),
		InputTokens:     evt.InputTokens,
		CacheReadTokens: evt.CacheReadTokens,
		OutputTokens:    evt.OutputTokens,
		ToolsCount:      evt.ToolsCount,
		OriginSource:    evt.OriginSource,
		Model:           evt.Model,
		ToolSummary:     evt.ToolSummary,
	}
	stepsList := a.sessionSteps[convID]
	stepsList = append(stepsList, stepItem)
	if len(stepsList) > 1500 {
		stepsList = stepsList[len(stepsList)-1500:]
	}
	a.sessionSteps[convID] = stepsList
}

// UpdateQuota sets or updates the quota cooldown status.
func (a *Aggregator) UpdateQuota(qc *parser.QuotaCooldown) {
	if qc == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	// Only set active if expiresAt is in the future
	if time.Now().Before(qc.ExpiresAt) {
		a.quota = *qc
	} else {
		a.quota.Active = false
		a.quota.Message = ""
	}
}

// CleanupRollingWindow removes points older than 60 seconds.
func (a *Aggregator) CleanupRollingWindow(nowSec int64) {
	a.mu.Lock()
	defer a.mu.Unlock()

	threshold := nowSec - 70
	for sec := range a.rollingSeconds {
		if sec < threshold {
			delete(a.rollingSeconds, sec)
		}
	}

	// Expire quota cooldown if elapsed
	if a.quota.Active && time.Now().After(a.quota.ExpiresAt) {
		a.quota.Active = false
		a.quota.Message = ""
	}
}

// Snapshot generates the current TelemetrySnapshot.
func (a *Aggregator) Snapshot() TelemetrySnapshot {
	now := time.Now()
	nowSec := now.Unix()
	a.CleanupRollingWindow(nowSec)

	a.mu.RLock()
	defer a.mu.RUnlock()

	// Compute 60s throughput array
	throughput := make([]int64, 60)
	var rollingTokens60s int64 = 0
	var rollingCost60s float64 = 0.0

	for i := 0; i < 60; i++ {
		sec := nowSec - int64(59-i)
		if tp, ok := a.rollingSeconds[sec]; ok {
			throughput[i] = tp.Tokens
			rollingTokens60s += tp.Tokens
			rollingCost60s += tp.Cost
		} else {
			throughput[i] = 0
		}
	}

	tokensPerSec := float64(rollingTokens60s) / 60.0
	burnRatePerHour := rollingCost60s * 60.0 // 60s * 60 = 1 hour

	// Resolve Active Session stats
	var activeStats *SessionStats
	if a.activeSessionID != "" {
		activeStats = a.sessionStats[a.activeSessionID]
	}

	var sessionTokens, sessionIn, sessionCache, sessionOut int64
	var sessionCost, sessionRaw, sessionSave, sessionCacheRatio float64

	if activeStats != nil {
		sessionTokens = activeStats.TotalTokens
		sessionIn = activeStats.InputTokens
		sessionCache = activeStats.CacheTokens
		sessionOut = activeStats.OutputTokens
		sessionCost = activeStats.TotalCost
		totalPrompt := sessionIn + sessionCache
		if totalPrompt > 0 {
			sessionCacheRatio = (float64(sessionCache) / float64(totalPrompt)) * 100.0
		}
		sessionCost, sessionRaw, sessionSave = a.cfg.CalculateCost("gemini-3.8-flash", sessionIn, sessionCache, sessionOut)
	} else {
		// Fallback to global totals if no session selected
		sessionTokens = a.globalInput + a.globalCached + a.globalOutput
		sessionIn = a.globalInput
		sessionCache = a.globalCached
		sessionOut = a.globalOutput
		sessionCost = a.globalCost
		sessionRaw = a.globalRaw
		sessionSave = a.globalSave
		totalPrompt := sessionIn + sessionCache
		if totalPrompt > 0 {
			sessionCacheRatio = (float64(sessionCache) / float64(totalPrompt)) * 100.0
		}
	}

	allTimeTotalTokens := a.globalInput + a.globalCached + a.globalOutput
	appPercent := 0.0
	cliPercent := 0.0
	if allTimeTotalTokens > 0 {
		appPercent = (float64(a.appTokens) / float64(allTimeTotalTokens)) * 100.0
		cliPercent = (float64(a.cliTokens) / float64(allTimeTotalTokens)) * 100.0
	}

	eventsCopy := make([]parser.TokenEvent, len(a.recentEvents))
	copy(eventsCopy, a.recentEvents)

	// Clean steps array for active session
	var stepsCopy []StepData
	if a.activeSessionID != "" {
		activeList := a.sessionSteps[a.activeSessionID]
		stepsCopy = make([]StepData, len(activeList))
		copy(stepsCopy, activeList)
	}

	sessionShort := a.activeSessionID
	if len(sessionShort) > 8 {
		sessionShort = sessionShort[:8]
	}
	if sessionShort == "" {
		sessionShort = "standby"
	}

	contextPct := (float64(a.lastContextTokens) / 1000000.0) * 100.0

	return TelemetrySnapshot{
		Timestamp:           nowSec,
		SessionID:           sessionShort,
		TotalTokens:         sessionTokens,
		TotalInput:          sessionIn,
		TotalCached:         sessionCache,
		TotalOutput:         sessionOut,
		CacheRatio:          sessionCacheRatio,
		ActiveContextTokens: a.lastContextTokens,
		ContextPct:          contextPct,
		TotalCost:           sessionCost,
		TotalRawCost:        sessionRaw,
		TotalSavings:        sessionSave,
		StepCount:           len(stepsCopy),
		Steps:               stepsCopy,
		AllTimeTokens:       allTimeTotalTokens,
		AllTimeCost:         a.globalCost,
		AllTimeInput:        a.globalInput,
		AllTimeCached:       a.globalCached,
		AllTimeOutput:       a.globalOutput,
		TotalSessions:       len(a.sessionStats),
		BurnRatePerHour:     burnRatePerHour,
		TokensPerSec:        tokensPerSec,
		AppTokens:           a.appTokens,
		CliTokens:           a.cliTokens,
		AppPercent:          appPercent,
		CliPercent:          cliPercent,
		Quota:               a.quota,
		Throughput60s:       throughput,
		RecentEvents:        eventsCopy,
	}
}
