// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ArangoGutierrez/k8s-gpu-mcp-server/pkg/nvml"
	"github.com/mark3labs/mcp-go/mcp"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

// DescribeGPUNodeHandler handles the describe_gpu_node tool.
type DescribeGPUNodeHandler struct {
	clientset  kubernetes.Interface
	nvmlClient nvml.Interface
}

// NewDescribeGPUNodeHandler creates a new describe GPU node handler.
func NewDescribeGPUNodeHandler(
	clientset kubernetes.Interface,
	nvmlClient nvml.Interface,
) *DescribeGPUNodeHandler {
	return &DescribeGPUNodeHandler{
		clientset:  clientset,
		nvmlClient: nvmlClient,
	}
}

// GPUNodeDescription represents the full node description.
// This struct is safe for concurrent read access; writes are confined to the
// handler goroutine that creates it.
type GPUNodeDescription struct {
	APIVersion string           `json:"api_version"`
	Status     string           `json:"status"`
	Node    NodeInfo         `json:"node"`
	Driver  DriverInfo       `json:"driver,omitempty"`
	GPUs    []GPUDescription `json:"gpus,omitempty"`
	Pods    []PodGPUSummary  `json:"pods"`
	Summary GPUNodeSummary   `json:"summary"`
}

// NodeInfoPartial is used when K8s API access fails but NVML data is available.
type NodeInfoPartial struct {
	Name           string `json:"name"`
	K8sUnavailable bool   `json:"k8s_unavailable,omitempty"`
	K8sError       string `json:"k8s_error,omitempty"`
}

// NodeInfo contains Kubernetes node metadata.
type NodeInfo struct {
	Name        string            `json:"name"`
	Labels      map[string]string `json:"labels"`
	Taints      []TaintInfo       `json:"taints,omitempty"`
	Conditions  map[string]bool   `json:"conditions"`
	Capacity    ResourceInfo      `json:"capacity"`
	Allocatable ResourceInfo      `json:"allocatable"`
}

// TaintInfo represents a node taint.
type TaintInfo struct {
	Key    string `json:"key"`
	Value  string `json:"value,omitempty"`
	Effect string `json:"effect"`
}

// ResourceInfo contains resource capacity information.
type ResourceInfo struct {
	CPU       string `json:"cpu"`
	Memory    string `json:"memory"`
	NvidiaGPU string `json:"nvidia.com/gpu,omitempty"`
}

// DriverInfo contains GPU driver information.
type DriverInfo struct {
	Version     string `json:"version,omitempty"`
	CudaVersion string `json:"cuda_version,omitempty"`
}

// GPUDescription represents GPU hardware details.
type GPUDescription struct {
	Index             int    `json:"index"`
	Name              string `json:"name"`
	UUID              string `json:"uuid"`
	HealthScore       int    `json:"health_score"`
	Temperature       uint32 `json:"temperature"`
	Utilization       uint32 `json:"utilization"`
	MemoryUsedPercent int    `json:"memory_used_percent"`
}

// PodGPUSummary is a simplified pod GPU summary for the node description.
type PodGPUSummary struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	GPUCount  int64  `json:"gpu_count"`
	Status    string `json:"status"`
}

// GPUNodeSummary provides summary statistics for the GPU node.
type GPUNodeSummary struct {
	TotalGPUs     int    `json:"total_gpus"`
	AllocatedGPUs int64  `json:"allocated_gpus"`
	AvailableGPUs int64  `json:"available_gpus"`
	OverallHealth string `json:"overall_health"`
}

// gpuLabelPrefixes are the prefixes for GPU-related labels.
var gpuLabelPrefixes = []string{
	"nvidia.com/",
	"feature.node.kubernetes.io/pci-",
	"node.kubernetes.io/instance-type",
	"kubernetes.io/arch",
}

// Health score calculation constants.
const (
	// tempThresholdWarning is the temperature (Celsius) above which
	// health score starts decreasing.
	tempThresholdWarning = 80
	// tempPenaltyMultiplier is multiplied by degrees above threshold.
	tempPenaltyMultiplier = 2
	// maxTempPenalty is the maximum penalty for high temperature.
	maxTempPenalty = 30
	// memoryPressureThreshold is the memory usage percent above which
	// health score is penalized.
	memoryPressureThreshold = 90
	// memoryPressurePenalty is the health score penalty for high memory.
	memoryPressurePenalty = 10
	// eccErrorPenalty is the health score penalty for uncorrectable ECC errors.
	eccErrorPenalty = 20
)

// Overall health classification thresholds.
const (
	// healthyThreshold is the minimum average score for "healthy" status.
	healthyThreshold = 90
	// degradedThreshold is the minimum average score for "degraded" status.
	// Below this threshold is considered "critical".
	degradedThreshold = 70
)

// Handle processes the describe_gpu_node tool request.
func (h *DescribeGPUNodeHandler) Handle(
	ctx context.Context,
	request mcp.CallToolRequest,
) (*mcp.CallToolResult, error) {
	klog.InfoS("describe_gpu_node invoked")

	args := request.GetArguments()

	// Extract node_name (required)
	nodeName, ok := args["node_name"].(string)
	if !ok || nodeName == "" {
		return mcp.NewToolResultError("node_name is required"), nil
	}

	// Validate node name format
	if !isValidNodeName(nodeName) {
		return mcp.NewToolResultError(
			"invalid node_name: must be a valid DNS subdomain (RFC 1123)"), nil
	}

	klog.V(4).InfoS("describing node", "node", nodeName)

	// Get GPU hardware info first (NVML data is always available locally)
	var driverInfo DriverInfo
	var gpus []GPUDescription
	if h.nvmlClient != nil {
		driverInfo, gpus = h.collectGPUInfo(ctx)
	}

	// Try to get Kubernetes node info, but don't fail if RBAC denies access
	var nodeInfo NodeInfo
	var k8sError string
	var node *corev1.Node

	if h.clientset != nil {
		var err error
		node, err = h.clientset.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
		if err != nil {
			klog.V(2).InfoS("K8s node access unavailable",
				"node", nodeName,
				"error", err,
				"hint", "Agent may lack RBAC permissions, returning NVML-only data")
			k8sError = err.Error()
			// Continue without K8s metadata - provide partial node info
			nodeInfo = NodeInfo{
				Name:        nodeName,
				Labels:      make(map[string]string),
				Conditions:  make(map[string]bool),
				Capacity:    ResourceInfo{},
				Allocatable: ResourceInfo{},
			}
		} else {
			nodeInfo = h.extractNodeInfo(node)
		}
	} else {
		// No clientset available
		nodeInfo = NodeInfo{
			Name:        nodeName,
			Labels:      make(map[string]string),
			Conditions:  make(map[string]bool),
			Capacity:    ResourceInfo{},
			Allocatable: ResourceInfo{},
		}
		k8sError = "K8s client not configured"
	}

	// Get pods with GPU allocations on this node (graceful on failure)
	var pods []PodGPUSummary
	var allocatedGPUs int64
	if h.clientset != nil {
		var err error
		pods, allocatedGPUs, err = h.getPodsSummary(ctx, nodeName)
		if err != nil {
			klog.V(2).InfoS("failed to get pods summary", "node", nodeName, "error", err)
			// Context cancellation is fatal
			if ctx.Err() != nil {
				return mcp.NewToolResultError(
					fmt.Sprintf("operation cancelled: %s", err)), nil
			}
			// RBAC errors are non-fatal, continue with empty pods list
			pods = []PodGPUSummary{}
		}
	}

	// Calculate summary
	totalGPUs := len(gpus)
	if totalGPUs == 0 && node != nil {
		// Fall back to K8s capacity if no NVML data
		if gpuCap, ok := node.Status.Capacity[nvidiaGPUResource]; ok {
			totalGPUs = int(gpuCap.Value())
		}
	}

	summary := GPUNodeSummary{
		TotalGPUs:     totalGPUs,
		AllocatedGPUs: allocatedGPUs,
		AvailableGPUs: int64(totalGPUs) - allocatedGPUs,
		OverallHealth: h.calculateOverallHealth(gpus),
	}

	// Determine status based on data availability
	status := "success"
	if k8sError != "" {
		status = "partial"
	}

	// Create response
	response := GPUNodeDescription{
		APIVersion: APIVersion,
		Status:     status,
		Node:       nodeInfo,
		Driver:  driverInfo,
		GPUs:    gpus,
		Pods:    pods,
		Summary: summary,
	}

	// Marshal to JSON
	jsonBytes, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		klog.ErrorS(err, "failed to marshal response")
		return mcp.NewToolResultError(
			fmt.Sprintf("failed to marshal response: %s", err)), nil
	}

	klog.InfoS("describe_gpu_node completed",
		"node", nodeName, "gpus", totalGPUs, "pods", len(pods))

	return mcp.NewToolResultText(string(jsonBytes)), nil
}

// extractNodeInfo extracts relevant node information.
func (h *DescribeGPUNodeHandler) extractNodeInfo(node *corev1.Node) NodeInfo {
	// Filter GPU-related labels
	gpuLabels := make(map[string]string)
	for key, value := range node.Labels {
		for _, prefix := range gpuLabelPrefixes {
			if strings.HasPrefix(key, prefix) {
				gpuLabels[key] = value
				break
			}
		}
	}

	// Extract taints
	taints := make([]TaintInfo, 0, len(node.Spec.Taints))
	for _, taint := range node.Spec.Taints {
		taints = append(taints, TaintInfo{
			Key:    taint.Key,
			Value:  taint.Value,
			Effect: string(taint.Effect),
		})
	}

	// Extract conditions as map
	conditions := make(map[string]bool)
	for _, cond := range node.Status.Conditions {
		conditions[string(cond.Type)] = cond.Status == corev1.ConditionTrue
	}

	// Extract capacity and allocatable
	capacity := ResourceInfo{
		CPU:    node.Status.Capacity.Cpu().String(),
		Memory: node.Status.Capacity.Memory().String(),
	}
	if gpuCap, ok := node.Status.Capacity[nvidiaGPUResource]; ok {
		capacity.NvidiaGPU = gpuCap.String()
	}

	allocatable := ResourceInfo{
		CPU:    node.Status.Allocatable.Cpu().String(),
		Memory: node.Status.Allocatable.Memory().String(),
	}
	if gpuAlloc, ok := node.Status.Allocatable[nvidiaGPUResource]; ok {
		allocatable.NvidiaGPU = gpuAlloc.String()
	}

	return NodeInfo{
		Name:        node.Name,
		Labels:      gpuLabels,
		Taints:      taints,
		Conditions:  conditions,
		Capacity:    capacity,
		Allocatable: allocatable,
	}
}

// collectGPUInfo gathers GPU hardware information via NVML.
func (h *DescribeGPUNodeHandler) collectGPUInfo(
	ctx context.Context,
) (DriverInfo, []GPUDescription) {
	var driverInfo DriverInfo
	var gpus []GPUDescription

	// Get driver info
	if ver, err := h.nvmlClient.GetDriverVersion(ctx); err == nil {
		driverInfo.Version = ver
	}
	if ver, err := h.nvmlClient.GetCudaDriverVersion(ctx); err == nil {
		driverInfo.CudaVersion = ver
	}

	// Get device count
	count, err := h.nvmlClient.GetDeviceCount(ctx)
	if err != nil {
		klog.V(2).InfoS("failed to get device count", "error", err)
		return driverInfo, gpus
	}

	gpus = make([]GPUDescription, 0, count)
	for i := 0; i < count; i++ {
		// Check for context cancellation
		select {
		case <-ctx.Done():
			klog.InfoS("context cancelled during GPU enumeration")
			return driverInfo, gpus
		default:
		}

		device, err := h.nvmlClient.GetDeviceByIndex(ctx, i)
		if err != nil {
			klog.V(2).InfoS("failed to get device", "index", i, "error", err)
			continue
		}

		gpuDesc := GPUDescription{
			Index: i,
		}

		if name, err := device.GetName(ctx); err == nil {
			gpuDesc.Name = name
		}
		if uuid, err := device.GetUUID(ctx); err == nil {
			gpuDesc.UUID = uuid
		}
		if temp, err := device.GetTemperature(ctx); err == nil {
			gpuDesc.Temperature = temp
		}
		if util, err := device.GetUtilizationRates(ctx); err == nil {
			gpuDesc.Utilization = util.GPU
		}
		if mem, err := device.GetMemoryInfo(ctx); err == nil && mem.Total > 0 {
			gpuDesc.MemoryUsedPercent = int(
				(float64(mem.Used) / float64(mem.Total)) * 100)
		}

		// Calculate health score
		gpuDesc.HealthScore = h.calculateGPUHealthScore(ctx, device, gpuDesc)

		gpus = append(gpus, gpuDesc)
	}

	return driverInfo, gpus
}

// calculateGPUHealthScore computes a health score for a GPU (0-100).
func (h *DescribeGPUNodeHandler) calculateGPUHealthScore(
	ctx context.Context,
	device nvml.Device,
	desc GPUDescription,
) int {
	score := 100

	// Temperature penalty (above threshold starts reducing score)
	if desc.Temperature > tempThresholdWarning {
		penalty := int((desc.Temperature - tempThresholdWarning) *
			tempPenaltyMultiplier)
		if penalty > maxTempPenalty {
			penalty = maxTempPenalty
		}
		score -= penalty
	}

	// Memory pressure penalty
	if desc.MemoryUsedPercent > memoryPressureThreshold {
		score -= memoryPressurePenalty
	}

	// ECC error penalty
	if enabled, _, err := device.GetEccMode(ctx); err == nil && enabled {
		if uncorrectable, err := device.GetTotalEccErrors(
			ctx, nvml.EccErrorUncorrectable); err == nil && uncorrectable > 0 {
			score -= eccErrorPenalty
		}
	}

	if score < 0 {
		score = 0
	}
	return score
}

// getPodsSummary gets pods with GPU allocations on the node.
// Returns an error if the context is cancelled during enumeration.
func (h *DescribeGPUNodeHandler) getPodsSummary(
	ctx context.Context,
	nodeName string,
) ([]PodGPUSummary, int64, error) {
	pods := make([]PodGPUSummary, 0)
	var totalGPUs int64

	// List pods on the node
	podList, err := h.clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{
		FieldSelector: fmt.Sprintf("spec.nodeName=%s", nodeName),
	})
	if err != nil {
		klog.V(2).InfoS("failed to list pods", "node", nodeName, "error", err)
		return pods, totalGPUs, nil // Non-fatal, return empty list
	}

	for _, pod := range podList.Items {
		// Check for context cancellation
		select {
		case <-ctx.Done():
			klog.InfoS("context cancelled during pod summary enumeration")
			return pods, totalGPUs, ctx.Err()
		default:
		}

		// Client-side node filter (FieldSelector backup for fake clients)
		if pod.Spec.NodeName != nodeName {
			continue
		}

		var gpuCount int64
		for _, container := range pod.Spec.Containers {
			if req, ok := container.Resources.Requests[nvidiaGPUResource]; ok {
				gpuCount += req.Value()
			}
		}

		if gpuCount > 0 {
			pods = append(pods, PodGPUSummary{
				Name:      pod.Name,
				Namespace: pod.Namespace,
				GPUCount:  gpuCount,
				Status:    string(pod.Status.Phase),
			})
			totalGPUs += gpuCount
		}
	}

	return pods, totalGPUs, nil
}

// calculateOverallHealth determines the overall health status.
func (h *DescribeGPUNodeHandler) calculateOverallHealth(
	gpus []GPUDescription,
) string {
	if len(gpus) == 0 {
		return "unknown"
	}

	totalScore := 0
	for _, gpu := range gpus {
		totalScore += gpu.HealthScore
	}
	avgScore := totalScore / len(gpus)

	switch {
	case avgScore >= healthyThreshold:
		return "healthy"
	case avgScore >= degradedThreshold:
		return "degraded"
	default:
		return "critical"
	}
}

// GetDescribeGPUNodeTool returns the MCP tool definition.
func GetDescribeGPUNodeTool() mcp.Tool {
	return mcp.NewTool("describe_gpu_node",
		mcp.WithDescription(
			"Comprehensive view of a GPU node combining Kubernetes metadata "+
				"with NVML hardware data. Includes node labels, taints, "+
				"conditions, capacity, GPU health status, and pods running "+
				"on the node.",
		),
		mcp.WithString("node_name",
			mcp.Required(),
			mcp.Description("Node name to describe"),
		),
	)
}
