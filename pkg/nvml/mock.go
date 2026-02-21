// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

package nvml

import (
	"context"
	"fmt"
	"sync/atomic"
)

// Mock is a mock implementation of the NVML Interface for testing.
// It returns fake but consistent GPU data without requiring real hardware.
type Mock struct {
	UnimplementedInterface // Embedded for forward compatibility
	deviceCount            int
	devices                []*MockDevice
	capabilities           *Capabilities
}

// Compile-time interface satisfaction checks.
var (
	_ Interface = (*Mock)(nil)
	_ Device    = (*MockDevice)(nil)
)

// NewMock creates a new mock NVML implementation with the specified
// number of fake GPU devices.
func NewMock(deviceCount int) *Mock {
	if deviceCount <= 0 {
		deviceCount = 2 // Default to 2 fake GPUs
	}

	// Build default full capabilities (Tier3Advanced)
	allAPIs := make([]string, 0, len(Tier1APIs)+len(Tier2APIs)+len(Tier3APIs))
	allAPIs = append(allAPIs, Tier1APIs...)
	allAPIs = append(allAPIs, Tier2APIs...)
	allAPIs = append(allAPIs, Tier3APIs...)

	m := &Mock{
		deviceCount: deviceCount,
		devices:     make([]*MockDevice, deviceCount),
		capabilities: &Capabilities{
			Tier:          Tier3Advanced,
			DriverVersion: "575.57.08",
			CudaVersion:   "12.9",
			SupportedAPIs: allAPIs,
		},
	}

	// Create fake devices
	for i := 0; i < deviceCount; i++ {
		m.devices[i] = &MockDevice{
			index:       i,
			name:        fmt.Sprintf("NVIDIA A100-SXM4-40GB (Mock %d)", i),
			uuid:        fmt.Sprintf("GPU-%08d-0000-0000-0000-%012d", i, i),
			busID:       fmt.Sprintf("0000:%02x:00.0", i+1),
			domain:      0,
			bus:         uint32(i + 1),
			device:      0,
			memoryTotal: 42949672960, // 40 GB
			memoryUsed:  8589934592,  // 8 GB
			temperature: 45 + uint32(i*5),
			powerUsage:  150000 + uint32(i*10000), // milliwatts
			gpuUtil:     30 + uint32(i*10),
			memoryUtil:  20 + uint32(i*5),

			// Extended health monitoring defaults (A100 profile)
			powerLimit:       400000, // 400W TDP for A100
			eccEnabled:       true,
			eccCorrectable:   0,
			eccUncorrectable: 0,
			throttleReasons:  0, // No throttling
			smClock:          1410,
			memClock:         1215,
			tempShutdown:     90,
			tempSlowdown:     82,
		}
	}

	return m
}

// Init initializes the mock NVML library (no-op).
func (m *Mock) Init(ctx context.Context) error {
	return nil
}

// Shutdown shuts down the mock NVML library (no-op).
func (m *Mock) Shutdown(ctx context.Context) error {
	return nil
}

// GetDeviceCount returns the number of mock GPU devices.
func (m *Mock) GetDeviceCount(ctx context.Context) (int, error) {
	return m.deviceCount, nil
}

// GetDeviceByIndex returns a mock Device handle for the given index.
func (m *Mock) GetDeviceByIndex(ctx context.Context, idx int) (Device, error) {
	if idx < 0 || idx >= m.deviceCount {
		return nil, fmt.Errorf("%w: %d (count: %d)",
			ErrInvalidDevice, idx, m.deviceCount)
	}
	return m.devices[idx], nil
}

// GetDriverVersion returns the mock NVIDIA driver version.
func (m *Mock) GetDriverVersion(ctx context.Context) (string, error) {
	return "575.57.08", nil
}

// GetCudaDriverVersion returns the mock CUDA driver version.
func (m *Mock) GetCudaDriverVersion(ctx context.Context) (string, error) {
	return "12.9", nil
}

// GetCapabilities returns the configured capabilities for the mock.
func (m *Mock) GetCapabilities(ctx context.Context) (*Capabilities, error) {
	return m.capabilities, nil
}

// SetCapabilities allows tests to configure capability reporting.
func (m *Mock) SetCapabilities(caps *Capabilities) {
	m.capabilities = caps
}

// GetMockDevice returns the MockDevice at the specified index for test
// configuration. Returns nil if index is out of range.
func (m *Mock) GetMockDevice(idx int) *MockDevice {
	if idx < 0 || idx >= len(m.devices) {
		return nil
	}
	return m.devices[idx]
}

// NVLinkTopology configures mock NVLink connections for testing.
// Key: link index, Value: remote device index (-1 if not connected).
type NVLinkTopology map[int]int

// MockDevice is a mock implementation of the Device interface.
type MockDevice struct {
	UnimplementedDevice // Embedded for forward compatibility
	index               int
	name                string
	uuid                string
	busID               string
	domain              uint32
	bus                 uint32
	device              uint32
	memoryTotal         uint64
	memoryUsed          uint64
	temperature         uint32
	powerUsage          uint32
	gpuUtil             uint32
	memoryUtil          uint32

	// Extended health monitoring fields
	powerLimit       uint32
	eccEnabled       bool
	eccCorrectable   uint64
	eccUncorrectable uint64
	throttleReasons  uint64
	smClock          uint32
	memClock         uint32
	tempShutdown     uint32
	tempSlowdown     uint32

	// NVLink configuration for testing
	nvlinkTopology NVLinkTopology // link -> remote GPU index
	nvlinkErrors   map[int]uint64 // link -> error count

	// Process tracking for testing
	runningProcesses []ProcessInfoNVML

	// Scenario tracking for dynamic mock behavior
	throttleAfterN    int
	throttleReasonVal uint64
	tempStart         int
	tempDelta         int
	errorAfterN       int
	scenarioError     error
	callCount         atomic.Int64
}

// GetName returns the mock device name.
func (d *MockDevice) GetName(ctx context.Context) (string, error) {
	return d.name, nil
}

// GetUUID returns the mock device UUID.
func (d *MockDevice) GetUUID(ctx context.Context) (string, error) {
	return d.uuid, nil
}

// GetPCIInfo returns mock PCI information.
func (d *MockDevice) GetPCIInfo(ctx context.Context) (*PCIInfo, error) {
	return &PCIInfo{
		BusID:  d.busID,
		Domain: d.domain,
		Bus:    d.bus,
		Device: d.device,
	}, nil
}

// GetMemoryInfo returns mock memory usage information.
func (d *MockDevice) GetMemoryInfo(ctx context.Context) (*MemoryInfo, error) {
	return &MemoryInfo{
		Total: d.memoryTotal,
		Used:  d.memoryUsed,
		Free:  d.memoryTotal - d.memoryUsed,
	}, nil
}

// GetTemperature returns the mock temperature.
// If SetTemperatureTrend was called, temperature increases by delta on each call.
// If SetErrorAfterN was called, returns the configured error after N calls.
func (d *MockDevice) GetTemperature(ctx context.Context) (uint32, error) {
	n := d.callCount.Add(1)
	if d.errorAfterN > 0 && int(n) > d.errorAfterN && d.scenarioError != nil {
		return 0, d.scenarioError
	}
	if d.tempDelta != 0 {
		// callCount is 1-based, so first call returns tempStart
		temp := d.tempStart + d.tempDelta*(int(n)-1)
		if temp < 0 {
			temp = 0
		}
		return uint32(temp), nil
	}
	return d.temperature, nil
}

// GetPowerUsage returns the mock power usage.
func (d *MockDevice) GetPowerUsage(ctx context.Context) (uint32, error) {
	return d.powerUsage, nil
}

// GetUtilizationRates returns mock utilization rates.
func (d *MockDevice) GetUtilizationRates(
	ctx context.Context,
) (*Utilization, error) {
	return &Utilization{
		GPU:    d.gpuUtil,
		Memory: d.memoryUtil,
	}, nil
}

// GetPowerManagementLimit returns the mock power management limit.
func (d *MockDevice) GetPowerManagementLimit(
	ctx context.Context,
) (uint32, error) {
	return d.powerLimit, nil
}

// GetEccMode returns mock ECC mode status.
func (d *MockDevice) GetEccMode(
	ctx context.Context,
) (current, pending bool, err error) {
	return d.eccEnabled, d.eccEnabled, nil
}

// GetTotalEccErrors returns mock ECC error counts.
func (d *MockDevice) GetTotalEccErrors(
	ctx context.Context,
	errorType int,
) (uint64, error) {
	if errorType == EccErrorCorrectable {
		return d.eccCorrectable, nil
	}
	return d.eccUncorrectable, nil
}

// GetCurrentClocksThrottleReasons returns mock throttle reason bitmask.
// If SetThrottleAfterNIterations was called, throttling activates after N calls.
func (d *MockDevice) GetCurrentClocksThrottleReasons(
	ctx context.Context,
) (uint64, error) {
	if d.throttleAfterN > 0 {
		// Use a separate counter: check callCount for throttle queries
		n := d.callCount.Load()
		if int(n) >= d.throttleAfterN {
			return d.throttleReasonVal, nil
		}
		return 0, nil
	}
	return d.throttleReasons, nil
}

// GetClockInfo returns mock clock frequency for the given type.
func (d *MockDevice) GetClockInfo(
	ctx context.Context,
	clockType int,
) (uint32, error) {
	if clockType == ClockGraphics {
		return d.smClock, nil
	}
	return d.memClock, nil
}

// GetTemperatureThreshold returns mock temperature threshold.
func (d *MockDevice) GetTemperatureThreshold(
	ctx context.Context,
	thresholdType int,
) (uint32, error) {
	if thresholdType == TempThresholdShutdown {
		return d.tempShutdown, nil
	}
	return d.tempSlowdown, nil
}

// GetCudaComputeCapability returns the mock CUDA compute capability.
// Returns "8.0" for mock A100 GPUs.
func (d *MockDevice) GetCudaComputeCapability(
	ctx context.Context,
) (string, error) {
	return "8.0", nil // A100 compute capability
}

// GetNvLinkState returns mock NVLink state for the specified link.
func (d *MockDevice) GetNvLinkState(
	ctx context.Context,
	link int,
) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if d.nvlinkTopology == nil {
		return false, nil // No NVLink support
	}
	_, connected := d.nvlinkTopology[link]
	return connected, nil
}

// GetNvLinkRemotePciInfo returns mock PCI info for the remote device.
func (d *MockDevice) GetNvLinkRemotePciInfo(
	ctx context.Context,
	link int,
) (*PCIInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if d.nvlinkTopology == nil {
		return nil, nil
	}
	remoteIdx, ok := d.nvlinkTopology[link]
	if !ok || remoteIdx < 0 {
		return nil, nil
	}
	return &PCIInfo{
		BusID:  fmt.Sprintf("0000:%02x:00.0", remoteIdx+1),
		Domain: 0,
		Bus:    uint32(remoteIdx + 1),
		Device: 0,
	}, nil
}

// GetNvLinkErrorCounter returns mock error count for the specified link.
func (d *MockDevice) GetNvLinkErrorCounter(
	ctx context.Context,
	link int,
	counterType int,
) (uint64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if d.nvlinkErrors == nil {
		return 0, nil
	}
	return d.nvlinkErrors[link], nil
}

// SetNVLinkTopology configures mock NVLink connections for testing.
func (d *MockDevice) SetNVLinkTopology(topology NVLinkTopology) {
	d.nvlinkTopology = topology
}

// SetNVLinkErrors configures mock NVLink error counts for testing.
func (d *MockDevice) SetNVLinkErrors(errors map[int]uint64) {
	d.nvlinkErrors = errors
}

// GetComputeRunningProcesses returns mock process list.
func (d *MockDevice) GetComputeRunningProcesses(
	ctx context.Context,
) ([]ProcessInfoNVML, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if d.runningProcesses == nil {
		return []ProcessInfoNVML{}, nil
	}
	// Return a copy to prevent mutation
	result := make([]ProcessInfoNVML, len(d.runningProcesses))
	copy(result, d.runningProcesses)
	return result, nil
}

// SetRunningProcesses configures mock running processes for testing.
func (d *MockDevice) SetRunningProcesses(procs []ProcessInfoNVML) {
	d.runningProcesses = procs
}

// SetThrottleAfterNIterations configures the mock to return no throttling
// for the first n calls to GetTemperature, then return the given throttle
// reasons bitmask on subsequent calls to GetCurrentClocksThrottleReasons.
// This simulates throttling that kicks in after sustained load.
func (d *MockDevice) SetThrottleAfterNIterations(n int, reasons uint64) {
	d.throttleAfterN = n
	d.throttleReasonVal = reasons
}

// SetTemperatureTrend configures the mock to return a linearly increasing
// temperature starting at start and increasing by delta on each call to
// GetTemperature. This simulates a GPU heating up over time.
// For example, SetTemperatureTrend(40, 5) returns 40, 45, 50, 55, ...
func (d *MockDevice) SetTemperatureTrend(start, delta int) {
	d.tempStart = start
	d.tempDelta = delta
	d.callCount.Store(0)
}

// SetErrorAfterN configures the mock to return err after n calls to
// GetTemperature. The first n calls behave normally; subsequent calls
// return the configured error. This simulates intermittent device failures.
func (d *MockDevice) SetErrorAfterN(n int, err error) {
	d.errorAfterN = n
	d.scenarioError = err
	d.callCount.Store(0)
}

// ResetScenario clears all scenario-based mock state, restoring default
// static behavior. Call this between test cases if reusing a MockDevice.
func (d *MockDevice) ResetScenario() {
	d.throttleAfterN = 0
	d.throttleReasonVal = 0
	d.tempStart = 0
	d.tempDelta = 0
	d.errorAfterN = 0
	d.scenarioError = nil
	d.callCount.Store(0)
}
