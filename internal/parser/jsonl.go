package parser

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mahin273/BrokeInRealTime/internal/config"
)

var (
	// Matches: Resets in 3h43m18s or Resets in 45m20s or Resets in 12s
	quotaRegex = regexp.MustCompile(`RESOURCE_EXHAUSTED \(code 429\):.*?Resets in\s+([0-9a-zA-Z]+)`)
	// Duration regex to parse strings like 3h43m18s, 4m20s, 30s
	durationComponentRegex = regexp.MustCompile(`(?:(\d+)d)?(?:(\d+)h)?(?:(\d+)m)?(?:(\d+)s)?`)
)

// ParseResetDuration parses human durations like "3h43m18s" or "4m20s" into time.Duration.
func ParseResetDuration(s string) time.Duration {
	s = strings.TrimSpace(s)
	// Try standard time.ParseDuration first
	if d, err := time.ParseDuration(s); err == nil {
		return d
	}

	// Try extracting days, hours, mins, secs
	matches := durationComponentRegex.FindStringSubmatch(s)
	if len(matches) > 0 {
		var total time.Duration
		if matches[1] != "" {
			if days, err := strconv.Atoi(matches[1]); err == nil {
				total += time.Duration(days) * 24 * time.Hour
			}
		}
		if matches[2] != "" {
			if hours, err := strconv.Atoi(matches[2]); err == nil {
				total += time.Duration(hours) * time.Hour
			}
		}
		if matches[3] != "" {
			if mins, err := strconv.Atoi(matches[3]); err == nil {
				total += time.Duration(mins) * time.Minute
			}
		}
		if matches[4] != "" {
			if secs, err := strconv.Atoi(matches[4]); err == nil {
				total += time.Duration(secs) * time.Second
			}
		}
		if total > 0 {
			return total
		}
	}

	// Fallback default
	return 1 * time.Hour
}

// FileCursor tracks incremental reading of a single file.
type FileCursor struct {
	Path              string
	Offset            int64
	ConversationID    string
	OriginSource      string
	DefaultModel      string
	LastCacheRead     int64
	PendingCompaction bool
	CompactionNote    string
}

// ParseResult holds new token events and quota updates parsed from lines.
type ParseResult struct {
	Events  []TokenEvent
	Cooling *QuotaCooldown
}

// ParseIncremental reads newly appended lines from a transcript file starting from cursor.Offset.
func ParseIncremental(cursor *FileCursor, cfg *config.Config) (*ParseResult, error) {
	file, err := os.Open(cursor.Path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		return nil, err
	}

	// If file was truncated/recreated, reset offset
	if stat.Size() < cursor.Offset {
		cursor.Offset = 0
	}

	if _, err := file.Seek(cursor.Offset, io.SeekStart); err != nil {
		return nil, err
	}

	reader := bufio.NewReader(file)
	result := &ParseResult{}

	var bytesRead int64 = 0

	for {
		lineBytes, err := reader.ReadBytes('\n')
		lineLen := int64(len(lineBytes))
		bytesRead += lineLen

		if lineLen > 0 {
			line := strings.TrimSpace(string(lineBytes))
			if len(line) > 0 {
				var raw RawStepRecord
				if jsonErr := json.Unmarshal([]byte(line), &raw); jsonErr == nil {
					createdAt := time.Now()
					if raw.CreatedAt != "" {
						if t, parseErr := time.Parse(time.RFC3339Nano, raw.CreatedAt); parseErr == nil {
							createdAt = t
						} else if t, parseErr := time.Parse(time.RFC3339, raw.CreatedAt); parseErr == nil {
							createdAt = t
						}
					}

					// Check for Quota exhaustion (strictly from real SYSTEM error messages, not tool outputs)
					if raw.Source == "SYSTEM" && raw.Type == "SYSTEM_MESSAGE" && strings.Contains(raw.Content, "RESOURCE_EXHAUSTED (code 429)") {
						if match := quotaRegex.FindStringSubmatch(raw.Content); len(match) > 1 {
							resetStr := match[1]
							duration := ParseResetDuration(resetStr)
							expiresAt := createdAt.Add(duration)
							// Only activate if the quota cooldown hasn't expired yet in real time
							if time.Now().Before(expiresAt) {
								result.Cooling = &QuotaCooldown{
									Active:        true,
									ResetDuration: resetStr,
									TriggeredAt:   createdAt,
									ExpiresAt:     expiresAt,
									Message:       fmt.Sprintf("Quota exhausted (code 429). Resets in %s", resetStr),
								}
							}
						}
					}

					// Check for Compaction / Checkpoint boundaries
					if raw.Source == "SYSTEM" && (raw.Type == "CHECKPOINT" || strings.Contains(strings.ToLower(raw.Content), "compaction")) {
						cursor.PendingCompaction = true
						cursor.CompactionNote = "Context Compacted / Checkpoint"
					}

					// Check for Model token event
					totalTokens := raw.InputTokens + raw.CacheReadTokens + raw.OutputTokens
					if raw.Source == "MODEL" && totalTokens > 0 {

						modelName := raw.Model
						if modelName == "" {
							modelName = cursor.DefaultModel
						}
						if modelName == "" {
							modelName = "gemini-3.8-flash"
						}

						totalPrompt := raw.InputTokens + raw.CacheReadTokens
						cacheRatio := 0.0
						if totalPrompt > 0 {
							cacheRatio = (float64(raw.CacheReadTokens) / float64(totalPrompt)) * 100.0
						}

						incCost, rawCost, savings := cfg.CalculateCost(modelName, raw.InputTokens, raw.CacheReadTokens, raw.OutputTokens)

						toolSummary := ""
						if len(raw.ToolCalls) > 0 {
							names := make([]string, 0, len(raw.ToolCalls))
							for _, tc := range raw.ToolCalls {
								names = append(names, tc.Name)
							}
							toolSummary = strings.Join(names, ", ")
						}

						isCompaction := false
						compactionNote := ""
						if cursor.PendingCompaction {
							isCompaction = true
							compactionNote = cursor.CompactionNote
							if compactionNote == "" {
								compactionNote = "Context Compacted"
							}
							cursor.PendingCompaction = false
							cursor.CompactionNote = ""
						} else if cursor.LastCacheRead > 40000 && raw.CacheReadTokens < (cursor.LastCacheRead*60/100) {
							// Significant sudden cache drop (>40%) without explicit checkpoint
							isCompaction = true
							compactionNote = "Cache Invalidation / Re-seed"
						}
						cursor.LastCacheRead = raw.CacheReadTokens

						evt := TokenEvent{
							ID:              fmt.Sprintf("%s-%d", cursor.ConversationID, raw.StepIndex),
							ConversationID:  cursor.ConversationID,
							StepIndex:       raw.StepIndex,
							OriginSource:    cursor.OriginSource,
							Model:           modelName,
							InputTokens:     raw.InputTokens,
							CacheReadTokens: raw.CacheReadTokens,
							OutputTokens:    raw.OutputTokens,
							TotalTokens:     totalTokens,
							CacheRatio:      cacheRatio,
							IncrementalCost: incCost,
							RawCost:         rawCost,
							CacheSavings:    savings,
							CreatedAt:       createdAt,
							ToolsCount:      len(raw.ToolCalls),
							ToolSummary:     toolSummary,
							IsCompaction:    isCompaction,
							CompactionNote:  compactionNote,
						}
						result.Events = append(result.Events, evt)
					}
				}
			}
		}

		if err != nil {
			break
		}
	}

	cursor.Offset += bytesRead
	return result, nil
}
