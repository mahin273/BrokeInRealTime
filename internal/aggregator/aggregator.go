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

// TelemetrySnapshot is the JSON payload broadcasted to web clients.
type TelemetrySnapshot struct {
	Timestamp       int64               `json:"timestamp"`
	TotalTokens     int64               `json:"total_tokens"`
	TotalInput      int64               `json:"total_input"`
	TotalCached     int64               `json:"total_cached"`
	TotalOutput     int64               `json:"total_output"`
	CacheRatio      float64             `json:"cache_ratio"` // 0.0 - 100.0%
	TotalCost       float64             `json:"total_cost"`
	TotalRawCost    float64             `json:"total_raw_cost"`
	TotalSavings    float64             `json:"total_savings"`
	BurnRatePerHour float64             `json:"burn_rate_per_hour"`
	TokensPerSec    float64             `json:"tokens_per_sec"`
	AppTokens       int64               `json:"app_tokens"`
	CliTokens       int64               `json:"cli_tokens"`
	AppPercent      float64             `json:"app_percent"`
	CliPercent      float64             `json:"cli_percent"`
	BudgetLimitUSD  float64             `json:"budget_limit_usd"`
	BudgetUsedUSD   float64             `json:"budget_used_usd"`
	BudgetPercent   float64             `json:"budget_percent"`
	ProjectedEODUSD float64             `json:"projected_eod_usd"`
	Quota           parser.QuotaCooldown `json:"quota"`
	Throughput60s   []int64             `json:"throughput_60s"` // 60 values (tokens/sec)
	RecentEvents    []parser.TokenEvent `json:"recent_events"`  // Last 30 events
}

// Aggregator coordinates all telemetry accumulation and snapshot production.
type Aggregator struct {
	mu          sync.RWMutex
	cfg         *config.Config
	startTime   time.Time

	// Totals
	totalInput  int64
	totalCached int64
	totalOutput int64
	totalCost   float64
	totalRaw    float64
	totalSave   float64

	appTokens   int64
	cliTokens   int64
	appCost     float64
	cliCost     float64

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

	a.totalInput += evt.InputTokens
	a.totalCached += evt.CacheReadTokens
	a.totalOutput += evt.OutputTokens
	a.totalCost += evt.IncrementalCost
	a.totalRaw += evt.RawCost
	a.totalSave += evt.CacheSavings

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
}

// UpdateQuota sets or updates the quota cooldown status.
func (a *Aggregator) UpdateQuota(qc *parser.QuotaCooldown) {
	if qc == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.quota = *qc
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
	// Velocity extrapolated to hourly burn
	burnRatePerHour := rollingCost60s * 60.0 // 60s * 60 = 1 hour

	totalTokens := a.totalInput + a.totalCached + a.totalOutput
	cacheRatio := 0.0
	totalPrompt := a.totalInput + a.totalCached
	if totalPrompt > 0 {
		cacheRatio = (float64(a.totalCached) / float64(totalPrompt)) * 100.0
	}

	appPercent := 0.0
	cliPercent := 0.0
	if totalTokens > 0 {
		appPercent = (float64(a.appTokens) / float64(totalTokens)) * 100.0
		cliPercent = (float64(a.cliTokens) / float64(totalTokens)) * 100.0
	}

	// Budget calculation
	budgetLimit := a.cfg.Budget.DailyUSDLimit
	budgetUsed := a.totalCost
	budgetPercent := 0.0
	if budgetLimit > 0 {
		budgetPercent = (budgetUsed / budgetLimit) * 100.0
	}

	// Projected EOD (simple linear extrapolation based on elapsed session time today)
	projectedEOD := budgetUsed
	elapsedHours := now.Sub(a.startTime).Hours()
	if elapsedHours > 0.05 {
		hourlyAvg := budgetUsed / elapsedHours
		// estimate 8 active hours of work
		projectedEOD = hourlyAvg * 8.0
	}

	eventsCopy := make([]parser.TokenEvent, len(a.recentEvents))
	copy(eventsCopy, a.recentEvents)

	return TelemetrySnapshot{
		Timestamp:       nowSec,
		TotalTokens:     totalTokens,
		TotalInput:      a.totalInput,
		TotalCached:     a.totalCached,
		TotalOutput:     a.totalOutput,
		CacheRatio:      cacheRatio,
		TotalCost:       a.totalCost,
		TotalRawCost:    a.totalRaw,
		TotalSavings:    a.totalSave,
		BurnRatePerHour: burnRatePerHour,
		TokensPerSec:    tokensPerSec,
		AppTokens:       a.appTokens,
		CliTokens:       a.cliTokens,
		AppPercent:      appPercent,
		CliPercent:      cliPercent,
		BudgetLimitUSD:  budgetLimit,
		BudgetUsedUSD:   budgetUsed,
		BudgetPercent:   budgetPercent,
		ProjectedEODUSD: projectedEOD,
		Quota:           a.quota,
		Throughput60s:   throughput,
		RecentEvents:    eventsCopy,
	}
}
