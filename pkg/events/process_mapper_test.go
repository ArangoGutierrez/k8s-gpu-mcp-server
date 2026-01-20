// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

package events

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ArangoGutierrez/k8s-gpu-mcp-server/pkg/blackbox"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

// TestParseCgroup_V1_CriO tests cgroup v1 parsing for CRI-O runtime.
func TestParseCgroup_V1_CriO(t *testing.T) {
	// Create mock /proc filesystem
	procPath := t.TempDir()
	pid := 12345
	cgroupFile := filepath.Join(procPath, "12345", "cgroup")
	if err := os.MkdirAll(filepath.Dir(cgroupFile), 0755); err != nil {
		t.Fatal(err)
	}

	// CRI-O cgroup v1 format (64-char container ID)
	cgroupContent := `11:cpuset:/kubepods/burstable/pod8a1b2c3d-4e5f-6789-abcd-ef0123456789/crio-abc123def456789012345678901234567890123456789012345678901234abcd
10:memory:/kubepods/burstable/pod8a1b2c3d-4e5f-6789-abcd-ef0123456789/crio-abc123def456789012345678901234567890123456789012345678901234abcd
`
	if err := os.WriteFile(cgroupFile, []byte(cgroupContent), 0644); err != nil {
		t.Fatal(err)
	}

	mapper := NewProcessMapper(nil, "test-node", WithProcPath(procPath))
	containerID, err := mapper.parseCgroup(pid)
	if err != nil {
		t.Fatalf("parseCgroup failed: %v", err)
	}

	expected := "abc123def456789012345678901234567890123456789012345678901234abcd"
	if containerID != expected {
		t.Errorf("got containerID %q, want %q", containerID, expected)
	}
}

// TestParseCgroup_V1_Containerd tests cgroup v1 parsing for containerd.
func TestParseCgroup_V1_Containerd(t *testing.T) {
	procPath := t.TempDir()
	pid := 12346
	cgroupFile := filepath.Join(procPath, "12346", "cgroup")
	if err := os.MkdirAll(filepath.Dir(cgroupFile), 0755); err != nil {
		t.Fatal(err)
	}

	// containerd cgroup v1 format (no runtime prefix, 64-char container ID)
	cgroupContent := `11:cpuset:/kubepods/burstable/pod8a1b2c3d-4e5f-6789-abcd-ef0123456789/def456789012345678901234567890123456789012345678901234567890abcd
`
	if err := os.WriteFile(cgroupFile, []byte(cgroupContent), 0644); err != nil {
		t.Fatal(err)
	}

	mapper := NewProcessMapper(nil, "test-node", WithProcPath(procPath))
	containerID, err := mapper.parseCgroup(pid)
	if err != nil {
		t.Fatalf("parseCgroup failed: %v", err)
	}

	expected := "def456789012345678901234567890123456789012345678901234567890abcd"
	if containerID != expected {
		t.Errorf("got containerID %q, want %q", containerID, expected)
	}
}

// TestParseCgroup_V2 tests cgroup v2 unified hierarchy parsing.
func TestParseCgroup_V2(t *testing.T) {
	procPath := t.TempDir()
	pid := 12347
	cgroupFile := filepath.Join(procPath, "12347", "cgroup")
	if err := os.MkdirAll(filepath.Dir(cgroupFile), 0755); err != nil {
		t.Fatal(err)
	}

	// cgroup v2 unified format (containerd, 64-char container ID)
	cgroupContent := `0::/kubepods.slice/kubepods-burstable.slice/kubepods-burstable-pod8a1b2c3d_4e5f_6789_abcd_ef0123456789.slice/cri-containerd-fed456789012345678901234567890123456789012345678901234567890abcd.scope
`
	if err := os.WriteFile(cgroupFile, []byte(cgroupContent), 0644); err != nil {
		t.Fatal(err)
	}

	mapper := NewProcessMapper(nil, "test-node", WithProcPath(procPath))
	containerID, err := mapper.parseCgroup(pid)
	if err != nil {
		t.Fatalf("parseCgroup failed: %v", err)
	}

	expected := "fed456789012345678901234567890123456789012345678901234567890abcd"
	if containerID != expected {
		t.Errorf("got containerID %q, want %q", containerID, expected)
	}
}

// TestParseCgroup_V2_CriO tests cgroup v2 parsing for CRI-O.
func TestParseCgroup_V2_CriO(t *testing.T) {
	procPath := t.TempDir()
	pid := 12348
	cgroupFile := filepath.Join(procPath, "12348", "cgroup")
	if err := os.MkdirAll(filepath.Dir(cgroupFile), 0755); err != nil {
		t.Fatal(err)
	}

	// cgroup v2 CRI-O format (64-char container ID)
	cgroupContent := `0::/kubepods.slice/kubepods-burstable.slice/crio-aaa456789012345678901234567890123456789012345678901234567890abcd.scope
`
	if err := os.WriteFile(cgroupFile, []byte(cgroupContent), 0644); err != nil {
		t.Fatal(err)
	}

	mapper := NewProcessMapper(nil, "test-node", WithProcPath(procPath))
	containerID, err := mapper.parseCgroup(pid)
	if err != nil {
		t.Fatalf("parseCgroup failed: %v", err)
	}

	expected := "aaa456789012345678901234567890123456789012345678901234567890abcd"
	if containerID != expected {
		t.Errorf("got containerID %q, want %q", containerID, expected)
	}
}

// TestParseCgroup_NonContainer tests that host processes return empty string.
func TestParseCgroup_NonContainer(t *testing.T) {
	procPath := t.TempDir()
	pid := 12349
	cgroupFile := filepath.Join(procPath, "12349", "cgroup")
	if err := os.MkdirAll(filepath.Dir(cgroupFile), 0755); err != nil {
		t.Fatal(err)
	}

	// Non-containerized process (host process)
	cgroupContent := `0::/user.slice/user-1000.slice/session-1.scope
`
	if err := os.WriteFile(cgroupFile, []byte(cgroupContent), 0644); err != nil {
		t.Fatal(err)
	}

	mapper := NewProcessMapper(nil, "test-node", WithProcPath(procPath))
	containerID, err := mapper.parseCgroup(pid)
	if err != nil {
		t.Fatalf("parseCgroup failed: %v", err)
	}

	if containerID != "" {
		t.Errorf("expected empty containerID for host process, got %q", containerID)
	}
}

// TestGetPodForPID_Cached tests cache lookup behavior.
func TestGetPodForPID_Cached(t *testing.T) {
	procPath := t.TempDir()
	mapper := NewProcessMapper(nil, "test-node",
		WithProcPath(procPath),
		WithStaleTTL(5*time.Minute),
	)

	// Manually populate cache
	pid := 99999
	now := time.Now()
	mapper.cache[pid] = &cachedPodInfo{
		Info: &blackbox.PodInfo{
			PodUID:    "test-uid",
			PodName:   "test-pod",
			Namespace: "test-ns",
		},
		CachedAt: now,
	}

	// Should return cached value
	ctx := context.Background()
	info, err := mapper.GetPodForPID(ctx, pid)
	if err != nil {
		t.Fatalf("GetPodForPID failed: %v", err)
	}

	if info == nil {
		t.Fatal("expected PodInfo from cache, got nil")
	}
	if info.PodName != "test-pod" {
		t.Errorf("got PodName %q, want %q", info.PodName, "test-pod")
	}
}

// TestGetPodForPID_DeadProcess tests behavior when process no longer exists.
func TestGetPodForPID_DeadProcess(t *testing.T) {
	procPath := t.TempDir()
	mapper := NewProcessMapper(nil, "test-node",
		WithProcPath(procPath),
		WithStaleTTL(5*time.Minute),
	)

	// Populate cache for a "dead" process (no /proc entry exists)
	pid := 88888
	now := time.Now()
	mapper.cache[pid] = &cachedPodInfo{
		Info: &blackbox.PodInfo{
			PodUID:    "dead-pod-uid",
			PodName:   "dead-pod",
			Namespace: "default",
		},
		CachedAt: now,
	}

	// Process doesn't exist in /proc, should return cached data
	ctx := context.Background()
	info, err := mapper.GetPodForPID(ctx, pid)
	if err != nil {
		t.Fatalf("GetPodForPID failed: %v", err)
	}

	if info == nil {
		t.Fatal("expected cached PodInfo for dead process, got nil")
	}
	if info.PodName != "dead-pod" {
		t.Errorf("got PodName %q, want %q", info.PodName, "dead-pod")
	}
}

// TestGetPodForPID_InvalidPID tests validation of PID values.
func TestGetPodForPID_InvalidPID(t *testing.T) {
	mapper := NewProcessMapper(nil, "test-node")
	ctx := context.Background()

	tests := []struct {
		name string
		pid  int
	}{
		{"zero", 0},
		{"negative", -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := mapper.GetPodForPID(ctx, tt.pid)
			if err == nil {
				t.Error("expected error for invalid PID")
			}
		})
	}
}

// TestCacheExpiry tests that stale cache entries are refreshed.
func TestCacheExpiry(t *testing.T) {
	procPath := t.TempDir()
	mapper := NewProcessMapper(nil, "test-node",
		WithProcPath(procPath),
		WithStaleTTL(100*time.Millisecond), // Very short TTL for testing
	)

	pid := 77777
	// Create an expired cache entry
	expiredTime := time.Now().Add(-200 * time.Millisecond)
	mapper.cache[pid] = &cachedPodInfo{
		Info: &blackbox.PodInfo{
			PodUID:    "expired-uid",
			PodName:   "expired-pod",
			Namespace: "default",
		},
		CachedAt: expiredTime,
	}

	// Create a non-container cgroup file for this PID
	cgroupFile := filepath.Join(procPath, "77777", "cgroup")
	if err := os.MkdirAll(filepath.Dir(cgroupFile), 0755); err != nil {
		t.Fatal(err)
	}
	cgroupContent := `0::/user.slice/user-1000.slice/session-1.scope
`
	if err := os.WriteFile(cgroupFile, []byte(cgroupContent), 0644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	info, err := mapper.GetPodForPID(ctx, pid)
	if err != nil {
		t.Fatalf("GetPodForPID failed: %v", err)
	}

	// Should return nil because it's not a container (stale entry not valid anymore)
	if info != nil {
		t.Errorf("expected nil for non-container process, got %+v", info)
	}
}

// TestRefreshPodCache tests the Pod cache refresh mechanism.
func TestRefreshPodCache(t *testing.T) {
	// Create a fake K8s client with pods
	client := fake.NewClientset()

	// Add a reactor to return pods for our node
	client.PrependReactor("list", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
		listAction := action.(k8stesting.ListAction)
		if listAction.GetListRestrictions().Fields.String() == "spec.nodeName=test-node" {
			return true, &corev1.PodList{
				Items: []corev1.Pod{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "gpu-pod-1",
							Namespace: "default",
							UID:       "pod-uid-1",
						},
						Spec: corev1.PodSpec{
							NodeName: "test-node",
						},
						Status: corev1.PodStatus{
							ContainerStatuses: []corev1.ContainerStatus{
								{
									Name:        "gpu-container",
									ContainerID: "containerd://abc123def456789012345678901234567890123456789012345678901234abcd",
								},
							},
						},
					},
				},
			}, nil
		}
		return false, nil, nil
	})

	mapper := NewProcessMapper(client, "test-node")
	ctx := context.Background()

	err := mapper.RefreshPodCache(ctx)
	if err != nil {
		t.Fatalf("RefreshPodCache failed: %v", err)
	}

	// Check that the pod cache was populated
	if mapper.PodCacheSize() != 1 {
		t.Errorf("expected 1 cached pod, got %d", mapper.PodCacheSize())
	}

	// Verify the cached entry
	mapper.podCacheMu.RLock()
	containerID := "abc123def456789012345678901234567890123456789012345678901234abcd"
	info, ok := mapper.podCache[containerID]
	mapper.podCacheMu.RUnlock()

	if !ok {
		t.Fatal("expected container ID to be in cache")
	}
	if info.PodName != "gpu-pod-1" {
		t.Errorf("got PodName %q, want %q", info.PodName, "gpu-pod-1")
	}
	if info.ContainerName != "gpu-container" {
		t.Errorf("got ContainerName %q, want %q", info.ContainerName, "gpu-container")
	}
}

// TestCacheEviction tests that old entries are evicted when cache is full.
func TestCacheEviction(t *testing.T) {
	mapper := NewProcessMapper(nil, "test-node",
		WithMaxCacheSize(10),
	)

	// Fill the cache beyond capacity
	now := time.Now()
	for i := 0; i < 15; i++ {
		mapper.cache[i+1000] = &cachedPodInfo{
			Info: &blackbox.PodInfo{
				PodName: fmt.Sprintf("pod-%d", i),
			},
			CachedAt: now.Add(-time.Duration(15-i) * time.Second), // Older first
		}
	}

	// Trigger eviction
	mapper.mu.Lock()
	mapper.evictOldest()
	mapper.mu.Unlock()

	// Should have evicted ~10% (at least 1)
	if mapper.CacheSize() >= 15 {
		t.Errorf("expected some entries to be evicted, still have %d", mapper.CacheSize())
	}
}

// TestExtractContainerID tests extracting container ID from K8s format.
func TestExtractContainerID(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "containerd",
			input:    "containerd://abc123def456",
			expected: "abc123def456",
		},
		{
			name:     "docker",
			input:    "docker://abc123def456",
			expected: "abc123def456",
		},
		{
			name:     "cri-o",
			input:    "cri-o://abc123def456",
			expected: "abc123def456",
		},
		{
			name:     "no prefix",
			input:    "abc123def456",
			expected: "abc123def456",
		},
		{
			name:     "empty",
			input:    "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractContainerID(tt.input)
			if result != tt.expected {
				t.Errorf("got %q, want %q", result, tt.expected)
			}
		})
	}
}

// TestClearCache tests cache clearing.
func TestClearCache(t *testing.T) {
	mapper := NewProcessMapper(nil, "test-node")

	// Add some entries
	mapper.cache[1] = &cachedPodInfo{Info: &blackbox.PodInfo{PodName: "pod1"}}
	mapper.cache[2] = &cachedPodInfo{Info: &blackbox.PodInfo{PodName: "pod2"}}

	if mapper.CacheSize() != 2 {
		t.Fatalf("expected 2 entries, got %d", mapper.CacheSize())
	}

	mapper.ClearCache()

	if mapper.CacheSize() != 0 {
		t.Errorf("expected 0 entries after clear, got %d", mapper.CacheSize())
	}
}

// TestExtractContainerIDFromCgroup tests the cgroup line parsing function.
func TestExtractContainerIDFromCgroup(t *testing.T) {
	tests := []struct {
		name     string
		line     string
		expected string
	}{
		{
			name:     "cgroup v1 CRI-O",
			line:     "11:cpuset:/kubepods/burstable/pod8a1b2c3d-4e5f-6789-abcd-ef0123456789/crio-abc123def456789012345678901234567890123456789012345678901234abcd",
			expected: "abc123def456789012345678901234567890123456789012345678901234abcd",
		},
		{
			name:     "cgroup v1 containerd",
			line:     "11:cpuset:/kubepods/burstable/pod8a1b2c3d-4e5f-6789-abcd-ef0123456789/abc123def456789012345678901234567890123456789012345678901234abcd",
			expected: "abc123def456789012345678901234567890123456789012345678901234abcd",
		},
		{
			name:     "cgroup v2 containerd",
			line:     "0::/kubepods.slice/kubepods-burstable.slice/cri-containerd-abc123def456789012345678901234567890123456789012345678901234abcd.scope",
			expected: "abc123def456789012345678901234567890123456789012345678901234abcd",
		},
		{
			name:     "cgroup v2 CRI-O",
			line:     "0::/kubepods.slice/kubepods-burstable.slice/crio-abc123def456789012345678901234567890123456789012345678901234abcd.scope",
			expected: "abc123def456789012345678901234567890123456789012345678901234abcd",
		},
		{
			name:     "non-container",
			line:     "0::/user.slice/user-1000.slice/session-1.scope",
			expected: "",
		},
		{
			name:     "systemd",
			line:     "0::/init.scope",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractContainerIDFromCgroup(tt.line)
			if result != tt.expected {
				t.Errorf("got %q, want %q", result, tt.expected)
			}
		})
	}
}
