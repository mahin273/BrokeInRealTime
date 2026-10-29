package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// TieredRate defines pricing above a certain token context window threshold.
type TieredRate struct {
	Threshold              int64   `json:"threshold"`
	HighInputPerMillion    float64 `json:"high_input_per_million"`
	HighCachePerMillion    float64 `json:"high_cache_per_million"`
	HighOutputPerMillion   float64 `json:"high_output_per_million"`
}

// ModelRate defines pricing per 1M tokens.
type ModelRate struct {
	InputPerMillion   float64     `json:"input_per_million"`
	CachePerMillion   float64     `json:"cache_per_million"`
	OutputPerMillion  float64     `json:"output_per_million"`
	ContextTiered     *TieredRate `json:"context_tiered,omitempty"`
}

// Config is the full configuration object.
type Config struct {
	Models map[string]ModelRate `json:"models"`
}

// DefaultConfig returns default fallback pricing.
func DefaultConfig() *Config {
	return &Config{
		Models: map[string]ModelRate{
			"gemini-3.8-flash": {
				InputPerMillion:  0.15,
				CachePerMillion:  0.0375,
				OutputPerMillion: 0.60,
			},
			"argon": {
				InputPerMillion:  1.25,
				CachePerMillion:  0.3125,
				OutputPerMillion: 5.00,
			},
			"gemini-4-argon": {
				InputPerMillion:  1.25,
				CachePerMillion:  0.3125,
				OutputPerMillion: 5.00,
			},
			"gemini-3.8-pro": {
				InputPerMillion:  1.25,
				CachePerMillion:  0.3125,
				OutputPerMillion: 5.00,
				ContextTiered: &TieredRate{
					Threshold:            128000,
					HighInputPerMillion:  2.50,
					HighCachePerMillion:  0.6250,
					HighOutputPerMillion: 10.00,
				},
			},
			"gemini-3.1-pro": {
				InputPerMillion:  1.25,
				CachePerMillion:  0.3125,
				OutputPerMillion: 5.00,
			},
			"gemini-2.5-flash": {
				InputPerMillion:  0.075,
				CachePerMillion:  0.01875,
				OutputPerMillion: 0.30,
			},
			"default": {
				InputPerMillion:  0.15,
				CachePerMillion:  0.0375,
				OutputPerMillion: 0.60,
			},
		},
	}
}

// LoadConfig attempts to load configuration from given path, standard paths, or defaults.
func LoadConfig(customPath string) *Config {
	cfg := DefaultConfig()

	candidatePaths := []string{}
	if customPath != "" {
		candidatePaths = append(candidatePaths, customPath)
	}

	// Local directory pricing.json
	candidatePaths = append(candidatePaths, "pricing.json")

	// User home directory ~/.config/brokeinrealtime/pricing.json
	if home, err := os.UserHomeDir(); err == nil {
		candidatePaths = append(candidatePaths, filepath.Join(home, ".config", "brokeinrealtime", "pricing.json"))
	}

	for _, p := range candidatePaths {
		data, err := os.ReadFile(p)
		if err == nil {
			var loaded Config
			if err := json.Unmarshal(data, &loaded); err == nil {
				if loaded.Models != nil {
					for k, v := range loaded.Models {
						cfg.Models[strings.ToLower(k)] = v
					}
				}
				break
			}
		}
	}

	return cfg
}

// CalculateCost computes total theoretical dollar cost for the given token metrics and model.
func (c *Config) CalculateCost(model string, inputTokens, cacheTokens, outputTokens int64) (totalCost, rawCost, cacheSavings float64) {
	rate, ok := c.Models[strings.ToLower(model)]
	if !ok {
		rate = c.Models["default"]
	}

	totalPrompt := inputTokens + cacheTokens
	inRate := rate.InputPerMillion
	cacheRate := rate.CachePerMillion
	outRate := rate.OutputPerMillion

	if rate.ContextTiered != nil && totalPrompt > rate.ContextTiered.Threshold {
		inRate = rate.ContextTiered.HighInputPerMillion
		cacheRate = rate.ContextTiered.HighCachePerMillion
		outRate = rate.ContextTiered.HighOutputPerMillion
	}

	// Cost if all tokens had been fresh input
	rawCost = (float64(totalPrompt)/1000000.0)*inRate + (float64(outputTokens)/1000000.0)*outRate

	// Actual discounted cost with caching
	actualInputCost := (float64(inputTokens) / 1000000.0) * inRate
	actualCacheCost := (float64(cacheTokens) / 1000000.0) * cacheRate
	actualOutputCost := (float64(outputTokens) / 1000000.0) * outRate

	totalCost = actualInputCost + actualCacheCost + actualOutputCost
	cacheSavings = rawCost - totalCost
	if cacheSavings < 0 {
		cacheSavings = 0
	}
	return totalCost, rawCost, cacheSavings
}

// GetAntigravityDir resolves the base ~/.gemini/antigravity directory.
func GetAntigravityDir(override string) string {
	if override != "" {
		return override
	}
	if env := os.Getenv("ANTIGRAVITY_DATA_DIR"); env != "" {
		return env
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return filepath.Join(home, ".gemini", "antigravity")
}
