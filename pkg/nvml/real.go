// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

//go:build cgo
// +build cgo

package nvml

import (
	"context"
	"fmt"
	"sync"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

// Real is a real implementation of the NVML Interface using go-nvml.
// This requires the NVIDIA driver and libnvidia-ml.so to be available.
// Real is safe for concurrent use.
type Real struct {
	UnimplementedInterface // Embedded for forward compatibility
	mu                     sync.Mutex
	initialized            bool
	capabilities           *Capabilities
}

// Compile-time interface satisfaction checks.
var (
	_ Interface = (*Real)(nil)
	_ Device    = (*RealDevice)(nil)
)

// NewReal creates a new real NVML implementation.
func NewReal() *Real {
	return &Real{
		initialized: false,
	}
}

// Init initializes the NVML library.
// Init is safe for concurrent calls; subsequent calls after successful
// initialization are no-ops.
func (r *Real) Init(ctx context.Context) error {
	// Check context before expensive operation
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: %w", ErrContextCancelled, err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.initialized {
		return nil
	}

	ret := nvml.Init()
	if ret != nvml.SUCCESS {
		return fmt.Errorf("failed to initialize NVML: %s", nvml.ErrorString(ret))
	}

	r.initialized = true

	// Probe capabilities after successful init
	r.capabilities = r.probeCapabilities(ctx)
	return nil
}

// Shutdown shuts down the NVML library.
// Shutdown is safe for concurrent calls; calls on an uninitialized
// instance are no-ops.
func (r *Real) Shutdown(ctx context.Context) error {
	// Check context before shutdown
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: %w", ErrContextCancelled, err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.initialized {
		return nil
	}

	ret := nvml.Shutdown()
	if ret != nvml.SUCCESS {
		return fmt.Errorf("failed to shutdown NVML: %s", nvml.ErrorString(ret))
	}

	r.initialized = false
	return nil
}

// GetDeviceCount returns the number of GPU devices.
func (r *Real) GetDeviceCount(ctx context.Context) (int, error) {
	// Check context cancellation
	if err := ctx.Err(); err != nil {
		return 0, fmt.Errorf("%w: %w", ErrContextCancelled, err)
	}

	if !r.initialized {
		return 0, ErrNotInitialized
	}

	count, ret := nvml.DeviceGetCount()
	if ret != nvml.SUCCESS {
		return 0, fmt.Errorf("failed to get device count: %s",
			nvml.ErrorString(ret))
	}

	return count, nil
}

// GetDeviceByIndex returns a Device handle for the given index.
func (r *Real) GetDeviceByIndex(ctx context.Context, idx int) (Device, error) {
	// Check context cancellation
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrContextCancelled, err)
	}

	if !r.initialized {
		return nil, ErrNotInitialized
	}

	device, ret := nvml.DeviceGetHandleByIndex(idx)
	if ret != nvml.SUCCESS {
		return nil, fmt.Errorf("failed to get device %d: %s", idx,
			nvml.ErrorString(ret))
	}

	return &RealDevice{device: device}, nil
}

// GetDriverVersion returns the NVIDIA driver version string.
func (r *Real) GetDriverVersion(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("%w: %w", ErrContextCancelled, err)
	}

	if !r.initialized {
		return "", ErrNotInitialized
	}

	version, ret := nvml.SystemGetDriverVersion()
	if ret != nvml.SUCCESS {
		return "", fmt.Errorf("failed to get driver version: %s",
			nvml.ErrorString(ret))
	}
	return version, nil
}

// GetCudaDriverVersion returns the CUDA driver version as a string.
func (r *Real) GetCudaDriverVersion(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("%w: %w", ErrContextCancelled, err)
	}

	if !r.initialized {
		return "", ErrNotInitialized
	}

	version, ret := nvml.SystemGetCudaDriverVersion()
	if ret != nvml.SUCCESS {
		return "", fmt.Errorf("failed to get CUDA driver version: %s",
			nvml.ErrorString(ret))
	}
	// Convert from major*1000 + minor*10 format to "major.minor" string
	major := version / 1000
	minor := (version % 1000) / 10
	return fmt.Sprintf("%d.%d", major, minor), nil
}

// GetCapabilities returns the detected NVML capability tier.
func (r *Real) GetCapabilities(ctx context.Context) (*Capabilities, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrContextCancelled, err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.initialized {
		return nil, ErrNotInitialized
	}
	return r.capabilities, nil
}

// probeCapabilities detects available NVML capabilities by testing APIs.
func (r *Real) probeCapabilities(ctx context.Context) *Capabilities {
	// Check context cancellation at start
	if ctx.Err() != nil {
		return buildCapabilities(nil, "", "")
	}

	supported := make(map[string]bool)

	// Get driver version
	var driverVersion, cudaVersion string
	if ver, ret := nvml.SystemGetDriverVersion(); ret == nvml.SUCCESS {
		driverVersion = ver
	}
	// Get CUDA version
	if ver, ret := nvml.SystemGetCudaDriverVersion(); ret == nvml.SUCCESS {
		major := ver / 1000
		minor := (ver % 1000) / 10
		cudaVersion = fmt.Sprintf("%d.%d", major, minor)
	}

	// Need at least one device to probe capabilities
	count, ret := nvml.DeviceGetCount()
	if ret != nvml.SUCCESS || count == 0 {
		return buildCapabilities(supported, driverVersion, cudaVersion)
	}

	device, ret := nvml.DeviceGetHandleByIndex(0)
	if ret != nvml.SUCCESS {
		return buildCapabilities(supported, driverVersion, cudaVersion)
	}

	// Probe Tier 1 APIs (basic)
	if _, ret := device.GetName(); ret == nvml.SUCCESS {
		supported[APIName] = true
	}
	if _, ret := device.GetUUID(); ret == nvml.SUCCESS {
		supported[APIUUID] = true
	}
	if _, ret := device.GetPciInfo(); ret == nvml.SUCCESS {
		supported[APIPCIInfo] = true
	}
	if _, ret := device.GetMemoryInfo(); ret == nvml.SUCCESS {
		supported[APIMemoryInfo] = true
	}
	if _, ret := device.GetTemperature(nvml.TEMPERATURE_GPU); ret == nvml.SUCCESS {
		supported[APITemperature] = true
	}
	if _, ret := device.GetPowerUsage(); ret == nvml.SUCCESS {
		supported[APIPowerUsage] = true
	}
	if _, ret := device.GetUtilizationRates(); ret == nvml.SUCCESS {
		supported[APIUtilization] = true
	}

	// Check context cancellation between tier groups
	if ctx.Err() != nil {
		return buildCapabilities(supported, driverVersion, cudaVersion)
	}

	// Probe Tier 2 APIs (health monitoring)
	if _, ret := device.GetPowerManagementLimit(); ret == nvml.SUCCESS ||
		ret == nvml.ERROR_NOT_SUPPORTED {
		supported[APIPowerLimit] = true
	}
	// ECC mode may return ERROR_NOT_SUPPORTED on consumer GPUs
	if _, _, ret := device.GetEccMode(); ret == nvml.SUCCESS ||
		ret == nvml.ERROR_NOT_SUPPORTED {
		supported[APIEccMode] = true
	}
	if _, ret := device.GetTotalEccErrors(
		nvml.MEMORY_ERROR_TYPE_CORRECTED, nvml.AGGREGATE_ECC,
	); ret == nvml.SUCCESS || ret == nvml.ERROR_NOT_SUPPORTED {
		supported[APIEccErrors] = true
	}
	if _, ret := device.GetCurrentClocksThrottleReasons(); ret == nvml.SUCCESS ||
		ret == nvml.ERROR_NOT_SUPPORTED {
		supported[APIThrottleReasons] = true
	}
	if _, ret := device.GetClockInfo(nvml.CLOCK_GRAPHICS); ret == nvml.SUCCESS ||
		ret == nvml.ERROR_NOT_SUPPORTED {
		supported[APIClockInfo] = true
	}
	if _, ret := device.GetTemperatureThreshold(
		nvml.TEMPERATURE_THRESHOLD_SLOWDOWN,
	); ret == nvml.SUCCESS || ret == nvml.ERROR_NOT_SUPPORTED {
		supported[APITempThreshold] = true
	}

	// Check context cancellation between tier groups
	if ctx.Err() != nil {
		return buildCapabilities(supported, driverVersion, cudaVersion)
	}

	// Probe Tier 3 APIs (advanced)
	if _, ret := device.GetNvLinkState(0); ret == nvml.SUCCESS ||
		ret == nvml.ERROR_NOT_SUPPORTED {
		supported[APINVLinkState] = true
	}
	if _, ret := device.GetNvLinkRemotePciInfo(0); ret == nvml.SUCCESS ||
		ret == nvml.ERROR_NOT_SUPPORTED || ret == nvml.ERROR_INVALID_ARGUMENT {
		supported[APINVLinkRemotePCI] = true
	}
	if _, ret := device.GetNvLinkErrorCounter(
		0, nvml.NvLinkErrorCounter(NvLinkErrorDL),
	); ret == nvml.SUCCESS || ret == nvml.ERROR_NOT_SUPPORTED {
		supported[APINVLinkErrors] = true
	}
	if _, ret := device.GetComputeRunningProcesses(); ret == nvml.SUCCESS ||
		ret == nvml.ERROR_NOT_SUPPORTED {
		supported[APIComputeProcesses] = true
	}

	return buildCapabilities(supported, driverVersion, cudaVersion)
}

// RealDevice is a real implementation of the Device interface.
type RealDevice struct {
	UnimplementedDevice // Embedded for forward compatibility
	device              nvml.Device
}

// GetName returns the product name of the device.
func (d *RealDevice) GetName(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("%w: %w", ErrContextCancelled, err)
	}

	name, ret := d.device.GetName()
	if ret != nvml.SUCCESS {
		return "", fmt.Errorf("failed to get device name: %s",
			nvml.ErrorString(ret))
	}
	return name, nil
}

// GetUUID returns the globally unique identifier of the device.
func (d *RealDevice) GetUUID(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("%w: %w", ErrContextCancelled, err)
	}

	uuid, ret := d.device.GetUUID()
	if ret != nvml.SUCCESS {
		return "", fmt.Errorf("failed to get device UUID: %s",
			nvml.ErrorString(ret))
	}
	return uuid, nil
}

// GetPCIInfo returns PCI bus information for the device.
func (d *RealDevice) GetPCIInfo(ctx context.Context) (*PCIInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrContextCancelled, err)
	}

	pciInfo, ret := d.device.GetPciInfo()
	if ret != nvml.SUCCESS {
		return nil, fmt.Errorf("failed to get PCI info: %s",
			nvml.ErrorString(ret))
	}

	// Convert BusIdLegacy byte array to string
	busID := string(pciInfo.BusIdLegacy[:])
	// Trim null bytes
	for i, b := range pciInfo.BusIdLegacy {
		if b == 0 {
			busID = string(pciInfo.BusIdLegacy[:i])
			break
		}
	}

	return &PCIInfo{
		BusID:  busID,
		Domain: pciInfo.Domain,
		Bus:    pciInfo.Bus,
		Device: pciInfo.Device,
	}, nil
}

// GetMemoryInfo returns memory usage information.
func (d *RealDevice) GetMemoryInfo(ctx context.Context) (*MemoryInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrContextCancelled, err)
	}

	memInfo, ret := d.device.GetMemoryInfo()
	if ret != nvml.SUCCESS {
		return nil, fmt.Errorf("failed to get memory info: %s",
			nvml.ErrorString(ret))
	}

	return &MemoryInfo{
		Total: memInfo.Total,
		Used:  memInfo.Used,
		Free:  memInfo.Free,
	}, nil
}

// GetTemperature returns the current temperature in Celsius.
func (d *RealDevice) GetTemperature(ctx context.Context) (uint32, error) {
	if err := ctx.Err(); err != nil {
		return 0, fmt.Errorf("%w: %w", ErrContextCancelled, err)
	}

	temp, ret := d.device.GetTemperature(nvml.TEMPERATURE_GPU)
	if ret != nvml.SUCCESS {
		return 0, fmt.Errorf("failed to get temperature: %s",
			nvml.ErrorString(ret))
	}
	return temp, nil
}

// GetPowerUsage returns the current power usage in milliwatts.
func (d *RealDevice) GetPowerUsage(ctx context.Context) (uint32, error) {
	if err := ctx.Err(); err != nil {
		return 0, fmt.Errorf("%w: %w", ErrContextCancelled, err)
	}

	power, ret := d.device.GetPowerUsage()
	if ret != nvml.SUCCESS {
		return 0, fmt.Errorf("failed to get power usage: %s",
			nvml.ErrorString(ret))
	}
	return power, nil
}

// GetUtilizationRates returns GPU and memory utilization rates.
func (d *RealDevice) GetUtilizationRates(
	ctx context.Context,
) (*Utilization, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrContextCancelled, err)
	}

	util, ret := d.device.GetUtilizationRates()
	if ret != nvml.SUCCESS {
		return nil, fmt.Errorf("failed to get utilization rates: %s",
			nvml.ErrorString(ret))
	}

	return &Utilization{
		GPU:    util.Gpu,
		Memory: util.Memory,
	}, nil
}

// GetPowerManagementLimit returns the power management limit in milliwatts.
func (d *RealDevice) GetPowerManagementLimit(
	ctx context.Context,
) (uint32, error) {
	if err := ctx.Err(); err != nil {
		return 0, fmt.Errorf("%w: %w", ErrContextCancelled, err)
	}

	limit, ret := d.device.GetPowerManagementLimit()
	if ret != nvml.SUCCESS {
		return 0, fmt.Errorf("failed to get power limit: %s",
			nvml.ErrorString(ret))
	}
	return limit, nil
}

// GetEccMode returns whether ECC is currently enabled.
func (d *RealDevice) GetEccMode(
	ctx context.Context,
) (current, pending bool, err error) {
	if err := ctx.Err(); err != nil {
		return false, false, fmt.Errorf("%w: %w", ErrContextCancelled, err)
	}

	curr, pend, ret := d.device.GetEccMode()
	if ret != nvml.SUCCESS {
		// ECC not supported is not an error, just return false
		if ret == nvml.ERROR_NOT_SUPPORTED {
			return false, false, nil
		}
		return false, false, fmt.Errorf("failed to get ECC mode: %s",
			nvml.ErrorString(ret))
	}
	return curr == nvml.FEATURE_ENABLED, pend == nvml.FEATURE_ENABLED, nil
}

// GetTotalEccErrors returns total ECC error count.
func (d *RealDevice) GetTotalEccErrors(
	ctx context.Context,
	errorType int,
) (uint64, error) {
	if err := ctx.Err(); err != nil {
		return 0, fmt.Errorf("%w: %w", ErrContextCancelled, err)
	}

	var nvmlErrorType nvml.MemoryErrorType
	if errorType == EccErrorCorrectable {
		nvmlErrorType = nvml.MEMORY_ERROR_TYPE_CORRECTED
	} else {
		nvmlErrorType = nvml.MEMORY_ERROR_TYPE_UNCORRECTED
	}

	// Get aggregate errors across all memory locations
	count, ret := d.device.GetTotalEccErrors(nvmlErrorType, nvml.AGGREGATE_ECC)
	if ret != nvml.SUCCESS {
		if ret == nvml.ERROR_NOT_SUPPORTED {
			return 0, nil
		}
		return 0, fmt.Errorf("failed to get ECC errors: %s",
			nvml.ErrorString(ret))
	}
	return count, nil
}

// GetCurrentClocksThrottleReasons returns the current throttle reason bitmask.
func (d *RealDevice) GetCurrentClocksThrottleReasons(
	ctx context.Context,
) (uint64, error) {
	if err := ctx.Err(); err != nil {
		return 0, fmt.Errorf("%w: %w", ErrContextCancelled, err)
	}

	reasons, ret := d.device.GetCurrentClocksThrottleReasons()
	if ret != nvml.SUCCESS {
		if ret == nvml.ERROR_NOT_SUPPORTED {
			return 0, nil
		}
		return 0, fmt.Errorf("failed to get throttle reasons: %s",
			nvml.ErrorString(ret))
	}
	return reasons, nil
}

// GetClockInfo returns the current clock frequency in MHz.
func (d *RealDevice) GetClockInfo(
	ctx context.Context,
	clockType int,
) (uint32, error) {
	if err := ctx.Err(); err != nil {
		return 0, fmt.Errorf("%w: %w", ErrContextCancelled, err)
	}

	var nvmlClockType nvml.ClockType
	if clockType == ClockGraphics {
		nvmlClockType = nvml.CLOCK_GRAPHICS
	} else {
		nvmlClockType = nvml.CLOCK_MEM
	}

	clock, ret := d.device.GetClockInfo(nvmlClockType)
	if ret != nvml.SUCCESS {
		return 0, fmt.Errorf("failed to get clock info: %s",
			nvml.ErrorString(ret))
	}
	return clock, nil
}

// GetTemperatureThreshold returns the temperature threshold in Celsius.
func (d *RealDevice) GetTemperatureThreshold(
	ctx context.Context,
	thresholdType int,
) (uint32, error) {
	if err := ctx.Err(); err != nil {
		return 0, fmt.Errorf("%w: %w", ErrContextCancelled, err)
	}

	var nvmlThresholdType nvml.TemperatureThresholds
	if thresholdType == TempThresholdShutdown {
		nvmlThresholdType = nvml.TEMPERATURE_THRESHOLD_SHUTDOWN
	} else {
		nvmlThresholdType = nvml.TEMPERATURE_THRESHOLD_SLOWDOWN
	}

	temp, ret := d.device.GetTemperatureThreshold(nvmlThresholdType)
	if ret != nvml.SUCCESS {
		if ret == nvml.ERROR_NOT_SUPPORTED {
			return 0, nil
		}
		return 0, fmt.Errorf("failed to get temp threshold: %s",
			nvml.ErrorString(ret))
	}
	return temp, nil
}

// GetCudaComputeCapability returns the CUDA compute capability as a string.
func (d *RealDevice) GetCudaComputeCapability(
	ctx context.Context,
) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("%w: %w", ErrContextCancelled, err)
	}

	major, minor, ret := d.device.GetCudaComputeCapability()
	if ret != nvml.SUCCESS {
		return "", fmt.Errorf("failed to get compute capability: %s",
			nvml.ErrorString(ret))
	}
	return fmt.Sprintf("%d.%d", major, minor), nil
}

// GetNvLinkState returns whether the specified NVLink is active.
func (d *RealDevice) GetNvLinkState(
	ctx context.Context,
	link int,
) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, fmt.Errorf("%w: %w", ErrContextCancelled, err)
	}

	state, ret := d.device.GetNvLinkState(link)
	if ret == nvml.ERROR_NOT_SUPPORTED {
		return false, nil // NVLink not supported on this device
	}
	if ret != nvml.SUCCESS {
		return false, fmt.Errorf("failed to get NVLink state: %s",
			nvml.ErrorString(ret))
	}
	return state == nvml.FEATURE_ENABLED, nil
}

// GetNvLinkRemotePciInfo returns PCI info of the remote device.
func (d *RealDevice) GetNvLinkRemotePciInfo(
	ctx context.Context,
	link int,
) (*PCIInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrContextCancelled, err)
	}

	pci, ret := d.device.GetNvLinkRemotePciInfo(link)
	if ret == nvml.ERROR_NOT_SUPPORTED || ret == nvml.ERROR_INVALID_ARGUMENT {
		return nil, nil // NVLink not supported or link not connected
	}
	if ret != nvml.SUCCESS {
		return nil, fmt.Errorf("failed to get NVLink remote PCI info: %s",
			nvml.ErrorString(ret))
	}

	// Convert BusId byte array to string
	busID := string(pci.BusId[:])
	for i, b := range pci.BusId {
		if b == 0 {
			busID = string(pci.BusId[:i])
			break
		}
	}

	return &PCIInfo{
		BusID:  busID,
		Domain: pci.Domain,
		Bus:    pci.Bus,
		Device: pci.Device,
	}, nil
}

// GetNvLinkErrorCounter returns the error count for the specified link.
func (d *RealDevice) GetNvLinkErrorCounter(
	ctx context.Context,
	link int,
	counterType int,
) (uint64, error) {
	if err := ctx.Err(); err != nil {
		return 0, fmt.Errorf("%w: %w", ErrContextCancelled, err)
	}

	count, ret := d.device.GetNvLinkErrorCounter(
		link, nvml.NvLinkErrorCounter(counterType))
	if ret == nvml.ERROR_NOT_SUPPORTED {
		return 0, nil
	}
	if ret != nvml.SUCCESS {
		return 0, fmt.Errorf("failed to get NVLink error counter: %s",
			nvml.ErrorString(ret))
	}
	return count, nil
}

// GetComputeRunningProcesses returns PIDs of processes using compute on GPU.
func (d *RealDevice) GetComputeRunningProcesses(
	ctx context.Context,
) ([]ProcessInfoNVML, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrContextCancelled, err)
	}

	procs, ret := d.device.GetComputeRunningProcesses()
	if ret == nvml.ERROR_NOT_SUPPORTED {
		return []ProcessInfoNVML{}, nil
	}
	if ret != nvml.SUCCESS {
		return nil, fmt.Errorf("failed to get running processes: %s",
			nvml.ErrorString(ret))
	}

	result := make([]ProcessInfoNVML, len(procs))
	for i, p := range procs {
		result[i] = ProcessInfoNVML{
			PID:           p.Pid,
			UsedGPUMemory: p.UsedGpuMemory,
		}
	}
	return result, nil
}
