// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

package nvml

import (
	"context"
)

// Compile-time interface satisfaction checks.
var (
	_ Interface = UnimplementedInterface{}
	_ Device    = UnimplementedDevice{}
)

// UnimplementedInterface provides default implementations that return
// ErrNotImplemented for all Interface methods. Embed this in your
// implementation for forward compatibility when new methods are added.
//
// Example:
//
//	type MyNVML struct {
//	    nvml.UnimplementedInterface
//	    // your fields
//	}
type UnimplementedInterface struct{}

// Init returns ErrNotImplemented.
func (UnimplementedInterface) Init(_ context.Context) error {
	return ErrNotImplemented
}

// Shutdown returns ErrNotImplemented.
func (UnimplementedInterface) Shutdown(_ context.Context) error {
	return ErrNotImplemented
}

// GetDeviceCount returns ErrNotImplemented.
func (UnimplementedInterface) GetDeviceCount(_ context.Context) (int, error) {
	return 0, ErrNotImplemented
}

// GetDeviceByIndex returns ErrNotImplemented.
func (UnimplementedInterface) GetDeviceByIndex(
	_ context.Context,
	_ int,
) (Device, error) {
	return nil, ErrNotImplemented
}

// GetDriverVersion returns ErrNotImplemented.
func (UnimplementedInterface) GetDriverVersion(
	_ context.Context,
) (string, error) {
	return "", ErrNotImplemented
}

// GetCudaDriverVersion returns ErrNotImplemented.
func (UnimplementedInterface) GetCudaDriverVersion(
	_ context.Context,
) (string, error) {
	return "", ErrNotImplemented
}

// GetCapabilities returns ErrNotImplemented.
func (UnimplementedInterface) GetCapabilities(
	_ context.Context,
) (*Capabilities, error) {
	return nil, ErrNotImplemented
}

// UnimplementedDevice provides default implementations that return
// ErrNotImplemented for all Device methods. Embed this in your
// implementation for forward compatibility when new methods are added.
//
// Example:
//
//	type MyDevice struct {
//	    nvml.UnimplementedDevice
//	    // your fields
//	}
type UnimplementedDevice struct{}

// GetName returns ErrNotImplemented.
func (UnimplementedDevice) GetName(_ context.Context) (string, error) {
	return "", ErrNotImplemented
}

// GetUUID returns ErrNotImplemented.
func (UnimplementedDevice) GetUUID(_ context.Context) (string, error) {
	return "", ErrNotImplemented
}

// GetPCIInfo returns ErrNotImplemented.
func (UnimplementedDevice) GetPCIInfo(_ context.Context) (*PCIInfo, error) {
	return nil, ErrNotImplemented
}

// GetMemoryInfo returns ErrNotImplemented.
func (UnimplementedDevice) GetMemoryInfo(
	_ context.Context,
) (*MemoryInfo, error) {
	return nil, ErrNotImplemented
}

// GetTemperature returns ErrNotImplemented.
func (UnimplementedDevice) GetTemperature(_ context.Context) (uint32, error) {
	return 0, ErrNotImplemented
}

// GetPowerUsage returns ErrNotImplemented.
func (UnimplementedDevice) GetPowerUsage(_ context.Context) (uint32, error) {
	return 0, ErrNotImplemented
}

// GetUtilizationRates returns ErrNotImplemented.
func (UnimplementedDevice) GetUtilizationRates(
	_ context.Context,
) (*Utilization, error) {
	return nil, ErrNotImplemented
}

// GetPowerManagementLimit returns ErrNotImplemented.
func (UnimplementedDevice) GetPowerManagementLimit(
	_ context.Context,
) (uint32, error) {
	return 0, ErrNotImplemented
}

// GetEccMode returns ErrNotImplemented.
func (UnimplementedDevice) GetEccMode(
	_ context.Context,
) (current, pending bool, err error) {
	return false, false, ErrNotImplemented
}

// GetTotalEccErrors returns ErrNotImplemented.
func (UnimplementedDevice) GetTotalEccErrors(
	_ context.Context,
	_ int,
) (uint64, error) {
	return 0, ErrNotImplemented
}

// GetCurrentClocksThrottleReasons returns ErrNotImplemented.
func (UnimplementedDevice) GetCurrentClocksThrottleReasons(
	_ context.Context,
) (uint64, error) {
	return 0, ErrNotImplemented
}

// GetClockInfo returns ErrNotImplemented.
func (UnimplementedDevice) GetClockInfo(
	_ context.Context,
	_ int,
) (uint32, error) {
	return 0, ErrNotImplemented
}

// GetTemperatureThreshold returns ErrNotImplemented.
func (UnimplementedDevice) GetTemperatureThreshold(
	_ context.Context,
	_ int,
) (uint32, error) {
	return 0, ErrNotImplemented
}

// GetCudaComputeCapability returns ErrNotImplemented.
func (UnimplementedDevice) GetCudaComputeCapability(
	_ context.Context,
) (string, error) {
	return "", ErrNotImplemented
}

// GetNvLinkState returns ErrNotImplemented.
func (UnimplementedDevice) GetNvLinkState(
	_ context.Context,
	_ int,
) (bool, error) {
	return false, ErrNotImplemented
}

// GetNvLinkRemotePciInfo returns ErrNotImplemented.
func (UnimplementedDevice) GetNvLinkRemotePciInfo(
	_ context.Context,
	_ int,
) (*PCIInfo, error) {
	return nil, ErrNotImplemented
}

// GetNvLinkErrorCounter returns ErrNotImplemented.
func (UnimplementedDevice) GetNvLinkErrorCounter(
	_ context.Context,
	_ int,
	_ int,
) (uint64, error) {
	return 0, ErrNotImplemented
}

// GetComputeRunningProcesses returns ErrNotImplemented.
func (UnimplementedDevice) GetComputeRunningProcesses(
	_ context.Context,
) ([]ProcessInfoNVML, error) {
	return nil, ErrNotImplemented
}
