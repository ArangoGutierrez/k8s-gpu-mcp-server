// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

package events

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/ArangoGutierrez/k8s-gpu-mcp-server/pkg/blackbox"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// Default configuration values.
const (
	// DefaultStaleTTL is how long to retain cached data for dead processes.
	DefaultStaleTTL = 5 * time.Minute

	// DefaultCacheSize is the maximum number of cached entries.
	DefaultCacheSize = 1000

	// DefaultRefreshInterval is how often to refresh Pod cache.
	DefaultRefreshInterval = 30 * time.Second
)

// PodInfo is an alias for blackbox.PodInfo for process-to-pod mapping.
type PodInfo = blackbox.PodInfo

// cachedPodInfo wraps PodInfo with cache metadata.
type cachedPodInfo struct {
	Info        *blackbox.PodInfo
	CachedAt    time.Time
	LastChecked time.Time
}

// ProcessMapper resolves GPU process PIDs to Kubernetes Pod information.
// It parses /proc/<pid>/cgroup to extract container IDs and maps them
// to Pod metadata via the Kubernetes API.
//
// The mapper maintains an in-memory cache with configurable TTL for:
//   - Fast repeated lookups during snapshot capture
//   - Post-mortem analysis of dead processes
//
// Thread-safe for concurrent use.
type ProcessMapper struct {
	// Configuration
	client   kubernetes.Interface
	nodeName string
	procPath string // Default: "/proc", configurable for testing
	staleTTL time.Duration
	maxCache int
	logger   *slog.Logger

	// State: PID -> cached PodInfo
	cache map[int]*cachedPodInfo
	mu    sync.RWMutex

	// Pod cache: containerID -> PodInfo (refreshed periodically)
	podCache   map[string]*blackbox.PodInfo
	podCacheMu sync.RWMutex
}

// MapperOption configures a ProcessMapper.
type MapperOption func(*ProcessMapper)

// WithProcPath sets the /proc filesystem path (for testing).
func WithProcPath(path string) MapperOption {
	return func(m *ProcessMapper) {
		m.procPath = path
	}
}

// WithStaleTTL sets how long to retain cached data for dead processes.
func WithStaleTTL(ttl time.Duration) MapperOption {
	return func(m *ProcessMapper) {
		m.staleTTL = ttl
	}
}

// WithMaxCacheSize sets the maximum cache size.
func WithMaxCacheSize(size int) MapperOption {
	return func(m *ProcessMapper) {
		if size > 0 {
			m.maxCache = size
		}
	}
}

// WithMapperLogger sets the logger.
func WithMapperLogger(logger *slog.Logger) MapperOption {
	return func(m *ProcessMapper) {
		if logger != nil {
			m.logger = logger
		}
	}
}

// NewProcessMapper creates a new ProcessMapper.
//
// Parameters:
//   - client: Kubernetes clientset for Pod lookups
//   - nodeName: Name of the current node (for filtering Pods)
//   - opts: Optional configuration
//
// Compile-time check that ProcessMapper satisfies blackbox.ProcessResolver.
var _ blackbox.ProcessResolver = (*ProcessMapper)(nil)

func NewProcessMapper(
	client kubernetes.Interface,
	nodeName string,
	opts ...MapperOption,
) *ProcessMapper {
	m := &ProcessMapper{
		client:   client,
		nodeName: nodeName,
		procPath: "/proc",
		staleTTL: DefaultStaleTTL,
		maxCache: DefaultCacheSize,
		logger:   slog.Default(),
		cache:    make(map[int]*cachedPodInfo),
		podCache: make(map[string]*blackbox.PodInfo),
	}

	for _, opt := range opts {
		opt(m)
	}

	return m
}

// GetPodForPID resolves a process ID to its Kubernetes Pod information.
//
// Resolution strategy:
//  1. Check cache for existing mapping
//  2. If cache miss or stale, parse /proc/<pid>/cgroup
//  3. Extract container ID from cgroup path
//  4. Map container ID to Pod via K8s API
//
// Returns nil if:
//   - Process is not in a container (host process)
//   - Process no longer exists and not in cache
//   - Container ID cannot be mapped to a Pod
func (m *ProcessMapper) GetPodForPID(ctx context.Context, pid int) (*blackbox.PodInfo, error) {
	if pid <= 0 {
		return nil, fmt.Errorf("%w: pid must be positive", ErrInvalidPID)
	}

	// Check cache first
	m.mu.RLock()
	cached, ok := m.cache[pid]
	m.mu.RUnlock()

	now := time.Now()
	if ok && cached.Info != nil {
		// Return cached result if still valid
		if now.Sub(cached.CachedAt) < m.staleTTL {
			return cached.Info, nil
		}
	}

	// Try to resolve via cgroup
	containerID, err := m.parseCgroup(pid)
	if err != nil {
		// Process may have died - return cached data if available
		if ok && cached.Info != nil {
			m.logger.Debug("returning stale cache for dead process",
				"pid", pid, "pod", cached.Info.PodName)
			return cached.Info, nil
		}
		// Not in cache and can't read cgroup
		if os.IsNotExist(err) {
			return nil, nil // Process doesn't exist
		}
		return nil, fmt.Errorf("parse cgroup: %w", err)
	}

	// Empty container ID means not a containerized process
	if containerID == "" {
		return nil, nil
	}

	// Look up Pod by container ID
	podInfo, err := m.lookupPodByContainerID(ctx, containerID)
	if err != nil {
		m.logger.Debug("failed to lookup pod",
			"pid", pid, "containerID", containerID, "error", err)
		// Return cached data if available
		if ok && cached.Info != nil {
			return cached.Info, nil
		}
		return nil, err
	}

	if podInfo == nil {
		// Container not found in any Pod - might be non-K8s container
		return nil, nil
	}

	// Update cache
	m.mu.Lock()
	m.cache[pid] = &cachedPodInfo{
		Info:        podInfo,
		CachedAt:    now,
		LastChecked: now,
	}
	// Evict oldest entries if over capacity
	if len(m.cache) > m.maxCache {
		m.evictOldest()
	}
	m.mu.Unlock()

	return podInfo, nil
}

// RefreshPodCache refreshes the container ID to Pod mapping cache.
// Call this periodically to ensure fresh mappings are available.
func (m *ProcessMapper) RefreshPodCache(ctx context.Context) error {
	if m.client == nil {
		return nil // No K8s client configured
	}

	// List all pods on this node
	pods, err := m.client.CoreV1().Pods("").List(ctx, metav1.ListOptions{
		FieldSelector: fmt.Sprintf("spec.nodeName=%s", m.nodeName),
	})
	if err != nil {
		return fmt.Errorf("list pods: %w", err)
	}

	newCache := make(map[string]*blackbox.PodInfo)

	for i := range pods.Items {
		pod := &pods.Items[i]
		for _, cs := range pod.Status.ContainerStatuses {
			containerID := extractContainerID(cs.ContainerID)
			if containerID == "" {
				continue
			}
			newCache[containerID] = &blackbox.PodInfo{
				PodUID:        string(pod.UID),
				PodName:       pod.Name,
				Namespace:     pod.Namespace,
				ContainerID:   containerID,
				ContainerName: cs.Name,
			}
		}
	}

	m.podCacheMu.Lock()
	m.podCache = newCache
	m.podCacheMu.Unlock()

	m.logger.Debug("refreshed pod cache",
		"node", m.nodeName, "containers", len(newCache))

	return nil
}

// CacheSize returns the current number of PID mappings cached.
func (m *ProcessMapper) CacheSize() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.cache)
}

// PodCacheSize returns the current number of container-to-pod mappings.
func (m *ProcessMapper) PodCacheSize() int {
	m.podCacheMu.RLock()
	defer m.podCacheMu.RUnlock()
	return len(m.podCache)
}

// ClearCache removes all cached entries.
func (m *ProcessMapper) ClearCache() {
	m.mu.Lock()
	m.cache = make(map[int]*cachedPodInfo)
	m.mu.Unlock()
}

// cgroupPatterns for extracting container IDs from /proc/<pid>/cgroup.
// These patterns support various container runtimes and cgroup versions.
var cgroupPatterns = []*regexp.Regexp{
	// cgroup v1: CRI-O format
	// /kubepods/burstable/pod<uid>/crio-<container-id>
	regexp.MustCompile(`/pod[a-f0-9-]+/crio-([a-f0-9]{64})`),

	// cgroup v1: containerd format
	// /kubepods/burstable/pod<uid>/<container-id>
	regexp.MustCompile(`/pod[a-f0-9-]+/([a-f0-9]{64})$`),

	// cgroup v2: containerd scope format
	// cri-containerd-<container-id>.scope
	regexp.MustCompile(`cri-containerd-([a-f0-9]{64})\.scope`),

	// cgroup v2: CRI-O scope format
	// crio-<container-id>.scope
	regexp.MustCompile(`crio-([a-f0-9]{64})\.scope`),

	// cgroup v2: Docker scope format
	// docker-<container-id>.scope
	regexp.MustCompile(`docker-([a-f0-9]{64})\.scope`),

	// Generic: any 64-char hex ID at end of path (fallback)
	regexp.MustCompile(`[/-]([a-f0-9]{64})(?:\.scope)?$`),
}

// parseCgroup reads /proc/<pid>/cgroup and extracts the container ID.
// Returns empty string for non-containerized processes.
func (m *ProcessMapper) parseCgroup(pid int) (string, error) {
	cgroupPath := filepath.Join(m.procPath, fmt.Sprintf("%d", pid), "cgroup")

	// Security: Validate the path doesn't escape /proc
	cleanPath := filepath.Clean(cgroupPath)
	if !strings.HasPrefix(cleanPath, filepath.Clean(m.procPath)) {
		return "", fmt.Errorf("%w: path traversal attempt", ErrInvalidPID)
	}

	file, err := os.Open(cleanPath)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		// cgroup v2 unified hierarchy: "0::/..."
		// cgroup v1: "N:subsystem:/..."
		if containerID := extractContainerIDFromCgroup(line); containerID != "" {
			return containerID, nil
		}
	}

	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("scan cgroup: %w", err)
	}

	return "", nil // Not a containerized process
}

// extractContainerIDFromCgroup extracts container ID from a cgroup line.
func extractContainerIDFromCgroup(line string) string {
	// Skip non-kubepods hierarchies for cgroup v1
	// For cgroup v2, the line starts with "0::"
	if !strings.Contains(line, "kubepods") &&
		!strings.HasPrefix(line, "0::") {
		return ""
	}

	for _, pattern := range cgroupPatterns {
		if matches := pattern.FindStringSubmatch(line); len(matches) >= 2 {
			return matches[1]
		}
	}
	return ""
}

// lookupPodByContainerID finds the Pod containing the given container ID.
func (m *ProcessMapper) lookupPodByContainerID(
	ctx context.Context,
	containerID string,
) (*blackbox.PodInfo, error) {
	// Check pod cache first
	m.podCacheMu.RLock()
	if info, ok := m.podCache[containerID]; ok {
		m.podCacheMu.RUnlock()
		return info, nil
	}
	m.podCacheMu.RUnlock()

	// Cache miss - refresh and try again
	if err := m.RefreshPodCache(ctx); err != nil {
		return nil, err
	}

	m.podCacheMu.RLock()
	defer m.podCacheMu.RUnlock()
	return m.podCache[containerID], nil
}

// extractContainerID extracts the container ID from a K8s container status.
// Format: "containerd://<id>" or "docker://<id>" or "cri-o://<id>"
func extractContainerID(fullID string) string {
	if fullID == "" {
		return ""
	}
	// Remove runtime prefix (e.g., "containerd://", "docker://", "cri-o://")
	if idx := strings.Index(fullID, "://"); idx >= 0 {
		return fullID[idx+3:]
	}
	return fullID
}

// evictOldest removes the oldest 10% of cache entries.
// Must be called with m.mu held.
func (m *ProcessMapper) evictOldest() {
	if len(m.cache) == 0 {
		return
	}

	// Find entries to evict (oldest 10%)
	toEvict := len(m.cache) / 10
	if toEvict < 1 {
		toEvict = 1
	}

	type entry struct {
		pid      int
		cachedAt time.Time
	}
	entries := make([]entry, 0, len(m.cache))
	for pid, c := range m.cache {
		entries = append(entries, entry{pid: pid, cachedAt: c.CachedAt})
	}

	// Sort by age (oldest first) - simple selection for small evict count
	for i := 0; i < toEvict; i++ {
		oldestIdx := i
		for j := i + 1; j < len(entries); j++ {
			if entries[j].cachedAt.Before(entries[oldestIdx].cachedAt) {
				oldestIdx = j
			}
		}
		entries[i], entries[oldestIdx] = entries[oldestIdx], entries[i]
		delete(m.cache, entries[i].pid)
	}
}

// GetAllCached returns all currently cached PodInfo entries.
// Useful for debugging and testing.
func (m *ProcessMapper) GetAllCached() map[int]*blackbox.PodInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[int]*blackbox.PodInfo, len(m.cache))
	for pid, cached := range m.cache {
		if cached.Info != nil {
			result[pid] = cached.Info
		}
	}
	return result
}
