// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ArangoGutierrez/k8s-gpu-mcp-server/pkg/nvml"
	"github.com/ArangoGutierrez/k8s-gpu-mcp-server/pkg/xid"
	"github.com/mark3labs/mcp-go/mcp"
	"k8s.io/klog/v2"
)

// xidParser is an interface for parsing XID events from kernel logs.
type xidParser interface {
	ParseKernelLogs(ctx context.Context) ([]xid.XIDEvent, error)
}

// AnalyzeXIDHandler handles the analyze_xid_errors tool.
type AnalyzeXIDHandler struct {
	nvmlClient nvml.Interface
	parser     xidParser
}

// NewAnalyzeXIDHandler creates a new XID analysis handler.
func NewAnalyzeXIDHandler(nvmlClient nvml.Interface) *AnalyzeXIDHandler {
	return &AnalyzeXIDHandler{
		nvmlClient: nvmlClient,
		parser:     xid.NewParser(),
	}
}

// EnrichedXIDError represents an XID error enriched with GPU metadata and
// error information.
type EnrichedXIDError struct {
	XIDCode     int    `json:"xid"`
	Name        string `json:"name"`
	Severity    string `json:"severity"`
	Description string `json:"description"`
	SREAction   string `json:"sre_action"`
	Category    string `json:"category"`
	GPUIndex    int    `json:"gpu_index"`
	GPUName     string `json:"gpu_name"`
	GPUUUID     string `json:"gpu_uuid"`
	PCIBusID    string `json:"pci_bus_id"`
	PID         int    `json:"pid,omitempty"`
	ProcessName string `json:"process_name,omitempty"`
	RawMessage  string `json:"raw_message"`
}

// SeveritySummary provides counts of errors by severity level.
type SeveritySummary struct {
	Fatal    int `json:"fatal"`
	Critical int `json:"critical"`
	Warning  int `json:"warning"`
	Info     int `json:"info"`
}

// AnalyzeXIDResponse is the structured response from the analyze_xid_errors
// tool.
// This struct is safe for concurrent read access; writes are confined to the
// handler goroutine that creates it.
type AnalyzeXIDResponse struct {
	APIVersion     string             `json:"api_version"`
	Status         string             `json:"status"`
	ErrorCount     int                `json:"error_count"`
	Errors         []EnrichedXIDError `json:"errors"`
	Summary        SeveritySummary    `json:"summary"`
	Recommendation string             `json:"recommendation"`
}

// Handle processes the analyze_xid_errors tool request.
func (h *AnalyzeXIDHandler) Handle(
	ctx context.Context,
	request mcp.CallToolRequest,
) (*mcp.CallToolResult, error) {
	klog.InfoS("analyze_xid_errors invoked")

	// Check context before expensive operation
	if err := ctx.Err(); err != nil {
		klog.InfoS("context cancelled before parsing")
		return mcp.NewToolResultError(
			fmt.Sprintf("operation cancelled: %s", err)), nil
	}

	// Parse kernel logs for XID events (prefers /dev/kmsg, falls back to dmesg)
	events, err := h.parser.ParseKernelLogs(ctx)
	if err != nil {
		klog.ErrorS(err, "failed to parse kernel logs")
		return mcp.NewToolResultError(
			fmt.Sprintf("failed to parse kernel logs: %s", err)), nil
	}

	klog.V(4).InfoS("parsed kernel logs", "events", len(events))

	// If no errors found, return success immediately
	if len(events) == 0 {
		response := AnalyzeXIDResponse{
			APIVersion:     APIVersion,
			Status:         "ok",
			ErrorCount:     0,
			Errors:         []EnrichedXIDError{},
			Summary:        SeveritySummary{},
			Recommendation: "No XID errors detected. GPU health is good.",
		}
		return h.marshalResponse(response)
	}

	// Enrich each event with XID info and GPU details
	enrichedErrors, err := h.enrichEvents(ctx, events)
	if err != nil {
		klog.ErrorS(err, "failed to enrich events")
		return mcp.NewToolResultError(
			fmt.Sprintf("failed to enrich error data: %s", err)), nil
	}

	// Create summary by severity
	summary := h.createSummary(enrichedErrors)

	// Determine overall status
	status := h.determineStatus(summary)

	// Generate recommendation
	recommendation := h.generateRecommendation(enrichedErrors, summary)

	// Build response
	response := AnalyzeXIDResponse{
		APIVersion:     APIVersion,
		Status:         status,
		ErrorCount:     len(enrichedErrors),
		Errors:         enrichedErrors,
		Summary:        summary,
		Recommendation: recommendation,
	}

	klog.InfoS("analyze_xid_errors completed",
		"errors", len(enrichedErrors), "status", status)

	return h.marshalResponse(response)
}

// enrichEvents enriches XID events with error info and GPU metadata.
func (h *AnalyzeXIDHandler) enrichEvents(
	ctx context.Context,
	events []xid.XIDEvent,
) ([]EnrichedXIDError, error) {
	enrichedErrors := make([]EnrichedXIDError, 0, len(events))

	for _, event := range events {
		// Check for context cancellation
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("context cancelled during enrichment: %w",
				err)
		}

		// Lookup XID error information
		info := xid.LookupOrUnknown(event.XIDCode)

		// Find GPU by PCI bus ID
		gpuIndex, gpuInfo := h.findGPUByPCI(ctx, event.PCIBusID)

		enriched := EnrichedXIDError{
			XIDCode:     event.XIDCode,
			Name:        info.Name,
			Severity:    info.Severity,
			Description: info.Description,
			SREAction:   info.Action,
			Category:    info.Category,
			GPUIndex:    gpuIndex,
			GPUName:     gpuInfo.Name,
			GPUUUID:     gpuInfo.UUID,
			PCIBusID:    event.PCIBusID,
			PID:         event.PID,
			ProcessName: event.ProcessName,
			RawMessage:  event.RawMessage,
		}

		enrichedErrors = append(enrichedErrors, enriched)
	}

	return enrichedErrors, nil
}

// gpuLookupResult holds GPU information found by PCI bus ID.
type gpuLookupResult struct {
	Name string
	UUID string
}

// findGPUByPCI maps a PCI bus ID to GPU index and retrieves GPU info.
// Returns -1 and empty info if GPU not found.
func (h *AnalyzeXIDHandler) findGPUByPCI(
	ctx context.Context,
	pciBusID string,
) (int, gpuLookupResult) {
	// Get device count
	count, err := h.nvmlClient.GetDeviceCount(ctx)
	if err != nil {
		klog.ErrorS(err, "failed to get device count")
		return -1, gpuLookupResult{}
	}

	// Iterate through all devices to find matching PCI bus ID
	for i := 0; i < count; i++ {
		// Check for context cancellation
		if err := ctx.Err(); err != nil {
			klog.V(4).InfoS("context cancelled during GPU lookup")
			return -1, gpuLookupResult{}
		}

		device, err := h.nvmlClient.GetDeviceByIndex(ctx, i)
		if err != nil {
			continue
		}

		// Get PCI info
		pciInfo, err := device.GetPCIInfo(ctx)
		if err != nil {
			continue
		}

		// Compare PCI bus IDs (case-insensitive)
		if strings.EqualFold(pciInfo.BusID, pciBusID) {
			// Found matching GPU, get name and UUID
			name, _ := device.GetName(ctx)
			uuid, _ := device.GetUUID(ctx)

			return i, gpuLookupResult{
				Name: name,
				UUID: uuid,
			}
		}
	}

	// GPU not found
	return -1, gpuLookupResult{
		Name: "Unknown GPU",
		UUID: "unknown",
	}
}

// createSummary counts errors by severity level.
func (h *AnalyzeXIDHandler) createSummary(
	errors []EnrichedXIDError,
) SeveritySummary {
	summary := SeveritySummary{}

	for _, err := range errors {
		switch err.Severity {
		case "fatal":
			summary.Fatal++
		case "critical":
			summary.Critical++
		case "warning":
			summary.Warning++
		case "info":
			summary.Info++
		}
	}

	return summary
}

// determineStatus determines overall system status based on error severity.
func (h *AnalyzeXIDHandler) determineStatus(summary SeveritySummary) string {
	if summary.Fatal > 0 {
		return "critical"
	}
	if summary.Critical > 0 {
		return "degraded"
	}
	if summary.Warning > 0 {
		return "warning"
	}
	return "ok"
}

// generateRecommendation creates an actionable recommendation based on
// detected errors.
func (h *AnalyzeXIDHandler) generateRecommendation(
	errors []EnrichedXIDError,
	summary SeveritySummary,
) string {
	if len(errors) == 0 {
		return "No XID errors detected. GPU health is good."
	}

	var recommendations []string

	// Fatal errors require immediate action
	if summary.Fatal > 0 {
		recommendations = append(recommendations,
			fmt.Sprintf("URGENT: %d fatal error(s) detected. "+
				"Drain affected nodes immediately.", summary.Fatal))

		// List specific fatal XIDs
		for _, err := range errors {
			if err.Severity == "fatal" {
				recommendations = append(recommendations,
					fmt.Sprintf("- GPU %d: XID %d (%s)",
						err.GPUIndex, err.XIDCode, err.Name))
			}
		}
	}

	// Critical errors require attention
	if summary.Critical > 0 {
		recommendations = append(recommendations,
			fmt.Sprintf("%d critical error(s) detected. "+
				"Investigate and consider GPU reset or replacement.",
				summary.Critical))
	}

	// Warnings should be monitored
	if summary.Warning > 0 {
		recommendations = append(recommendations,
			fmt.Sprintf("%d warning(s) detected. Monitor for frequency. "+
				"Review application logs.", summary.Warning))
	}

	// Combine all recommendations
	if len(recommendations) > 0 {
		return strings.Join(recommendations, " ")
	}

	return "XID errors detected. Review detailed error information above."
}

// marshalResponse marshals the response to JSON and returns as tool result.
func (h *AnalyzeXIDHandler) marshalResponse(
	response AnalyzeXIDResponse,
) (*mcp.CallToolResult, error) {
	jsonBytes, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		klog.ErrorS(err, "failed to marshal response")
		return mcp.NewToolResultError(
			fmt.Sprintf("failed to marshal response: %s", err)), nil
	}

	return mcp.NewToolResultText(string(jsonBytes)), nil
}

// GetAnalyzeXIDTool returns the MCP tool definition for analyze_xid_errors.
func GetAnalyzeXIDTool() mcp.Tool {
	return mcp.NewTool("analyze_xid_errors",
		mcp.WithDescription(
			"Analyze NVIDIA GPU XID (eXception ID) errors from kernel logs. "+
				"XID errors are hardware failures logged by the NVIDIA driver "+
				"indicating issues like memory corruption, bus failures, or "+
				"thermal problems. Returns structured error data with severity "+
				"classifications and SRE-actionable recommendations. "+
				"Note: May require elevated permissions to read kernel logs.",
		),
	)
}
