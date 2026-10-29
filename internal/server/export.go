package server

import (
	"fmt"
	"strings"
	"time"

	"github.com/mahin273/BrokeInRealTime/internal/aggregator"
)

func formatNumber(n int64) string {
	in := fmt.Sprintf("%d", n)
	var out []rune
	for i, r := range in {
		if i > 0 && (len(in)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, r)
	}
	return string(out)
}

// GenerateMarkdownReport converts a TelemetrySnapshot into a formatted Markdown report.
func GenerateMarkdownReport(snap aggregator.TelemetrySnapshot) string {
	var sb strings.Builder

	title := snap.SessionTitle
	if title == "" {
		title = "Antigravity Session"
	}

	sb.WriteString(fmt.Sprintf("# Antigravity Telemetry Report — %s\n\n", title))
	sb.WriteString(fmt.Sprintf("- **Session ID**: `%s`\n", snap.SessionID))
	sb.WriteString(fmt.Sprintf("- **Generated At**: `%s`\n", time.Now().Format("2006-01-02 15:04:05 MST")))
	sb.WriteString(fmt.Sprintf("- **Total Turns**: `%d`\n", snap.StepCount))
	sb.WriteString(fmt.Sprintf("- **Context Health**: `%s`\n\n", snap.ContextHealthMsg))

	sb.WriteString("## 1. Executive Summary\n\n")
	sb.WriteString("| Telemetry Metric | Measured Value |\n")
	sb.WriteString("| :--- | :--- |\n")
	sb.WriteString(fmt.Sprintf("| **Total Tokens** | **%s** |\n", formatNumber(snap.TotalTokens)))
	sb.WriteString(fmt.Sprintf("| **Context Cache Hits** | %s (%.1f%%) |\n", formatNumber(snap.TotalCached), snap.CacheRatio))
	sb.WriteString(fmt.Sprintf("| **Fresh Input Tokens** | %s |\n", formatNumber(snap.TotalInput)))
	sb.WriteString(fmt.Sprintf("| **Output / Thinking Tokens** | %s |\n", formatNumber(snap.TotalOutput)))
	sb.WriteString(fmt.Sprintf("| **Active Context Load** | %s / 1,000,000 (%.1f%%) |\n", formatNumber(snap.ActiveContextTokens), snap.ContextPct))
	sb.WriteString(fmt.Sprintf("| **Estimated Net Cost** | **$%.4f** |\n", snap.TotalCost))
	sb.WriteString(fmt.Sprintf("| **Uncached Theoretical Cost** | $%.4f |\n", snap.TotalRawCost))
	sb.WriteString(fmt.Sprintf("| **Context Caching Savings** | **$%.4f** |\n\n", snap.TotalSavings))

	// Section 2: Tools
	sb.WriteString("## 2. Granular Tool Consumption & Heatmap\n\n")
	if len(snap.ToolStats) > 0 {
		sb.WriteString("| Tool Name | Calls | Fresh Input | Cache Reads | Output | Total Tokens | % Share | Est. Cost |\n")
		sb.WriteString("| :--- | :---: | :---: | :---: | :---: | :---: | :---: | :---: |\n")
		for _, ts := range snap.ToolStats {
			sb.WriteString(fmt.Sprintf("| `%s` | %d | %s | %s | %s | **%s** | %.1f%% | $%.4f |\n",
				ts.Name, ts.Calls, formatNumber(ts.InputTokens), formatNumber(ts.CacheTokens), formatNumber(ts.OutputTokens),
				formatNumber(ts.TotalTokens), ts.Percentage, ts.EstCost))
		}
		sb.WriteString("\n")
	} else {
		sb.WriteString("*No tool invocations recorded for this session.*\n\n")
	}

	// Section 3: Subagents
	sb.WriteString("## 3. Subagent Workflow Tree\n\n")
	if len(snap.Subagents) > 0 {
		sb.WriteString("| Subagent / Role | Conversation ID | Nesting Depth | Steps | Total Tokens | Est. Cost |\n")
		sb.WriteString("| :--- | :--- | :---: | :---: | :---: | :---: |\n")
		for _, sub := range snap.Subagents {
			sb.WriteString(fmt.Sprintf("| **%s** | `%s` | Level %d | %d | %s | $%.4f |\n",
				sub.AgentName, sub.ShortID, sub.NestingDepth, sub.StepCount, formatNumber(sub.Tokens), sub.Cost))
		}
		sb.WriteString("\n")
	} else {
		sb.WriteString("*No autonomous subagents were spawned during this session (ran directly in primary agent).*\n\n")
	}

	// Section 4: Step Ledger
	sb.WriteString("## 4. Step Execution Ledger\n\n")
	if len(snap.Steps) > 0 {
		sb.WriteString("| Step | Time | Tool Calls | Fresh Input | Cache Read | Output | Turn Total | Compaction Note |\n")
		sb.WriteString("| :---: | :---: | :--- | :---: | :---: | :---: | :---: | :--- |\n")
		for _, step := range snap.Steps {
			note := "-"
			if step.IsCompaction {
				note = "⚡ " + step.CompactionNote
			}
			toolStr := step.ToolSummary
			if toolStr == "" {
				toolStr = "chat/reasoning"
			}
			turnTotal := step.InputTokens + step.CacheReadTokens + step.OutputTokens
			sb.WriteString(fmt.Sprintf("| #%d | %s | `%s` | %s | %s | %s | **%s** | %s |\n",
				step.StepIndex, step.TimeStr, toolStr, formatNumber(step.InputTokens), formatNumber(step.CacheReadTokens),
				formatNumber(step.OutputTokens), formatNumber(turnTotal), note))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("---\n*Generated locally by BrokeInRealTime — Passive Antigravity Telemetry.*\n")
	return sb.String()
}
