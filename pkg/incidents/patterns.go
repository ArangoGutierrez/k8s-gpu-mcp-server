// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

package incidents

// FailurePattern defines a known GPU failure signature with weighted indicators.
// When an incident matches multiple indicators, confidence is calculated as
// the sum of matched indicator weights.
type FailurePattern struct {
	// Name is the unique identifier for this pattern.
	Name string

	// Category is the classification (e.g., "thermal_cascade", "ecc_failure").
	Category string

	// Indicators are the conditions to check against incident data.
	Indicators []Indicator

	// RootCause provides the pre-defined root cause for this pattern.
	RootCause PatternRootCause

	// Recommendations are templated actions for this failure type.
	// Templates may use {{.Node}}, {{.PodName}}, {{.Namespace}}, {{.GPUUUID}}.
	Recommendations []Recommendation
}

// PatternRootCause contains root cause metadata for a pattern.
type PatternRootCause struct {
	// Category matches FailurePattern.Category.
	Category string

	// NotYourCode indicates this is a hardware/infra issue, not user code.
	NotYourCode bool
}

// Indicator defines a single condition to check in incident data.
type Indicator struct {
	// Type specifies what to check: "xid", "temp", "ecc", "throttle",
	// "k8s_event", "mem_util", "causality".
	Type string

	// Condition is the comparison operation: "equals", "greater_than",
	// "less_than", "in", "contains", "present", "not_present".
	Condition string

	// Value is the target value for comparison.
	// Type depends on Condition:
	// - "equals", "greater_than", "less_than": int, uint32, uint64, float64
	// - "in": []int for XID codes
	// - "contains", "present", "not_present": string
	Value any

	// Weight is this indicator's contribution to confidence (0.0-1.0).
	// Weights within a pattern should sum to approximately 1.0.
	Weight float64
}

// Indicator types.
const (
	IndicatorTypeXID       = "xid"
	IndicatorTypeTemp      = "temp"
	IndicatorTypeECC       = "ecc"
	IndicatorTypeThrottle  = "throttle"
	IndicatorTypeK8sEvent  = "k8s_event"
	IndicatorTypeMemUtil   = "mem_util"
	IndicatorTypeCausality = "causality"
)

// Indicator conditions.
const (
	ConditionEquals      = "equals"
	ConditionGreaterThan = "greater_than"
	ConditionLessThan    = "less_than"
	ConditionIn          = "in"
	ConditionContains    = "contains"
	ConditionPresent     = "present"
	ConditionNotPresent  = "not_present"
)

// Category constants for failure patterns.
const (
	CategoryThermalCascade = "thermal_cascade"
	CategoryECCFailure     = "ecc_failure"
	CategorySoftwareOOM    = "software_oom"
	CategoryNVLinkFailure  = "nvlink_failure"
	CategoryXID79BusError  = "xid_79_bus_error"
	CategoryUnknown        = "unknown"
)

// Priority levels for recommendations.
const (
	PriorityHigh   = "high"
	PriorityMedium = "medium"
	PriorityLow    = "low"
)

// KnownPatterns is the registry of known GPU failure patterns.
// Patterns are checked in order; the first with highest confidence wins.
var KnownPatterns = []FailurePattern{
	// 1. Thermal Cascade: GPU overheating causes throttling and eventual failure.
	// Signature: temp > threshold, hw_thermal throttle active, often XID 79.
	{
		Name:     "thermal_cascade",
		Category: CategoryThermalCascade,
		Indicators: []Indicator{
			{Type: IndicatorTypeTemp, Condition: ConditionGreaterThan, Value: uint32(82), Weight: 0.30},
			{Type: IndicatorTypeThrottle, Condition: ConditionPresent, Value: "hw_thermal", Weight: 0.30},
			{Type: IndicatorTypeCausality, Condition: ConditionEquals, Value: "thermal_cascade", Weight: 0.25},
			{Type: IndicatorTypeXID, Condition: ConditionIn, Value: []int{79, 43}, Weight: 0.15},
		},
		RootCause: PatternRootCause{
			Category:    CategoryThermalCascade,
			NotYourCode: true,
		},
		Recommendations: []Recommendation{
			{
				Action:   "Check node cooling and airflow",
				Command:  "kubectl describe node {{.Node}} | grep -A5 'Conditions'",
				Priority: PriorityHigh,
			},
			{
				Action:   "Drain node for cooling investigation",
				Command:  "kubectl drain {{.Node}} --ignore-daemonsets --delete-emptydir-data",
				Priority: PriorityHigh,
			},
			{
				Action:   "Check GPU temperature and throttling",
				Command:  "nvidia-smi -q -d TEMPERATURE,PERFORMANCE",
				Priority: PriorityMedium,
			},
		},
	},

	// 2. ECC Failure: Memory corruption from uncorrectable ECC errors.
	// Signature: uncorrectable ECC > 0, XID in [48, 63, 64, 68, 69].
	{
		Name:     "ecc_failure",
		Category: CategoryECCFailure,
		Indicators: []Indicator{
			{Type: IndicatorTypeECC, Condition: ConditionGreaterThan, Value: uint64(0), Weight: 0.40},
			{Type: IndicatorTypeXID, Condition: ConditionIn, Value: []int{48, 63, 64, 68, 69, 8, 92}, Weight: 0.35},
			{Type: IndicatorTypeCausality, Condition: ConditionEquals, Value: "memory_failure", Weight: 0.25},
		},
		RootCause: PatternRootCause{
			Category:    CategoryECCFailure,
			NotYourCode: true,
		},
		Recommendations: []Recommendation{
			{
				Action:   "DRAIN NODE IMMEDIATELY - Memory corruption detected",
				Command:  "kubectl drain {{.Node}} --ignore-daemonsets --delete-emptydir-data",
				Priority: PriorityHigh,
			},
			{
				Action:   "Check ECC error counts",
				Command:  "nvidia-smi -q -d ECC",
				Priority: PriorityHigh,
			},
			{
				Action:   "Schedule GPU replacement",
				Command:  "kubectl label node {{.Node}} gpu-health=degraded",
				Priority: PriorityMedium,
			},
		},
	},

	// 3. Software OOM: GPU memory exhaustion from user application.
	// Signature: mem_util > 95%, OOMKilled event, no hardware XID.
	{
		Name:     "software_oom",
		Category: CategorySoftwareOOM,
		Indicators: []Indicator{
			{Type: IndicatorTypeMemUtil, Condition: ConditionGreaterThan, Value: float64(95.0), Weight: 0.30},
			{Type: IndicatorTypeK8sEvent, Condition: ConditionContains, Value: "OOMKilled", Weight: 0.35},
			{Type: IndicatorTypeCausality, Condition: ConditionEquals, Value: "software_oom", Weight: 0.25},
			{Type: IndicatorTypeXID, Condition: ConditionNotPresent, Value: nil, Weight: 0.10},
		},
		RootCause: PatternRootCause{
			Category:    CategorySoftwareOOM,
			NotYourCode: false, // User code issue
		},
		Recommendations: []Recommendation{
			{
				Action:   "Review pod memory limits and GPU memory usage",
				Command:  "kubectl describe pod {{.PodName}} -n {{.Namespace}}",
				Priority: PriorityHigh,
			},
			{
				Action:   "Check application memory allocation patterns",
				Command:  "kubectl logs {{.PodName}} -n {{.Namespace}} --previous | tail -100",
				Priority: PriorityMedium,
			},
			{
				Action:   "Consider increasing GPU memory request or optimizing model",
				Priority: PriorityMedium,
			},
		},
	},

	// 4. NVLink Failure: Inter-GPU communication failure.
	// Signature: XID 74, NVLink-related errors.
	{
		Name:     "nvlink_failure",
		Category: CategoryNVLinkFailure,
		Indicators: []Indicator{
			{Type: IndicatorTypeXID, Condition: ConditionEquals, Value: 74, Weight: 0.50},
			{Type: IndicatorTypeK8sEvent, Condition: ConditionContains, Value: "NVLink", Weight: 0.30},
			{Type: IndicatorTypeK8sEvent, Condition: ConditionContains, Value: "interconnect", Weight: 0.20},
		},
		RootCause: PatternRootCause{
			Category:    CategoryNVLinkFailure,
			NotYourCode: true,
		},
		Recommendations: []Recommendation{
			{
				Action:   "Check NVLink topology and status",
				Command:  "nvidia-smi nvlink -s",
				Priority: PriorityHigh,
			},
			{
				Action:   "Inspect NVLink error counters",
				Command:  "nvidia-smi nvlink -e",
				Priority: PriorityHigh,
			},
			{
				Action:   "Drain node if multi-GPU workload affected",
				Command:  "kubectl drain {{.Node}} --ignore-daemonsets --delete-emptydir-data",
				Priority: PriorityMedium,
			},
			{
				Action:   "Check physical NVLink cable connections",
				Priority: PriorityMedium,
			},
		},
	},

	// 5. XID 79 Bus Error: GPU fell off PCIe bus - critical hardware failure.
	// Signature: XID 79 specifically.
	{
		Name:     "xid_79_bus_error",
		Category: CategoryXID79BusError,
		Indicators: []Indicator{
			{Type: IndicatorTypeXID, Condition: ConditionEquals, Value: 79, Weight: 0.70},
			{Type: IndicatorTypeCausality, Condition: ConditionEquals, Value: "thermal_cascade", Weight: 0.15},
			{Type: IndicatorTypeThrottle, Condition: ConditionPresent, Value: "", Weight: 0.15},
		},
		RootCause: PatternRootCause{
			Category:    CategoryXID79BusError,
			NotYourCode: true,
		},
		Recommendations: []Recommendation{
			{
				Action:   "DRAIN NODE IMMEDIATELY - GPU hardware failure",
				Command:  "kubectl drain {{.Node}} --ignore-daemonsets --delete-emptydir-data",
				Priority: PriorityHigh,
			},
			{
				Action:   "Check PCIe bus status",
				Command:  "lspci -vvv | grep -A20 'NVIDIA'",
				Priority: PriorityHigh,
			},
			{
				Action:   "Check dmesg for PCIe errors",
				Command:  "dmesg | grep -i 'pci\\|nvidia' | tail -50",
				Priority: PriorityHigh,
			},
			{
				Action:   "Schedule GPU replacement - hardware failure confirmed",
				Priority: PriorityHigh,
			},
		},
	},
}
