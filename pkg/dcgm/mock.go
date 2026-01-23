// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

package dcgm

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Mock is a mock implementation of Interface for testing.
// It allows configuring fake data and simulating various scenarios.
type Mock struct {
	mu sync.RWMutex

	// State
	initialized bool
	available   bool
	gpuCount    int

	// Configurable data
	profilingData    map[int]*ProfilingMetrics
	nvswitchStatus   *NVSwitchStatus
	healthViolations []HealthViolation
	healthPolicies   []HealthPolicy
	xidErrors        map[int][]XIDError
	watchedFields    map[int][]FieldID
	fieldValues      map[int]map[FieldID]Value
}

// Compile-time interface check.
var _ Interface = (*Mock)(nil)

// NewMock creates a new mock DCGM implementation with the specified GPU count.
func NewMock(gpuCount int) *Mock {
	if gpuCount <= 0 {
		gpuCount = 2
	}

	m := &Mock{
		available:        true,
		gpuCount:         gpuCount,
		profilingData:    make(map[int]*ProfilingMetrics),
		xidErrors:        make(map[int][]XIDError),
		watchedFields:    make(map[int][]FieldID),
		fieldValues:      make(map[int]map[FieldID]Value),
		healthViolations: make([]HealthViolation, 0),
		healthPolicies:   make([]HealthPolicy, 0),
	}

	// Initialize default data for each GPU
	now := time.Now()
	for i := 0; i < gpuCount; i++ {
		m.profilingData[i] = &ProfilingMetrics{
			GPUID:          i,
			Timestamp:      now,
			SMActive:       float64(60 + i*5),
			SMOccupancy:    float64(50 + i*5),
			TensorActivity: float64(30 + i*3),
			DRAMActivity:   float64(40 + i*4),
			FP64Activity:   float64(10 + i*2),
			FP32Activity:   float64(45 + i*5),
			FP16Activity:   float64(20 + i*3),
			PCIeTxBytes:    uint64(10000000 + i*1000000),
			PCIeRxBytes:    uint64(8000000 + i*800000),
			NVLinkTxBytes:  uint64(50000000 + i*5000000),
			NVLinkRxBytes:  uint64(45000000 + i*4500000),
		}

		m.fieldValues[i] = map[FieldID]Value{
			FieldGPUTemp: {
				FieldID:   FieldGPUTemp,
				Timestamp: now,
				Status:    ValueStatusOK,
				Int64:     int64(45 + i*5),
			},
			FieldPowerUsage: {
				FieldID:   FieldPowerUsage,
				Timestamp: now,
				Status:    ValueStatusOK,
				Float64:   150.0 + float64(i*10),
			},
			FieldGPUUtil: {
				FieldID:   FieldGPUUtil,
				Timestamp: now,
				Status:    ValueStatusOK,
				Int64:     int64(70 + i*5),
			},
			FieldMemUtil: {
				FieldID:   FieldMemUtil,
				Timestamp: now,
				Status:    ValueStatusOK,
				Int64:     int64(60 + i*5),
			},
			FieldSMOccupancy: {
				FieldID:   FieldSMOccupancy,
				Timestamp: now,
				Status:    ValueStatusOK,
				Float64:   50.0 + float64(i*5),
			},
			FieldTensorActive: {
				FieldID:   FieldTensorActive,
				Timestamp: now,
				Status:    ValueStatusOK,
				Float64:   30.0 + float64(i*3),
			},
			FieldXIDErrors: {
				FieldID:   FieldXIDErrors,
				Timestamp: now,
				Status:    ValueStatusOK,
				Int64:     0, // No XID errors by default
			},
		}
	}

	return m
}

// Init initializes the mock.
func (m *Mock) Init(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.initialized {
		return ErrAlreadyInitialized
	}

	if !m.available {
		return ErrDCGMUnavailable
	}

	m.initialized = true
	return nil
}

// Shutdown shuts down the mock.
func (m *Mock) Shutdown(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.initialized = false
	return nil
}

// Reconnect is a no-op for mock (always succeeds if initialized).
func (m *Mock) Reconnect(ctx context.Context) error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if !m.initialized {
		return ErrNotInitialized
	}
	return nil
}

// IsAvailable returns whether DCGM is available and initialized.
func (m *Mock) IsAvailable() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.available && m.initialized
}

// WatchFields subscribes to field updates for a GPU.
func (m *Mock) WatchFields(gpuID int, fields []FieldID, interval time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.initialized {
		return ErrNotInitialized
	}

	if gpuID < 0 || gpuID >= m.gpuCount {
		return fmt.Errorf("%w: %d", ErrInvalidGPU, gpuID)
	}

	if interval < 100*time.Millisecond {
		return ErrIntervalTooShort
	}

	m.watchedFields[gpuID] = append([]FieldID{}, fields...)
	return nil
}

// GetLatestValues returns the latest field values for a GPU.
func (m *Mock) GetLatestValues(gpuID int, fields []FieldID) (map[FieldID]Value, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if !m.initialized {
		return nil, ErrNotInitialized
	}

	if gpuID < 0 || gpuID >= m.gpuCount {
		return nil, fmt.Errorf("%w: %d", ErrInvalidGPU, gpuID)
	}

	result := make(map[FieldID]Value)
	gpuValues := m.fieldValues[gpuID]

	now := time.Now()
	for _, field := range fields {
		if v, ok := gpuValues[field]; ok {
			// Return copy with updated timestamp
			v.Timestamp = now
			result[field] = v
		} else {
			result[field] = Value{
				FieldID:   field,
				Timestamp: now,
				Status:    ValueStatusNotSupported,
			}
		}
	}

	return result, nil
}

// GetProfilingMetrics returns profiling metrics for a GPU.
func (m *Mock) GetProfilingMetrics(gpuID int) (*ProfilingMetrics, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if !m.initialized {
		return nil, ErrNotInitialized
	}

	if gpuID < 0 || gpuID >= m.gpuCount {
		return nil, fmt.Errorf("%w: %d", ErrInvalidGPU, gpuID)
	}

	metrics := m.profilingData[gpuID]
	if metrics == nil {
		return nil, ErrProfilingUnavailable
	}

	// Return copy with updated timestamp
	result := *metrics
	result.Timestamp = time.Now()
	return &result, nil
}

// GetNVSwitchStatus returns NVSwitch status.
func (m *Mock) GetNVSwitchStatus() (*NVSwitchStatus, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if !m.initialized {
		return nil, ErrNotInitialized
	}

	if m.nvswitchStatus == nil {
		return &NVSwitchStatus{Available: false}, nil
	}

	return m.nvswitchStatus, nil
}

// SetHealthPolicy configures a health policy.
func (m *Mock) SetHealthPolicy(policy HealthPolicy) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.initialized {
		return ErrNotInitialized
	}

	if err := policy.Validate(); err != nil {
		return err
	}

	// Replace existing policy with same name, or add new
	found := false
	for i, p := range m.healthPolicies {
		if p.Name == policy.Name {
			m.healthPolicies[i] = policy
			found = true
			break
		}
	}
	if !found {
		m.healthPolicies = append(m.healthPolicies, policy)
	}

	return nil
}

// GetHealthViolations returns current health violations.
func (m *Mock) GetHealthViolations() ([]HealthViolation, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if !m.initialized {
		return nil, ErrNotInitialized
	}

	// Return a copy
	result := make([]HealthViolation, len(m.healthViolations))
	copy(result, m.healthViolations)
	return result, nil
}

// GetXIDErrors returns XID errors from the mock.
func (m *Mock) GetXIDErrors(gpuID int, since time.Time) ([]XIDError, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if !m.initialized {
		return nil, ErrNotInitialized
	}

	if gpuID < 0 || gpuID >= m.gpuCount {
		return nil, fmt.Errorf("%w: %d", ErrInvalidGPU, gpuID)
	}

	errors := m.xidErrors[gpuID]
	result := make([]XIDError, 0)

	for _, e := range errors {
		if e.Timestamp.After(since) || e.Timestamp.Equal(since) {
			result = append(result, e)
		}
	}

	return result, nil
}

// --- Test configuration methods ---

// SetAvailable configures whether DCGM is available.
// Use this to simulate DCGM being unavailable.
func (m *Mock) SetAvailable(available bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.available = available
}

// SetProfilingData sets profiling data for a GPU.
func (m *Mock) SetProfilingData(gpuID int, data *ProfilingMetrics) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.profilingData[gpuID] = data
}

// SetNVSwitchStatus sets the NVSwitch status.
func (m *Mock) SetNVSwitchStatus(status *NVSwitchStatus) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nvswitchStatus = status
}

// AddHealthViolation adds a health violation.
func (m *Mock) AddHealthViolation(v HealthViolation) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.healthViolations = append(m.healthViolations, v)
}

// ClearHealthViolations removes all health violations.
func (m *Mock) ClearHealthViolations() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.healthViolations = make([]HealthViolation, 0)
}

// AddXIDError adds an XID error for a GPU.
func (m *Mock) AddXIDError(gpuID int, err XIDError) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.xidErrors[gpuID] = append(m.xidErrors[gpuID], err)
}

// ClearXIDErrors removes all XID errors for a GPU.
func (m *Mock) ClearXIDErrors(gpuID int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.xidErrors[gpuID] = make([]XIDError, 0)
}

// SetFieldValue sets a field value for testing.
func (m *Mock) SetFieldValue(gpuID int, field FieldID, value Value) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fieldValues[gpuID] == nil {
		m.fieldValues[gpuID] = make(map[FieldID]Value)
	}
	m.fieldValues[gpuID][field] = value
}

// GPUCount returns the number of mock GPUs.
func (m *Mock) GPUCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.gpuCount
}

// WatchedFields returns the fields being watched for a GPU.
func (m *Mock) WatchedFields(gpuID int) []FieldID {
	m.mu.RLock()
	defer m.mu.RUnlock()
	fields := m.watchedFields[gpuID]
	result := make([]FieldID, len(fields))
	copy(result, fields)
	return result
}
