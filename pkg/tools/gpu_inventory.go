// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

// Package tools implements MCP tool handlers for GPU operations.
package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ArangoGutierrez/k8s-gpu-mcp-server/pkg/nvml"
	"github.com/mark3labs/mcp-go/mcp"
	"k8s.io/klog/v2"
)

// GPUInventoryHandler handles the get_gpu_inventory tool.
type GPUInventoryHandler struct {
	nvmlClient nvml.Interface
}

// NewGPUInventoryHandler creates a new GPU inventory handler.
func NewGPUInventoryHandler(nvmlClient nvml.Interface) *GPUInventoryHandler {
	return &GPUInventoryHandler{
		nvmlClient: nvmlClient,
	}
}

// Handle processes the get_gpu_inventory tool request.
func (h *GPUInventoryHandler) Handle(
	ctx context.Context,
	request mcp.CallToolRequest,
) (*mcp.CallToolResult, error) {
	klog.InfoS("get_gpu_inventory invoked")

	// Get device count
	count, err := h.nvmlClient.GetDeviceCount(ctx)
	if err != nil {
		klog.ErrorS(err, "failed to get device count")
		return mcp.NewToolResultError(
			fmt.Sprintf("failed to get device count: %s", err)), nil
	}

	// Collect information for all devices
	gpus := make([]nvml.GPUInfo, 0, count)
	for i := 0; i < count; i++ {
		// Check for context cancellation
		select {
		case <-ctx.Done():
			klog.InfoS("context cancelled during GPU enumeration")
			return mcp.NewToolResultError(
				fmt.Sprintf("operation cancelled: %s", ctx.Err())), nil
		default:
		}

		device, err := h.nvmlClient.GetDeviceByIndex(ctx, i)
		if err != nil {
			klog.ErrorS(err, "failed to get device", "index", i)
			continue
		}
		if device == nil {
			klog.ErrorS(nil, "nil device returned without error", "index", i)
			continue
		}

		gpuInfo, err := h.collectDeviceInfo(ctx, i, device)
		if err != nil {
			klog.ErrorS(err, "failed to collect device info", "index", i)
			continue
		}

		gpus = append(gpus, *gpuInfo)
	}

	// Get system-level information
	var driverVersion, cudaVersion string
	if ver, err := h.nvmlClient.GetDriverVersion(ctx); err == nil {
		driverVersion = ver
	}
	if ver, err := h.nvmlClient.GetCudaDriverVersion(ctx); err == nil {
		cudaVersion = ver
	}

	// Create response
	response := map[string]interface{}{
		"api_version":    APIVersion,
		"status":         "success",
		"driver_version": driverVersion,
		"cuda_version":   cudaVersion,
		"device_count":   count,
		"devices":        gpus,
	}

	// Marshal to JSON
	jsonBytes, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		klog.ErrorS(err, "failed to marshal response")
		return mcp.NewToolResultError(
			fmt.Sprintf("failed to marshal response: %s", err)), nil
	}

	klog.InfoS("get_gpu_inventory completed", "count", count)

	return mcp.NewToolResultText(string(jsonBytes)), nil
}

// collectDeviceInfo gathers all information for a single device.
func (h *GPUInventoryHandler) collectDeviceInfo(
	ctx context.Context,
	index int,
	device nvml.Device,
) (*nvml.GPUInfo, error) {
	info := &nvml.GPUInfo{
		Index: index,
	}

	// Get name (required)
	name, err := device.GetName(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get name: %w", err)
	}
	info.Name = name

	// Get UUID (required)
	uuid, err := device.GetUUID(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get UUID: %w", err)
	}
	info.UUID = uuid

	// Get PCI info (required)
	pciInfo, err := device.GetPCIInfo(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get PCI info: %w", err)
	}
	info.BusID = pciInfo.BusID

	// Get compute capability (optional)
	if cc, err := device.GetCudaComputeCapability(ctx); err == nil {
		info.ComputeCapability = cc
	}

	// Collect memory info
	if memInfo, err := device.GetMemoryInfo(ctx); err != nil {
		klog.V(2).InfoS("failed to get memory info", "index", index, "error", err)
	} else {
		info.Memory = nvml.MemorySpec{
			TotalBytes: memInfo.Total,
			UsedBytes:  memInfo.Used,
			FreeBytes:  memInfo.Free,
		}
	}

	// Collect temperature with thresholds
	if temp, err := device.GetTemperature(ctx); err != nil {
		klog.V(2).InfoS("failed to get temperature", "index", index, "error", err)
	} else {
		info.Temperature.CurrentCelsius = temp
	}
	if slowdown, err := device.GetTemperatureThreshold(
		ctx, nvml.TempThresholdSlowdown); err == nil {
		info.Temperature.SlowdownCelsius = slowdown
	}
	if shutdown, err := device.GetTemperatureThreshold(
		ctx, nvml.TempThresholdShutdown); err == nil {
		info.Temperature.ShutdownCelsius = shutdown
	}

	// Collect power with limit
	if power, err := device.GetPowerUsage(ctx); err != nil {
		klog.V(2).InfoS("failed to get power usage", "index", index, "error", err)
	} else {
		info.Power.CurrentMW = power
	}
	if limit, err := device.GetPowerManagementLimit(ctx); err == nil {
		info.Power.LimitMW = limit
	}

	// Collect clock frequencies
	if smClock, err := device.GetClockInfo(
		ctx, nvml.ClockGraphics); err == nil {
		info.Clocks.SMMHZ = smClock
	}
	if memClock, err := device.GetClockInfo(
		ctx, nvml.ClockMemory); err == nil {
		info.Clocks.MemoryMHZ = memClock
	}

	// Collect utilization
	if util, err := device.GetUtilizationRates(ctx); err != nil {
		klog.V(2).InfoS("failed to get utilization", "index", index, "error", err)
	} else {
		info.Utilization.GPUPercent = util.GPU
		info.Utilization.MemoryPercent = util.Memory
	}

	// Collect ECC status (optional - may not be supported)
	if enabled, _, err := device.GetEccMode(ctx); err == nil {
		eccSpec := &nvml.ECCSpec{Enabled: enabled}
		if enabled {
			if correctable, err := device.GetTotalEccErrors(
				ctx, nvml.EccErrorCorrectable); err == nil {
				eccSpec.CorrectableErrors = correctable
			}
			if uncorrectable, err := device.GetTotalEccErrors(
				ctx, nvml.EccErrorUncorrectable); err == nil {
				eccSpec.UncorrectableErrors = uncorrectable
			}
		}
		info.ECC = eccSpec
	}

	return info, nil
}

// GetGPUInventoryTool returns the MCP tool definition for get_gpu_inventory.
func GetGPUInventoryTool() mcp.Tool {
	return mcp.NewTool("get_gpu_inventory",
		mcp.WithDescription(
			"Returns GPU inventory for all devices. "+
				"In agent mode: returns local GPU hardware details. "+
				"In gateway mode: returns cluster-wide inventory with "+
				"summary (total nodes, GPUs, types) and per-node GPU list. "+
				"Includes model, UUID, memory, temperature, and utilization. "+
				"When include_k8s_metadata is true (default in gateway mode), "+
				"also includes Kubernetes node labels, conditions, and GPU "+
				"resource allocation counts.",
		),
		mcp.WithBoolean("include_k8s_metadata",
			mcp.Description(
				"Include Kubernetes node metadata (labels, conditions, "+
					"GPU capacity/allocation). Default: true in gateway mode, "+
					"ignored in agent mode.",
			),
		),
	)
}
