// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/ArangoGutierrez/k8s-gpu-mcp-server/pkg/nvml"
	"github.com/mark3labs/mcp-go/mcp"
	"k8s.io/klog/v2"
)

// NVLinkDegradedErrorThreshold is the cumulative NVLink error count above
// which link health is considered "degraded". Based on operational experience
// where >1000 errors indicate persistent link issues requiring attention.
// Below this threshold but >0 is "warning"; 0 errors is "healthy".
const NVLinkDegradedErrorThreshold = 1000

// NVLinkTopologyHandler handles the get_nvlink_topology tool.
type NVLinkTopologyHandler struct {
	nvmlClient nvml.Interface
}

// NewNVLinkTopologyHandler creates a new NVLink topology handler.
func NewNVLinkTopologyHandler(nvmlClient nvml.Interface) *NVLinkTopologyHandler {
	return &NVLinkTopologyHandler{
		nvmlClient: nvmlClient,
	}
}

// NVLinkTopologyResponse is the response structure for NVLink topology.
// This struct is safe for concurrent read access; writes are confined to the
// handler goroutine that creates it.
type NVLinkTopologyResponse struct {
	APIVersion   string             `json:"api_version"`
	GPUCount     int                `json:"gpu_count"`
	Links        []NVLinkConnection `json:"links"`
	Summary      string             `json:"summary"`
	HealthStatus string             `json:"health_status"`
}

// NVLinkConnection represents a single NVLink connection between GPUs.
type NVLinkConnection struct {
	GPU1       int    `json:"gpu1"`
	GPU2       int    `json:"gpu2"`
	LinkType   string `json:"link_type"`
	Active     bool   `json:"active"`
	ErrorCount uint64 `json:"error_count"`
}

// Handle processes the get_nvlink_topology tool request.
func (h *NVLinkTopologyHandler) Handle(
	ctx context.Context,
	request mcp.CallToolRequest,
) (*mcp.CallToolResult, error) {
	klog.InfoS("get_nvlink_topology invoked")

	if err := ctx.Err(); err != nil {
		return mcp.NewToolResultError(
			fmt.Sprintf("operation cancelled: %s", err)), nil
	}

	count, err := h.nvmlClient.GetDeviceCount(ctx)
	if err != nil {
		klog.ErrorS(err, "failed to get device count")
		return mcp.NewToolResultError(
			fmt.Sprintf("failed to get device count: %s", err)), nil
	}

	response := NVLinkTopologyResponse{
		APIVersion: APIVersion,
		GPUCount:   count,
		Links:      make([]NVLinkConnection, 0),
	}

	if count == 0 {
		response.Summary = "No GPU devices detected"
		response.HealthStatus = "unknown"
		return h.marshalResponse(response)
	}

	// Build PCI bus ID to GPU index map for lookups
	pciToGPU := make(map[string]int)
	for i := 0; i < count; i++ {
		device, err := h.nvmlClient.GetDeviceByIndex(ctx, i)
		if err != nil {
			continue
		}
		pciInfo, err := device.GetPCIInfo(ctx)
		if err != nil {
			continue
		}
		pciToGPU[pciInfo.BusID] = i
	}

	// Discover NVLink connections
	linkMap := make(map[string]NVLinkConnection) // Dedupe bidirectional links

	for i := 0; i < count; i++ {
		if err := ctx.Err(); err != nil {
			return mcp.NewToolResultError(
				fmt.Sprintf("operation cancelled: %s", err)), nil
		}

		device, err := h.nvmlClient.GetDeviceByIndex(ctx, i)
		if err != nil {
			klog.ErrorS(err, "failed to get device", "index", i)
			continue
		}

		h.discoverNVLinks(ctx, i, device, linkMap, pciToGPU)
	}

	// Convert map to sorted slice
	for _, link := range linkMap {
		response.Links = append(response.Links, link)
	}
	// Sort by GPU1, then GPU2 for deterministic output
	sort.Slice(response.Links, func(i, j int) bool {
		if response.Links[i].GPU1 != response.Links[j].GPU1 {
			return response.Links[i].GPU1 < response.Links[j].GPU1
		}
		return response.Links[i].GPU2 < response.Links[j].GPU2
	})

	// Generate summary and health status
	response.Summary = h.generateSummary(count, response.Links)
	response.HealthStatus = h.calculateHealthStatus(response.Links)

	klog.InfoS("get_nvlink_topology completed",
		"gpus", count, "links", len(response.Links), "health", response.HealthStatus)

	return h.marshalResponse(response)
}

// discoverNVLinks finds all NVLink connections for a device.
func (h *NVLinkTopologyHandler) discoverNVLinks(
	ctx context.Context,
	gpuIdx int,
	device nvml.Device,
	linkMap map[string]NVLinkConnection,
	pciToGPU map[string]int,
) {
	for link := 0; link < nvml.MaxNvLinks; link++ {
		if err := ctx.Err(); err != nil {
			return
		}

		active, err := device.GetNvLinkState(ctx, link)
		if err != nil {
			klog.V(4).InfoS("failed to get NVLink state",
				"gpu", gpuIdx, "link", link, "error", err)
			continue
		}

		if !active {
			continue // Link not connected
		}

		remotePCI, err := device.GetNvLinkRemotePciInfo(ctx, link)
		if err != nil || remotePCI == nil {
			continue
		}

		// Get error count (sum of all error types)
		var totalErrors uint64
		for counter := 0; counter <= nvml.NvLinkErrorCRCData; counter++ {
			count, err := device.GetNvLinkErrorCounter(ctx, link, counter)
			if err == nil {
				totalErrors += count
			}
		}

		// Look up remote GPU index from PCI bus ID
		remoteGPU, found := pciToGPU[remotePCI.BusID]
		if !found {
			// Fallback: use bus number as proxy. This heuristic may be
			// inaccurate if GPU indices don't align with PCI bus ordering.
			klog.V(2).InfoS("remote GPU PCI bus ID not in map, using heuristic",
				"gpu", gpuIdx, "link", link,
				"remoteBusID", remotePCI.BusID,
				"remoteBus", remotePCI.Bus,
				"heuristicGPU", int(remotePCI.Bus)-1)
			remoteGPU = int(remotePCI.Bus) - 1
		}

		// Create canonical key (smaller GPU index first)
		gpu1, gpu2 := gpuIdx, remoteGPU
		if gpu1 > gpu2 {
			gpu1, gpu2 = gpu2, gpu1
		}
		key := fmt.Sprintf("%d-%d", gpu1, gpu2)

		// Only add if not already discovered, or update error count
		if existing, exists := linkMap[key]; !exists {
			linkMap[key] = NVLinkConnection{
				GPU1:       gpu1,
				GPU2:       gpu2,
				LinkType:   "NVLink",
				Active:     true,
				ErrorCount: totalErrors,
			}
		} else if totalErrors > existing.ErrorCount {
			// Keep the higher error count (bidirectional links)
			existing.ErrorCount = totalErrors
			linkMap[key] = existing
		}
	}
}

// generateSummary creates a human-readable topology description.
func (h *NVLinkTopologyHandler) generateSummary(
	gpuCount int,
	links []NVLinkConnection,
) string {
	if len(links) == 0 {
		if gpuCount == 1 {
			return "Single GPU system, no NVLink connections"
		}
		return fmt.Sprintf("%d GPUs detected, no NVLink connections (PCIe only)",
			gpuCount)
	}

	var parts []string
	var degradedLinks []string

	for _, link := range links {
		desc := fmt.Sprintf("GPU %d↔GPU %d", link.GPU1, link.GPU2)
		if link.ErrorCount > 0 {
			degradedLinks = append(degradedLinks,
				fmt.Sprintf("%s (%d errors)", desc, link.ErrorCount))
		} else {
			parts = append(parts, desc)
		}
	}

	summary := fmt.Sprintf("%d GPUs with %d NVLink connection(s)",
		gpuCount, len(links))
	if len(parts) > 0 {
		summary += ": " + strings.Join(parts, ", ")
	}
	if len(degradedLinks) > 0 {
		summary += ". Degraded: " + strings.Join(degradedLinks, ", ")
	}

	return summary
}

// calculateHealthStatus determines overall NVLink health.
// Note: Currently all links in the slice are active (we only add active links).
// The Active field is retained for API stability and future enhancements.
func (h *NVLinkTopologyHandler) calculateHealthStatus(
	links []NVLinkConnection,
) string {
	if len(links) == 0 {
		return "healthy" // No NVLinks is valid (consumer GPU or single GPU)
	}

	var totalErrors uint64
	for _, link := range links {
		totalErrors += link.ErrorCount
	}

	switch {
	case totalErrors > NVLinkDegradedErrorThreshold:
		return "degraded"
	case totalErrors > 0:
		return "warning"
	default:
		return "healthy"
	}
}

// marshalResponse marshals the response to JSON.
func (h *NVLinkTopologyHandler) marshalResponse(
	response NVLinkTopologyResponse,
) (*mcp.CallToolResult, error) {
	jsonBytes, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		klog.ErrorS(err, "failed to marshal response")
		return mcp.NewToolResultError(
			fmt.Sprintf("failed to marshal response: %s", err)), nil
	}
	return mcp.NewToolResultText(string(jsonBytes)), nil
}

// GetNVLinkTopologyTool returns the MCP tool definition.
func GetNVLinkTopologyTool() mcp.Tool {
	return mcp.NewTool("get_nvlink_topology",
		mcp.WithDescription(
			"Get NVLink interconnect topology and health status. "+
				"Returns GPU-to-GPU NVLink connections, link state, "+
				"and error counters for multi-GPU systems. "+
				"Useful for diagnosing silent NVLink degradation.",
		),
	)
}
