// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

//go:build !cgo
// +build !cgo

package nvml

import (
	"context"
)

// Real is a stub that returns an error when CGO is disabled.
// This allows the code to compile without NVML library.
type Real struct {
	UnimplementedInterface // Embedded for forward compatibility
}

// Compile-time interface satisfaction checks.
var (
	_ Interface = (*Real)(nil)
	_ Device    = (*RealDevice)(nil)
)

// NewReal creates a stub that will error on init.
func NewReal() *Real {
	return &Real{}
}

// Init returns an error indicating CGO is required.
func (r *Real) Init(ctx context.Context) error {
	return ErrCGORequired
}

// Shutdown is a no-op stub.
func (r *Real) Shutdown(ctx context.Context) error {
	return nil
}

// GetDeviceCount returns an error.
func (r *Real) GetDeviceCount(ctx context.Context) (int, error) {
	return 0, ErrCGORequired
}

// GetDeviceByIndex returns an error.
func (r *Real) GetDeviceByIndex(ctx context.Context, idx int) (Device, error) {
	return nil, ErrCGORequired
}

// GetDriverVersion returns an error indicating CGO is required.
func (r *Real) GetDriverVersion(ctx context.Context) (string, error) {
	return "", ErrCGORequired
}

// GetCudaDriverVersion returns an error indicating CGO is required.
func (r *Real) GetCudaDriverVersion(ctx context.Context) (string, error) {
	return "", ErrCGORequired
}

// RealDevice is a stub for non-CGO builds.
type RealDevice struct {
	UnimplementedDevice // Embedded for forward compatibility
}

// GetName returns an error indicating CGO is required.
func (d *RealDevice) GetName(ctx context.Context) (string, error) {
	return "", ErrCGORequired
}

// GetUUID returns an error indicating CGO is required.
func (d *RealDevice) GetUUID(ctx context.Context) (string, error) {
	return "", ErrCGORequired
}

// GetPCIInfo returns an error indicating CGO is required.
func (d *RealDevice) GetPCIInfo(ctx context.Context) (*PCIInfo, error) {
	return nil, ErrCGORequired
}

// GetMemoryInfo returns an error indicating CGO is required.
func (d *RealDevice) GetMemoryInfo(ctx context.Context) (*MemoryInfo, error) {
	return nil, ErrCGORequired
}

// GetTemperature returns an error indicating CGO is required.
func (d *RealDevice) GetTemperature(ctx context.Context) (uint32, error) {
	return 0, ErrCGORequired
}

// GetPowerUsage returns an error indicating CGO is required.
func (d *RealDevice) GetPowerUsage(ctx context.Context) (uint32, error) {
	return 0, ErrCGORequired
}

// GetUtilizationRates returns an error indicating CGO is required.
func (d *RealDevice) GetUtilizationRates(
	ctx context.Context,
) (*Utilization, error) {
	return nil, ErrCGORequired
}

// GetPowerManagementLimit returns an error indicating CGO is required.
func (d *RealDevice) GetPowerManagementLimit(
	ctx context.Context,
) (uint32, error) {
	return 0, ErrCGORequired
}

// GetEccMode returns an error indicating CGO is required.
func (d *RealDevice) GetEccMode(
	ctx context.Context,
) (current, pending bool, err error) {
	return false, false, ErrCGORequired
}

// GetTotalEccErrors returns an error indicating CGO is required.
func (d *RealDevice) GetTotalEccErrors(
	ctx context.Context,
	errorType int,
) (uint64, error) {
	return 0, ErrCGORequired
}

// GetCurrentClocksThrottleReasons returns an error indicating CGO is required.
func (d *RealDevice) GetCurrentClocksThrottleReasons(
	ctx context.Context,
) (uint64, error) {
	return 0, ErrCGORequired
}

// GetClockInfo returns an error indicating CGO is required.
func (d *RealDevice) GetClockInfo(
	ctx context.Context,
	clockType int,
) (uint32, error) {
	return 0, ErrCGORequired
}

// GetTemperatureThreshold returns an error indicating CGO is required.
func (d *RealDevice) GetTemperatureThreshold(
	ctx context.Context,
	thresholdType int,
) (uint32, error) {
	return 0, ErrCGORequired
}

// GetCudaComputeCapability returns an error indicating CGO is required.
func (d *RealDevice) GetCudaComputeCapability(
	ctx context.Context,
) (string, error) {
	return "", ErrCGORequired
}

// GetNvLinkState returns an error indicating CGO is required.
func (d *RealDevice) GetNvLinkState(
	ctx context.Context,
	link int,
) (bool, error) {
	return false, ErrCGORequired
}

// GetNvLinkRemotePciInfo returns an error indicating CGO is required.
func (d *RealDevice) GetNvLinkRemotePciInfo(
	ctx context.Context,
	link int,
) (*PCIInfo, error) {
	return nil, ErrCGORequired
}

// GetNvLinkErrorCounter returns an error indicating CGO is required.
func (d *RealDevice) GetNvLinkErrorCounter(
	ctx context.Context,
	link int,
	counterType int,
) (uint64, error) {
	return 0, ErrCGORequired
}
