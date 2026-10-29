package aggregator

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mahin273/BrokeInRealTime/internal/config"
	"github.com/mahin273/BrokeInRealTime/internal/meta"
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
	IsCompaction    bool   `json:"is_compaction"`
	CompactionNote  string `json:"compaction_note,omitempty"`
}

// SessionStats tracks totals for an individual conversation.
type SessionStats struct {
	ConversationID string
	InputTokens    int64
	CacheTokens    int64
	OutputTokens   int64
	TotalTokens    int64
	TotalCost      float64
	LastActivity   time.Time
	LastContext    int64
}

// TelemetrySnapshot is the JSON payload broadcasted to web clients.
type TelemetrySnapshot struct {
	Timestamp           int64                `json:"timestamp"`
	SessionID           string               `json:"session_id"`
	SessionTitle        string               `json:"session_title"`
	
	// Active Session Scope (What is currently happening in active conversation)
	TotalTokens         int64                `json:"total_tokens"` // Active session tokens
	TotalInput          int64                `json:"total_input"`
	TotalCached         int64                `json:"total_cached"`
	TotalOutput         int64                `json:"total_output"`
	CacheRatio          float64              `json:"cache_ratio"` // 0.0 - 100.0%
	ActiveContextTokens int64                `json:"active_context_tokens"`
	ContextPct          float64              `json:"context_pct"`
	ContextHealth       string               `json:"context_health"`     // "healthy", "warning", "critical"
	ContextHealthMsg    string               `json:"context_health_msg"` // User-facing alert/tip
	TotalCost           float64              `json:"total_cost"`
	TotalRawCost        float64              `json:"total_raw_cost"`
	TotalSavings        float64              `json:"total_savings"`
	StepCount           int                  `json:"step_count"`
	Steps               []StepData           `json:"steps"` // Sequential steps of active conversation

	// Tool & Subagent Deep Breakdown
	ToolStats           []parser.ToolStat    `json:"tool_stats"`
	Subagents           []parser.SubagentInfo `json:"subagents"`

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
	metaRes   *meta.Resolver
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

	// Tool Usage stats
	sessionToolStats map[string]map[string]*parser.ToolStat
	globalToolStats  map[string]*parser.ToolStat

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
		cfg:              cfg,
		startTime:        time.Now(),
		sessionStats:     make(map[string]*SessionStats),
		sessionSteps:     make(map[string][]StepData),
		sessionToolStats: make(map[string]map[string]*parser.ToolStat),
		globalToolStats:  make(map[string]*parser.ToolStat),
		rollingSeconds:   make(map[int64]*TimePoint),
		recentEvents:     make([]parser.TokenEvent, 0, 50),
		quota: parser.QuotaCooldown{
			Active: false,
		},
	}
}

// SetResolver binds the metadata resolver to the aggregator.
func (a *Aggregator) SetResolver(r *meta.Resolver) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.metaRes = r
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
		ss = &SessionStats{ConversationID: convID}
		a.sessionStats[convID] = ss
	}
	ss.InputTokens += evt.InputTokens
	ss.CacheTokens += evt.CacheReadTokens
	ss.OutputTokens += evt.OutputTokens
	ss.TotalTokens += evt.TotalTokens
	ss.TotalCost += evt.IncrementalCost
	ss.LastActivity = evt.CreatedAt
	ss.LastContext = evt.InputTokens + evt.CacheReadTokens

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

	// Tool attribution
	toolNames := make([]string, 0)
	if evt.ToolSummary != "" {
		for _, t := range strings.Split(evt.ToolSummary, ",") {
			trimmed := strings.TrimSpace(t)
			if trimmed != "" {
				toolNames = append(toolNames, trimmed)
			}
		}
	}
	if len(toolNames) == 0 {
		toolNames = append(toolNames, "chat/reasoning")
	}

	count := int64(len(toolNames))
	perIn := evt.InputTokens / count
	perCache := evt.CacheReadTokens / count
	perOut := evt.OutputTokens / count
	perTot := evt.TotalTokens / count
	perCost := evt.IncrementalCost / float64(count)

	for _, name := range toolNames {
		// Session tool stat
		if a.sessionToolStats[convID] == nil {
			a.sessionToolStats[convID] = make(map[string]*parser.ToolStat)
		}
		st, exists := a.sessionToolStats[convID][name]
		if !exists {
			st = &parser.ToolStat{Name: name}
			a.sessionToolStats[convID][name] = st
		}
		st.Calls++
		st.InputTokens += perIn
		st.CacheTokens += perCache
		st.OutputTokens += perOut
		st.TotalTokens += perTot
		st.EstCost += perCost

		// Global tool stat
		gt, gexists := a.globalToolStats[name]
		if !gexists {
			gt = &parser.ToolStat{Name: name}
			a.globalToolStats[name] = gt
		}
		gt.Calls++
		gt.InputTokens += perIn
		gt.CacheTokens += perCache
		gt.OutputTokens += perOut
		gt.TotalTokens += perTot
		gt.EstCost += perCost
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
		IsCompaction:    evt.IsCompaction,
		CompactionNote:  evt.CompactionNote,
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

	if a.quota.Active && time.Now().After(a.quota.ExpiresAt) {
		a.quota.Active = false
		a.quota.Message = ""
	}
}

// Snapshot generates the current TelemetrySnapshot for active session.
func (a *Aggregator) Snapshot() TelemetrySnapshot {
	return a.SnapshotForSession("")
}

// SnapshotForSession returns telemetry snapshot for a specific session ID or the active session.
func (a *Aggregator) SnapshotForSession(targetID string) TelemetrySnapshot {
	now := time.Now()
	nowSec := now.Unix()
	a.CleanupRollingWindow(nowSec)

	a.mu.RLock()
	defer a.mu.RUnlock()

	selectedID := targetID
	if selectedID == "" || selectedID == "active" {
		selectedID = a.activeSessionID
	} else if _, ok := a.sessionStats[selectedID]; !ok {
		// Support prefix matching (e.g. 8-character short ID)
		for fullID := range a.sessionStats {
			if strings.HasPrefix(fullID, selectedID) {
				selectedID = fullID
				break
			}
		}
	}

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
		}
	}

	tokensPerSec := float64(rollingTokens60s) / 60.0
	burnRatePerHour := rollingCost60s * 60.0

	// Resolve Session stats
	var sessionStats *SessionStats
	if selectedID != "" {
		sessionStats = a.sessionStats[selectedID]
	}

	var sessionTokens, sessionIn, sessionCache, sessionOut, sessionContext int64
	var sessionCost, sessionRaw, sessionSave, sessionCacheRatio float64

	if sessionStats != nil {
		sessionTokens = sessionStats.TotalTokens
		sessionIn = sessionStats.InputTokens
		sessionCache = sessionStats.CacheTokens
		sessionOut = sessionStats.OutputTokens
		sessionCost = sessionStats.TotalCost
		sessionContext = sessionStats.LastContext
		totalPrompt := sessionIn + sessionCache
		if totalPrompt > 0 {
			sessionCacheRatio = (float64(sessionCache) / float64(totalPrompt)) * 100.0
		}
		sessionCost, sessionRaw, sessionSave = a.cfg.CalculateCost("gemini-3.8-flash", sessionIn, sessionCache, sessionOut)
	} else {
		sessionTokens = a.globalInput + a.globalCached + a.globalOutput
		sessionIn = a.globalInput
		sessionCache = a.globalCached
		sessionOut = a.globalOutput
		sessionCost = a.globalCost
		sessionRaw = a.globalRaw
		sessionSave = a.globalSave
		sessionContext = a.lastContextTokens
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

	// Clean steps array for selected session
	var stepsCopy []StepData
	if selectedID != "" {
		activeList := a.sessionSteps[selectedID]
		stepsCopy = make([]StepData, len(activeList))
		copy(stepsCopy, activeList)
	}

	sessionShort := selectedID
	if len(sessionShort) > 8 {
		sessionShort = sessionShort[:8]
	}
	if sessionShort == "" {
		sessionShort = "standby"
	}

	sessionTitle := "Antigravity Session"
	if a.metaRes != nil && selectedID != "" {
		if meta, ok := a.metaRes.GetSessionMeta(selectedID); ok && meta.Title != "" {
			sessionTitle = meta.Title
		}
	}

	// Tool stats compilation
	toolStatsList := make([]parser.ToolStat, 0)
	var toolSourceMap map[string]*parser.ToolStat
	if selectedID != "" && a.sessionToolStats[selectedID] != nil {
		toolSourceMap = a.sessionToolStats[selectedID]
	} else {
		toolSourceMap = a.globalToolStats
	}

	for _, ts := range toolSourceMap {
		copied := *ts
		if sessionTokens > 0 {
			copied.Percentage = (float64(copied.TotalTokens) / float64(sessionTokens)) * 100.0
		}
		toolStatsList = append(toolStatsList, copied)
	}
	sort.Slice(toolStatsList, func(i, j int) bool {
		return toolStatsList[i].TotalTokens > toolStatsList[j].TotalTokens
	})

	// Subagent attribution compilation
	subagentsList := make([]parser.SubagentInfo, 0)
	if a.metaRes != nil && selectedID != "" {
		children := a.metaRes.GetSubagents(selectedID)
		for _, ch := range children {
			var tokens int64
			var cost float64
			var steps int
			if chStat, ok := a.sessionStats[ch.ConversationID]; ok {
				tokens = chStat.TotalTokens
				cost = chStat.TotalCost
			}
			if chSteps, ok := a.sessionSteps[ch.ConversationID]; ok {
				steps = len(chSteps)
			}
			if steps == 0 {
				steps = ch.StepCount
			}

			shortID := ch.ConversationID
			if len(shortID) > 8 {
				shortID = shortID[:8]
			}

			subagentsList = append(subagentsList, parser.SubagentInfo{
				ConversationID: ch.ConversationID,
				ShortID:        shortID,
				AgentName:      ch.AgentName,
				NestingDepth:   ch.NestingDepth,
				Tokens:         tokens,
				Cost:           cost,
				StepCount:      steps,
			})
		}
	}

	contextPct := (float64(sessionContext) / 1000000.0) * 100.0
	health := "healthy"
	healthMsg := "Context Load Healthy"
	if sessionContext >= 800000 {
		health = "critical"
		healthMsg = fmt.Sprintf("Critical Context Load: %.1f%% (%d / 1M) — High latency risk! Run /compact now.", contextPct, sessionContext)
	} else if sessionContext >= 500000 {
		health = "warning"
		healthMsg = fmt.Sprintf("Context Bloat Warning: %.1f%% (%d / 1M) — Consider running /compact to optimize cache efficiency.", contextPct, sessionContext)
	}

	return TelemetrySnapshot{
		Timestamp:           nowSec,
		SessionID:           sessionShort,
		SessionTitle:        sessionTitle,
		TotalTokens:         sessionTokens,
		TotalInput:          sessionIn,
		TotalCached:         sessionCache,
		TotalOutput:         sessionOut,
		CacheRatio:          sessionCacheRatio,
		ActiveContextTokens: sessionContext,
		ContextPct:          contextPct,
		ContextHealth:       health,
		ContextHealthMsg:    healthMsg,
		TotalCost:           sessionCost,
		TotalRawCost:        sessionRaw,
		TotalSavings:        sessionSave,
		StepCount:           len(stepsCopy),
		Steps:               stepsCopy,
		ToolStats:           toolStatsList,
		Subagents:           subagentsList,
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

// GetSessionsList returns historical and active sessions for the UI picker.
func (a *Aggregator) GetSessionsList() []parser.SessionSummaryItem {
	a.mu.RLock()
	defer a.mu.RUnlock()

	items := make([]parser.SessionSummaryItem, 0)
	seen := make(map[string]bool)

	// Collect from sessionStats in aggregator
	for id, ss := range a.sessionStats {
		seen[id] = true
		title := "Antigravity Session"
		source := "APP"
		agentName := ""
		parentID := ""
		nesting := 0
		lastAct := ss.LastActivity

		if a.metaRes != nil {
			if m, ok := a.metaRes.GetSessionMeta(id); ok {
				if m.Title != "" {
					title = m.Title
				}
				source = m.Source
				agentName = m.AgentName
				parentID = m.ParentConversationID
				nesting = m.NestingDepth
				if m.LastModifiedTime.After(lastAct) {
					lastAct = m.LastModifiedTime
				}
			}
		}

		shortID := id
		if len(shortID) > 8 {
			shortID = shortID[:8]
		}

		cacheRatio := 0.0
		totalPrompt := ss.InputTokens + ss.CacheTokens
		if totalPrompt > 0 {
			cacheRatio = (float64(ss.CacheTokens) / float64(totalPrompt)) * 100.0
		}

		steps := len(a.sessionSteps[id])

		items = append(items, parser.SessionSummaryItem{
			ID:              id,
			ShortID:         shortID,
			Title:           title,
			Source:          source,
			AgentName:       agentName,
			ParentID:        parentID,
			NestingDepth:    nesting,
			TotalTokens:     ss.TotalTokens,
			TotalCost:       ss.TotalCost,
			StepCount:       steps,
			CacheRatio:      cacheRatio,
			LastActivity:    lastAct,
			LastActivityStr: lastAct.Format("2006-01-02 15:04"),
			IsActive:        (id == a.activeSessionID),
		})
	}

	// Add any sessions known to SQLite metaRes that haven't produced events in aggregator yet
	if a.metaRes != nil {
		for _, m := range a.metaRes.GetAllSessions() {
			if !seen[m.ConversationID] {
				shortID := m.ConversationID
				if len(shortID) > 8 {
					shortID = shortID[:8]
				}
				items = append(items, parser.SessionSummaryItem{
					ID:              m.ConversationID,
					ShortID:         shortID,
					Title:           m.Title,
					Source:          m.Source,
					AgentName:       m.AgentName,
					ParentID:        m.ParentConversationID,
					NestingDepth:    m.NestingDepth,
					TotalTokens:     0,
					TotalCost:       0.0,
					StepCount:       m.StepCount,
					CacheRatio:      0.0,
					LastActivity:    m.LastModifiedTime,
					LastActivityStr: m.LastModifiedTime.Format("2006-01-02 15:04"),
					IsActive:        (m.ConversationID == a.activeSessionID),
				})
			}
		}
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].LastActivity.After(items[j].LastActivity)
	})

	return items
}
