// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

package incidents

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKnownPatterns_RequiredFields(t *testing.T) {
	require.NotEmpty(t, KnownPatterns, "KnownPatterns must not be empty")

	for _, p := range KnownPatterns {
		t.Run(p.Name, func(t *testing.T) {
			assert.NotEmpty(t, p.Name, "pattern Name must not be empty")
			assert.NotEmpty(t, p.Category, "pattern Category must not be empty")
			assert.NotEmpty(t, p.Indicators, "pattern must have at least one Indicator")
			assert.NotEmpty(t, p.Recommendations, "pattern must have at least one Recommendation")
			assert.NotEmpty(t, p.RootCause.Category, "RootCause.Category must not be empty")
		})
	}
}

func TestKnownPatterns_IndicatorWeightsSum(t *testing.T) {
	for _, p := range KnownPatterns {
		t.Run(p.Name, func(t *testing.T) {
			var totalWeight float64
			for _, ind := range p.Indicators {
				assert.Positive(t, ind.Weight, "indicator weight must be positive")
				assert.LessOrEqual(t, ind.Weight, 1.0, "indicator weight must be <= 1.0")
				totalWeight += ind.Weight
			}
			assert.InDelta(t, 1.0, totalWeight, 0.01,
				"indicator weights should sum to approximately 1.0")
		})
	}
}

func TestKnownPatterns_IndicatorTypesValid(t *testing.T) {
	validTypes := map[string]bool{
		IndicatorTypeXID:       true,
		IndicatorTypeTemp:      true,
		IndicatorTypeECC:       true,
		IndicatorTypeThrottle:  true,
		IndicatorTypeK8sEvent:  true,
		IndicatorTypeMemUtil:   true,
		IndicatorTypeCausality: true,
	}

	for _, p := range KnownPatterns {
		for _, ind := range p.Indicators {
			t.Run(p.Name+"/"+ind.Type, func(t *testing.T) {
				assert.True(t, validTypes[ind.Type],
					"unknown indicator type %q", ind.Type)
			})
		}
	}
}

func TestKnownPatterns_IndicatorConditionsValid(t *testing.T) {
	validConditions := map[string]bool{
		ConditionEquals:      true,
		ConditionGreaterThan: true,
		ConditionLessThan:    true,
		ConditionIn:          true,
		ConditionContains:    true,
		ConditionPresent:     true,
		ConditionNotPresent:  true,
	}

	for _, p := range KnownPatterns {
		for _, ind := range p.Indicators {
			t.Run(p.Name+"/"+ind.Type+"_condition", func(t *testing.T) {
				assert.True(t, validConditions[ind.Condition],
					"unknown condition %q", ind.Condition)
			})
		}
	}
}

func TestKnownPatterns_XIDCodesValid(t *testing.T) {
	// NVIDIA XID codes are in the range 1-120 (approximately).
	// See: https://docs.nvidia.com/deploy/xid-errors/index.html
	const maxXID = 200

	for _, p := range KnownPatterns {
		for _, ind := range p.Indicators {
			if ind.Type != IndicatorTypeXID {
				continue
			}
			t.Run(p.Name+"/xid_range", func(t *testing.T) {
				switch v := ind.Value.(type) {
				case int:
					assert.Positive(t, v, "XID code must be positive")
					assert.LessOrEqual(t, v, maxXID, "XID code exceeds expected range")
				case []int:
					for _, code := range v {
						assert.Positive(t, code, "XID code must be positive")
						assert.LessOrEqual(t, code, maxXID, "XID code %d exceeds expected range", code)
					}
				}
			})
		}
	}
}

func TestKnownPatterns_SeverityLevels(t *testing.T) {
	validPriorities := map[string]bool{
		PriorityHigh:   true,
		PriorityMedium: true,
		PriorityLow:    true,
	}

	for _, p := range KnownPatterns {
		for i, rec := range p.Recommendations {
			t.Run(p.Name+"/rec_"+rec.Priority, func(t *testing.T) {
				assert.True(t, validPriorities[rec.Priority],
					"recommendation %d has invalid priority %q", i, rec.Priority)
				assert.NotEmpty(t, rec.Action, "recommendation Action must not be empty")
			})
		}
	}
}

func TestKnownPatterns_UniqueNames(t *testing.T) {
	seen := make(map[string]bool)
	for _, p := range KnownPatterns {
		assert.False(t, seen[p.Name], "duplicate pattern name: %s", p.Name)
		seen[p.Name] = true
	}
}

func TestKnownPatterns_CategoryMatchesRootCause(t *testing.T) {
	for _, p := range KnownPatterns {
		t.Run(p.Name, func(t *testing.T) {
			assert.Equal(t, p.Category, p.RootCause.Category,
				"pattern Category should match RootCause.Category")
		})
	}
}
