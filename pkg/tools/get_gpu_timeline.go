// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/ArangoGutierrez/k8s-gpu-mcp-server/pkg/blackbox"
	"github.com/ArangoGutierrez/k8s-gpu-mcp-server/pkg/nvml"
	"github.com/ArangoGutierrez/k8s-gpu-mcp-server/pkg/xid"
	"github.com/mark3labs/mcp-go/mcp"
	"k8s.io/klog/v2"
)

// Default values for get_gpu_timeline tool.
const (
	DefaultTimelineDuration = 30 * time.Minute
	MaxTimelineDuration     = 24 * time.Hour
)

// GPUTimelineHandler handles the get_gpu_timeline tool.
type GPUTimelineHandler struct {
	recorder   *blackbox.Recorder
	nvmlClient nvml.Interface
	xidWatcher *xid.Watcher // optional, may be nil
}

// GPUTimelineOption configures GPUTimelineHandler.
type GPUTimelineOption func(*GPUTimelineHandler)

// WithTimelineRecorder sets the flight recorder.
func WithTimelineRecorder(r *blackbox.Recorder) GPUTimelineOption {
	return func(h *GPUTimelineHandler) {
		h.recorder = r
	}
}

// WithTimelineXIDWatcher sets the XID watcher for event extraction.
func WithTimelineXIDWatcher(w *xid.Watcher) GPUTimelineOption {
	return func(h *GPUTimelineHandler) {
		h.xidWatcher = w
	}
}

// NewGPUTimelineHandler creates a new get_gpu_timeline handler.
func NewGPUTimelineHandler(
	nvmlClient nvml.Interface,
	opts ...GPUTimelineOption,
) *GPUTimelineHandler {
	h := &GPUTimelineHandler{
		nvmlClient: nvmlClient,
	}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// GPUTimelineResponse is the response from get_gpu_timeline.
// This struct is safe for concurrent read access; writes are confined to the
// handler goroutine that creates it.
type GPUTimelineResponse struct {
	APIVersion     string          `json:"api_version"`
	GPUUUID        string          `json:"gpu_uuid"`
	GPUIndex       int             `json:"gpu_index"`
	GPUName        string          `json:"gpu_name,omitempty"`
	Duration       string          `json:"duration"`
	SampleCount    int             `json:"sample_count"`
	SampleInterval string          `json:"sample_interval"`
	DataPoints     []DataPoint     `json:"data_points"`
	Statistics     *TimelineStats  `json:"statistics,omitempty"`
	Events         []TimelineEvent `json:"events,omitempty"`
	Warning        string          `json:"warning,omitempty"`
}

// MultiGPUTimelineResponse wraps multiple GPU timelines.
// This struct is safe for concurrent read access; writes are confined to the
// handler goroutine that creates it.
type MultiGPUTimelineResponse struct {
	APIVersion string                `json:"api_version"`
	GPUCount   int                   `json:"gpu_count"`
	Duration  string                `json:"duration"`
	Timelines []GPUTimelineResponse `json:"timelines"`
	Warning   string                `json:"warning,omitempty"`
}

// DataPoint represents a single GPU telemetry sample.
type DataPoint struct {
	Timestamp          string `json:"timestamp"`
	TemperatureCelsius uint32 `json:"temperature_celsius"`
	PowerMW            uint32 `json:"power_mw"`
	GPUUtilPercent     uint32 `json:"gpu_util_percent"`
	MemUtilPercent     uint32 `json:"mem_util_percent"`
	MemUsedBytes       uint64 `json:"mem_used_bytes"`
	MemTotalBytes      uint64 `json:"mem_total_bytes"`
	Throttling         bool   `json:"throttling"`
}

// TimelineStats contains min/max/avg statistics for key metrics.
type TimelineStats struct {
	Temperature StatValues `json:"temperature"`
	PowerMW     StatValues `json:"power_mw"`
	GPUUtil     StatValues `json:"gpu_util"`
	MemUtil     StatValues `json:"mem_util"`
}

// StatValues holds min/max/avg for a single metric.
type StatValues struct {
	Min uint32  `json:"min"`
	Max uint32  `json:"max"`
	Avg float64 `json:"avg"`
}

// TimelineEvent represents an event in the GPU timeline.
type TimelineEvent struct {
	Timestamp   string `json:"timestamp"`
	Type        string `json:"type"`                  // "throttle_start", "throttle_end", "xid"
	Code        int    `json:"code,omitempty"`        // XID code if type="xid"
	Description string `json:"description,omitempty"` // Event description
	Severity    string `json:"severity,omitempty"`    // Event severity
}

// timelineArgs holds parsed arguments for get_gpu_timeline.
type timelineArgs struct {
	gpuUUID      string
	gpuIndex     *int
	duration     time.Duration
	includeStats bool
}

// Handle processes the get_gpu_timeline tool request.
func (h *GPUTimelineHandler) Handle(
	ctx context.Context,
	request mcp.CallToolRequest,
) (*mcp.CallToolResult, error) {
	klog.InfoS("get_gpu_timeline invoked")

	// Check context before starting
	if err := ctx.Err(); err != nil {
		klog.InfoS("context cancelled before timeline query")
		return mcp.NewToolResultError(
			fmt.Sprintf("operation cancelled: %s", err)), nil
	}

	// 1. Validate recorder is available
	if h.recorder == nil || !h.recorder.IsRunning() {
		return mcp.NewToolResultError(
			"flight recorder not available: the recorder must be running to query GPU timeline data"), nil
	}

	// 2. Parse arguments
	args, err := h.parseArgs(request)
	if err != nil {
		klog.ErrorS(err, "failed to parse arguments")
		return mcp.NewToolResultError(err.Error()), nil
	}

	klog.V(4).InfoS("get_gpu_timeline args",
		"gpu_uuid", args.gpuUUID,
		"gpu_index", args.gpuIndex,
		"duration", args.duration,
		"include_stats", args.includeStats,
	)

	// 3. Resolve GPU UUID from index if needed
	gpuUUID, err := h.resolveGPUUUID(ctx, args.gpuUUID, args.gpuIndex)
	if err != nil {
		klog.ErrorS(err, "failed to resolve GPU")
		return mcp.NewToolResultError(err.Error()), nil
	}

	// 4. Query timeline(s)
	if gpuUUID == "" {
		// Query all GPUs
		return h.handleAllGPUs(ctx, args)
	}
	return h.handleSingleGPU(ctx, gpuUUID, args)
}

// parseArgs extracts and validates arguments from the request.
func (h *GPUTimelineHandler) parseArgs(
	request mcp.CallToolRequest,
) (*timelineArgs, error) {
	args := &timelineArgs{
		duration:     DefaultTimelineDuration,
		includeStats: true, // default per spec
	}

	reqArgs := request.GetArguments()

	// gpu_uuid (optional)
	if uuidRaw, ok := reqArgs["gpu_uuid"]; ok && uuidRaw != nil {
		if uuid, ok := uuidRaw.(string); ok && uuid != "" {
			args.gpuUUID = uuid
		}
	}

	// gpu_index (optional)
	if indexRaw, ok := reqArgs["gpu_index"]; ok && indexRaw != nil {
		switch v := indexRaw.(type) {
		case float64:
			idx := int(v)
			if idx < 0 {
				return nil, fmt.Errorf("gpu_index must be >= 0, got %d", idx)
			}
			args.gpuIndex = &idx
		case int:
			if v < 0 {
				return nil, fmt.Errorf("gpu_index must be >= 0, got %d", v)
			}
			args.gpuIndex = &v
		}
	}

	// duration (optional, default: "30m")
	if durationRaw, ok := reqArgs["duration"]; ok && durationRaw != nil {
		if durationStr, ok := durationRaw.(string); ok && durationStr != "" {
			d, err := parseDuration(durationStr)
			if err != nil {
				return nil, fmt.Errorf("invalid duration: %s: %w", durationStr, err)
			}
			if d <= 0 {
				return nil, fmt.Errorf("duration must be positive, got %s", durationStr)
			}
			if d > MaxTimelineDuration {
				return nil, fmt.Errorf("duration exceeds maximum of %s", MaxTimelineDuration)
			}
			args.duration = d
		}
	}

	// include_stats (optional, default: true)
	if statsRaw, ok := reqArgs["include_stats"]; ok && statsRaw != nil {
		if stats, ok := statsRaw.(bool); ok {
			args.includeStats = stats
		}
	}

	return args, nil
}

// parseDuration converts duration strings like "10m", "1h", "30m" to time.Duration.
// Supports standard Go duration format plus convenience formats.
func parseDuration(s string) (time.Duration, error) {
	// Use Go's standard duration parser which handles:
	// "10m", "1h", "30s", "2h30m", "1.5h", etc.
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid duration format %q: use formats like 10m, 1h, 30s", s)
	}
	return d, nil
}

// resolveGPUUUID resolves a GPU UUID from either direct UUID or index.
// If both uuid and index are empty/nil, returns empty string (query all GPUs).
func (h *GPUTimelineHandler) resolveGPUUUID(
	ctx context.Context,
	uuid string,
	index *int,
) (string, error) {
	// If UUID provided, verify it exists
	if uuid != "" {
		trackedGPUs := h.recorder.TrackedGPUs()
		for _, tracked := range trackedGPUs {
			if tracked == uuid {
				return uuid, nil
			}
		}
		return "", fmt.Errorf("GPU not found: %s", uuid)
	}

	// If index provided, resolve via NVML enumeration
	if index != nil {
		if h.nvmlClient == nil {
			return "", fmt.Errorf("GPU index lookup requires NVML client, use gpu_uuid instead")
		}

		count, err := h.nvmlClient.GetDeviceCount(ctx)
		if err != nil {
			return "", fmt.Errorf("failed to get device count: %w", err)
		}

		if *index >= count {
			return "", fmt.Errorf("GPU index out of range: %d (have %d GPUs)", *index, count)
		}

		dev, err := h.nvmlClient.GetDeviceByIndex(ctx, *index)
		if err != nil {
			return "", fmt.Errorf("failed to get device at index %d: %w", *index, err)
		}

		resolvedUUID, err := dev.GetUUID(ctx)
		if err != nil {
			return "", fmt.Errorf("failed to get UUID for GPU index %d: %w", *index, err)
		}

		return resolvedUUID, nil
	}

	// Neither provided: query all GPUs
	return "", nil
}

// handleSingleGPU handles timeline request for a single GPU.
func (h *GPUTimelineHandler) handleSingleGPU(
	ctx context.Context,
	gpuUUID string,
	args *timelineArgs,
) (*mcp.CallToolResult, error) {
	snapshots, err := h.recorder.GetTimeline(gpuUUID, args.duration)
	if err != nil {
		return mcp.NewToolResultError(
			fmt.Sprintf("failed to get timeline: %v", err)), nil
	}

	response := h.buildResponse(ctx, gpuUUID, snapshots, args)

	// Add events if XID watcher is available
	since := time.Now().Add(-args.duration)
	response.Events = h.extractEvents(snapshots, gpuUUID, since)

	return h.marshalResponse(response)
}

// handleAllGPUs handles timeline request for all GPUs.
func (h *GPUTimelineHandler) handleAllGPUs(
	ctx context.Context,
	args *timelineArgs,
) (*mcp.CallToolResult, error) {
	allTimelines := h.recorder.GetAllTimelines(args.duration)

	if len(allTimelines) == 0 {
		return mcp.NewToolResultError("no GPU data available"), nil
	}

	multiResp := MultiGPUTimelineResponse{
		APIVersion: APIVersion,
		GPUCount:   len(allTimelines),
		Duration:   args.duration.String(),
		Timelines: make([]GPUTimelineResponse, 0, len(allTimelines)),
	}

	since := time.Now().Add(-args.duration)

	for uuid, snapshots := range allTimelines {
		// Check for context cancellation
		if err := ctx.Err(); err != nil {
			return mcp.NewToolResultError(
				fmt.Sprintf("operation cancelled: %s", err)), nil
		}

		resp := h.buildResponse(ctx, uuid, snapshots, args)
		resp.Events = h.extractEvents(snapshots, uuid, since)
		multiResp.Timelines = append(multiResp.Timelines, resp)
	}

	// Sort by GPU index for consistent output
	sort.Slice(multiResp.Timelines, func(i, j int) bool {
		return multiResp.Timelines[i].GPUIndex < multiResp.Timelines[j].GPUIndex
	})

	return h.marshalMultiResponse(multiResp)
}

// buildResponse constructs a GPUTimelineResponse from snapshots.
func (h *GPUTimelineHandler) buildResponse(
	ctx context.Context,
	gpuUUID string,
	snapshots []blackbox.GPUSnapshot,
	args *timelineArgs,
) GPUTimelineResponse {
	resp := GPUTimelineResponse{
		APIVersion:  APIVersion,
		GPUUUID:     gpuUUID,
		Duration:    args.duration.String(),
		SampleCount: len(snapshots),
		DataPoints:  make([]DataPoint, 0, len(snapshots)),
	}

	// Extract index and compute sample interval from snapshots
	if len(snapshots) > 0 {
		resp.GPUIndex = snapshots[0].Index

		// Compute sample interval
		if len(snapshots) >= 2 {
			interval := snapshots[1].Timestamp.Sub(snapshots[0].Timestamp)
			resp.SampleInterval = interval.String()
		}
	}

	// Try to get GPU name via NVML
	if h.nvmlClient != nil {
		nvmlCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if dev, err := h.nvmlClient.GetDeviceByIndex(nvmlCtx, resp.GPUIndex); err == nil {
			if name, err := dev.GetName(nvmlCtx); err == nil {
				resp.GPUName = name
			}
		}
	}

	// Convert snapshots to data points
	for _, snap := range snapshots {
		dp := DataPoint{
			Timestamp:          snap.Timestamp.Format(time.RFC3339),
			TemperatureCelsius: snap.Temperature,
			PowerMW:            snap.PowerMW,
			GPUUtilPercent:     snap.GPUUtil,
			MemUtilPercent:     snap.MemUtil,
			MemUsedBytes:       snap.MemUsed,
			MemTotalBytes:      snap.MemTotal,
			Throttling:         snap.IsThrottled(),
		}
		resp.DataPoints = append(resp.DataPoints, dp)
	}

	// Compute statistics if requested
	if args.includeStats && len(snapshots) > 0 {
		resp.Statistics = computeStatistics(snapshots)
	}

	return resp
}

// computeStatistics calculates min/max/avg for key metrics.
func computeStatistics(snapshots []blackbox.GPUSnapshot) *TimelineStats {
	if len(snapshots) == 0 {
		return nil
	}

	stats := &TimelineStats{}
	n := float64(len(snapshots))

	// Initialize with first snapshot values
	first := snapshots[0]
	stats.Temperature = StatValues{Min: first.Temperature, Max: first.Temperature}
	stats.PowerMW = StatValues{Min: first.PowerMW, Max: first.PowerMW}
	stats.GPUUtil = StatValues{Min: first.GPUUtil, Max: first.GPUUtil}
	stats.MemUtil = StatValues{Min: first.MemUtil, Max: first.MemUtil}

	var tempSum, powerSum, gpuUtilSum, memUtilSum float64

	for _, snap := range snapshots {
		// Temperature
		if snap.Temperature < stats.Temperature.Min {
			stats.Temperature.Min = snap.Temperature
		}
		if snap.Temperature > stats.Temperature.Max {
			stats.Temperature.Max = snap.Temperature
		}
		tempSum += float64(snap.Temperature)

		// Power
		if snap.PowerMW < stats.PowerMW.Min {
			stats.PowerMW.Min = snap.PowerMW
		}
		if snap.PowerMW > stats.PowerMW.Max {
			stats.PowerMW.Max = snap.PowerMW
		}
		powerSum += float64(snap.PowerMW)

		// GPU Utilization
		if snap.GPUUtil < stats.GPUUtil.Min {
			stats.GPUUtil.Min = snap.GPUUtil
		}
		if snap.GPUUtil > stats.GPUUtil.Max {
			stats.GPUUtil.Max = snap.GPUUtil
		}
		gpuUtilSum += float64(snap.GPUUtil)

		// Memory Utilization
		if snap.MemUtil < stats.MemUtil.Min {
			stats.MemUtil.Min = snap.MemUtil
		}
		if snap.MemUtil > stats.MemUtil.Max {
			stats.MemUtil.Max = snap.MemUtil
		}
		memUtilSum += float64(snap.MemUtil)
	}

	// Compute averages
	stats.Temperature.Avg = tempSum / n
	stats.PowerMW.Avg = powerSum / n
	stats.GPUUtil.Avg = gpuUtilSum / n
	stats.MemUtil.Avg = memUtilSum / n

	return stats
}

// extractEvents collects XID and throttling events from the time window.
func (h *GPUTimelineHandler) extractEvents(
	snapshots []blackbox.GPUSnapshot,
	gpuUUID string,
	since time.Time,
) []TimelineEvent {
	var events []TimelineEvent

	// Extract XID events from watcher (if available)
	if h.xidWatcher != nil {
		xidEvents := h.xidWatcher.GetEvents(since)
		for _, xe := range xidEvents {
			// Filter by GPU if UUID is set in the event
			if xe.GPUUUID != "" && xe.GPUUUID != gpuUUID {
				continue
			}
			events = append(events, TimelineEvent{
				Timestamp:   xe.Timestamp.Format(time.RFC3339),
				Type:        "xid",
				Code:        xe.XIDCode,
				Description: xe.Description,
				Severity:    xe.Severity,
			})
		}
	}

	// Detect throttling state transitions from snapshots
	events = append(events, detectThrottlingTransitions(snapshots)...)

	// Sort events chronologically
	sort.Slice(events, func(i, j int) bool {
		return events[i].Timestamp < events[j].Timestamp
	})

	return events
}

// detectThrottlingTransitions finds throttling state changes in snapshots.
func detectThrottlingTransitions(snapshots []blackbox.GPUSnapshot) []TimelineEvent {
	var events []TimelineEvent

	if len(snapshots) < 2 {
		return events
	}

	prevThrottled := snapshots[0].IsThrottled()

	for i := 1; i < len(snapshots); i++ {
		currThrottled := snapshots[i].IsThrottled()

		if prevThrottled != currThrottled {
			eventType := "throttle_end"
			desc := "GPU throttling ended"
			if currThrottled {
				eventType = "throttle_start"
				desc = "GPU throttling started"
			}
			events = append(events, TimelineEvent{
				Timestamp:   snapshots[i].Timestamp.Format(time.RFC3339),
				Type:        eventType,
				Description: desc,
			})
		}
		prevThrottled = currThrottled
	}

	return events
}

// marshalResponse marshals a single GPU response.
func (h *GPUTimelineHandler) marshalResponse(
	response GPUTimelineResponse,
) (*mcp.CallToolResult, error) {
	jsonBytes, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		klog.ErrorS(err, "failed to marshal response")
		return mcp.NewToolResultError(
			fmt.Sprintf("failed to marshal response: %s", err)), nil
	}
	return mcp.NewToolResultText(string(jsonBytes)), nil
}

// marshalMultiResponse marshals a multi-GPU response.
func (h *GPUTimelineHandler) marshalMultiResponse(
	response MultiGPUTimelineResponse,
) (*mcp.CallToolResult, error) {
	jsonBytes, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		klog.ErrorS(err, "failed to marshal response")
		return mcp.NewToolResultError(
			fmt.Sprintf("failed to marshal response: %s", err)), nil
	}
	return mcp.NewToolResultText(string(jsonBytes)), nil
}

// GetGPUTimelineTool returns the MCP tool definition.
func GetGPUTimelineTool() mcp.Tool {
	return mcp.NewTool("get_gpu_timeline",
		mcp.WithDescription(
			"Get historical GPU metrics for a specific time window. "+
				"Returns time-series data for temperature, power, utilization, "+
				"memory, and other metrics from the flight recorder. "+
				"Use this to answer questions like 'What was the GPU temperature 10 minutes ago?' "+
				"or 'Show me the power usage trend over the last hour.'",
		),
		mcp.WithString("gpu_uuid",
			mcp.Description("GPU UUID to query (optional, defaults to all GPUs)"),
		),
		mcp.WithNumber("gpu_index",
			mcp.Description("GPU device index (optional, alternative to gpu_uuid)"),
		),
		mcp.WithString("duration",
			mcp.Description("Time window to query (default: 30m). Examples: 10m, 1h, 2h30m"),
		),
		mcp.WithBoolean("include_stats",
			mcp.Description("Include min/max/avg statistics (default: true)"),
		),
	)
}
