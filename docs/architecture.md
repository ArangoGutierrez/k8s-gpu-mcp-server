# Architecture

This document describes the architecture, design decisions, and technical
implementation of `k8s-gpu-mcp-server`.

## Table of Contents

- [Overview](#overview)
- [Design Principles](#design-principles)
- [System Architecture](#system-architecture)
- [Component Design](#component-design)
- [Data Flow](#data-flow)
- [Security Model](#security-model)
- [Performance Considerations](#performance-considerations)
- [Design Decisions](#design-decisions)
- [File Structure](#file-structure)
- [Extension Points](#extension-points)

## Overview

`k8s-gpu-mcp-server` is an **ephemeral diagnostic agent** that provides real-time
NVIDIA GPU hardware introspection for Kubernetes clusters via the Model
Context Protocol (MCP).

### Key Characteristics

- **HTTP-First**: JSON-RPC 2.0 over HTTP/SSE (production default)
- **On-Demand**: Agent runs only during diagnostic sessions
- **AI-Native**: Designed for AI assistant consumption (Claude, Cursor)
- **Hardware-Focused**: Direct NVML access for deep GPU diagnostics
- **Multi-Node**: Gateway architecture for cluster-wide diagnostics
- **Observable**: Prometheus metrics, circuit breaker, distributed tracing

## Design Principles

### 1. On-Demand Diagnostics

Unlike traditional monitoring (always-running exporters with network endpoints),
the agent only runs during active diagnostic sessions:

```
Traditional Monitoring:              k8s-gpu-mcp-server:
┌─────────────────┐                 ┌─────────────────────────────┐
│ DaemonSet       │                 │ DaemonSet                   │
│ (Always On)     │                 │ └─ HTTP server on :8080     │
└────────┬────────┘                 └───────────────┬─────────────┘
         │                                          │
         ▼                                          │ HTTP request
┌─────────────────┐                                 ▼
│   Metrics       │                 ┌─────────────────────────────┐
│   Server        │                 │ Process request             │
│  :9090/tcp      │                 │ └─ Query NVML               │
│  (always open)  │                 │ └─ Return JSON response     │
└─────────────────┘                 └───────────────┬─────────────┘
                                                    │
                                                    ▼ idle
                                    ┌─────────────────────────────┐
                                    │ Wait for next request       │
                                    │ (15-20MB memory footprint)  │
                                    └─────────────────────────────┘
```

**Benefits:**
- **Low resource usage** when idle (~15-20MB resident memory)
- **Always-available** diagnostics via persistent HTTP server or kubectl exec
- **No GPU allocation** — doesn't block scheduler
- **Works with AI agents** and human SREs alike

### 2. Transport Options

The agent supports two transport modes:

| Transport | Default For | Use Case | Overhead |
|-----------|-------------|----------|----------|
| **HTTP** | Gateway, Production | Multi-node clusters | Low (~12-57ms/request) |
| **Stdio** | Direct Access | Single-node debugging | Higher (process spawn) |

**HTTP Mode (Production Default):**
- Persistent HTTP server on port 8080
- Low memory footprint (~15-20MB resident)
- Direct pod-to-pod HTTP routing
- Health endpoints (`/healthz`, `/readyz`, `/metrics`)
- Ideal for multi-node gateway deployments

**Stdio Mode (Direct Access):**
- Works through `kubectl exec` SPDY tunneling
- Works with Docker direct stdin/stdout
- No network configuration required
- Firewall-friendly (no listening ports)
- Ideal for single-node debugging

### 3. Gateway Architecture

For multi-node clusters, the gateway provides unified access:

```
┌─────────────────────────────────────────────────────────────────────┐
│                    MCP Client (Claude/Cursor)                        │
└────────────────────────────┬────────────────────────────────────────┘
                             │ MCP over stdio
                             ▼
┌─────────────────────────────────────────────────────────────────────┐
│                    NPM Bridge (local workstation)                    │
│                    kubectl port-forward :8080                        │
└────────────────────────────┬────────────────────────────────────────┘
                             │ HTTP
                             ▼
┌─────────────────────────────────────────────────────────────────────┐
│                    Gateway Pod (:8080)                               │
│  ┌──────────────┐  ┌─────────────────┐  ┌────────────────┐         │
│  │    Router    │─▶│ Circuit Breaker │─▶│  HTTP Client   │         │
│  └──────────────┘  └─────────────────┘  └───────┬────────┘         │
│                                                  │                   │
│  Prometheus Metrics: mcp_gateway_request_duration_seconds{node}     │
└──────────────────────────────────────────────────┼──────────────────┘
                                                   │ HTTP (pod-to-pod)
              ┌────────────────────────────────────┼────────────────┐
              ▼                                    ▼                ▼
┌─────────────────────┐  ┌─────────────────────┐  ┌─────────────────────┐
│  Agent Pod (Node 1) │  │  Agent Pod (Node 2) │  │  Agent Pod (Node N) │
│  :8080              │  │  :8080              │  │  :8080              │
│  ┌───────────────┐  │  │  ┌───────────────┐  │  │  ┌───────────────┐  │
│  │ 9 MCP Tools   │  │  │  │ 9 MCP Tools   │  │  │  │ 9 MCP Tools   │  │
│  │ NVML Client   │  │  │  │ NVML Client   │  │  │  │ NVML Client   │  │
│  └───────┬───────┘  │  │  └───────┬───────┘  │  │  └───────┬───────┘  │
│          │ CGO      │  │          │ CGO      │  │          │ CGO      │
│          ▼          │  │          ▼          │  │          ▼          │
│       GPU 0..N      │  │       GPU 0..N      │  │       GPU 0..N      │
└─────────────────────┘  └─────────────────────┘  └─────────────────────┘
```

**Gateway Routing Modes:**

| Mode | Flag | Description | Performance |
|------|------|-------------|-------------|
| **HTTP** (default) | `--routing-mode=http` | Direct HTTP to agent pods | ~12-57ms |
| **Exec** (legacy) | `--routing-mode=exec` | kubectl exec to agents | ~30s |

### 4. Interface Abstraction

We abstract NVML behind a Go interface for testability:

```go
type Interface interface {
    Init(ctx context.Context) error
    GetDeviceCount(ctx context.Context) (int, error)
    GetDeviceByIndex(ctx context.Context, idx int) (Device, error)
    // ...
}
```

**Benefits:**
- **Testable**: Mock implementation for CI/development
- **Flexible**: Can add other GPU vendors (AMD, Intel)
- **Safe**: Isolates CGO complexity
- **Portable**: Tests run on any platform (538 tests, no GPU required)

## System Architecture

### Deployment Modes

| Mode | Use Case | Command |
|------|----------|---------|
| **Kubernetes (Gateway)** | Production GPU clusters | `helm install ...` |
| **Kubernetes (Direct)** | Single-node debugging | `kubectl exec -it <pod> -- /agent` |
| **Docker** | Slurm, workstations | `docker run --gpus all ... /agent` |
| **Local** | Development, testing | `./agent --nvml-mode=mock` |

### Component Layers

```
┌─────────────────────────────────────────────────────────────────┐
│  CLI Layer (cmd/agent/main.go)                                   │
│  - Flag parsing (--port, --gateway, --routing-mode)              │
│  - Lifecycle management                                          │
│  - Signal handling (SIGINT, SIGTERM)                             │
└──────────────────────────┬──────────────────────────────────────┘
                           │
┌──────────────────────────▼──────────────────────────────────────┐
│  MCP Server Layer (pkg/mcp/)                                     │
│  - JSON-RPC 2.0 protocol                                         │
│  - HTTP transport (HTTPServer)                                   │
│  - Stdio transport (ServeStdio)                                  │
│  - Tool registration and routing                                 │
└──────────────────────────┬──────────────────────────────────────┘
                           │
        ┌──────────────────┼──────────────────┐
        │                  │                  │
        ▼                  ▼                  ▼
┌───────────────┐  ┌───────────────┐  ┌───────────────────────────┐
│ Gateway Layer │  │  Tool Layer   │  │  K8s Client Layer         │
│ (pkg/gateway/)│  │ (pkg/tools/)  │  │  (pkg/k8s/)               │
│               │  │               │  │                           │
│ - Router      │  │ - gpu_inv     │  │ - Pod discovery           │
│ - CircuitBrkr │  │ - gpu_health  │  │ - Node listing            │
│ - HTTPClient  │  │ - xid_errors  │  │ - Exec in pod             │
│ - Tracing     │  │ - describe_   │  │ - Service discovery       │
│               │  │   gpu_node    │  │                           │
│               │  │ - pod_gpu_    │  │                           │
│               │  │   allocation  │  │                           │
└───────────────┘  └───────┬───────┘  └───────────────────────────┘
                           │
┌──────────────────────────▼──────────────────────────────────────┐
│  NVML Abstraction Layer (pkg/nvml/)                              │
│  ┌─────────────────┐    ┌─────────────────┐                     │
│  │    Mock         │    │    Real         │                     │
│  │  (Testing)      │    │  (go-nvml/CGO)  │                     │
│  │  No GPU needed  │    │  Requires GPU   │                     │
│  └─────────────────┘    └─────────────────┘                     │
└─────────────────────────────────────────────────────────────────┘
```

### GPU Access in Kubernetes

GPU access requires CDI (Container Device Interface) injection, provided
by nvidia-container-toolkit. The agent supports clusters with:

- **NVIDIA Device Plugin** — Standard GPU scheduling
- **NVIDIA GPU Operator** — Full-stack GPU management
- **NVIDIA DRA Driver** — Dynamic Resource Allocation (K8s 1.32+)

> **Note:** The agent does NOT request `nvidia.com/gpu` resources. It monitors
> all GPUs on a node without consuming scheduler-visible resources.

**Helm Chart:** [`deployment/helm/k8s-gpu-mcp-server/`](../deployment/helm/k8s-gpu-mcp-server/)

| Mode | Default | Description |
|------|---------|-------------|
| **RuntimeClass** | ✓ | Uses `runtimeClassName: nvidia` + `NVIDIA_VISIBLE_DEVICES=all` |
| **Resource Request** | | Requests `nvidia.com/gpu` from device plugin (fallback) |

## Component Design

### MCP Server (`pkg/mcp/`)

The MCP server supports dual transport modes:

```go
type Server struct {
    mcpServer   *server.MCPServer
    mode        string           // "read-only" or "operator"
    nvmlClient  nvml.Interface
    transport   TransportType    // "stdio" or "http"
    httpAddr    string           // e.g., "0.0.0.0:8080"
    gatewayMode bool
    k8sClient   *k8s.Client
}
```

**Key Files:**
- `server.go` - Main server, tool registration, transport selection
- `http.go` - HTTP transport with health endpoints
- `oneshot.go` - Single-request mode for exec-based invocations

### Gateway (`pkg/gateway/`)

The gateway routes requests to agent pods across the cluster:

```go
type Router struct {
    k8sClient      *k8s.Client
    httpClient     *AgentHTTPClient
    routingMode    RoutingMode        // "http" or "exec"
    circuitBreaker *CircuitBreaker
    maxConcurrency int                // Default: 10
}
```

**Key Files:**
- `router.go` - Request routing, node discovery, result aggregation
- `circuit_breaker.go` - Per-node circuit breaker (closed/open/half-open)
- `http_client.go` - HTTP client for agent communication
- `proxy.go` - Tool proxy handlers for gateway mode
- `tracing.go` - Distributed tracing with correlation IDs
- `framing.go` - MCP message framing utilities

**Circuit Breaker States:**

| State | Behavior |
|-------|----------|
| **Closed** | Requests flow normally |
| **Open** | Requests fail fast (node unhealthy) |
| **Half-Open** | Probe requests to test recovery |

### K8s Client (`pkg/k8s/`)

Provides Kubernetes API access for the gateway and K8s-aware tools:

```go
type Client struct {
    clientset kubernetes.Interface
    namespace string
    config    *rest.Config
}
```

**Capabilities:**
- `ListGPUNodes()` - Discover agent pods on GPU nodes
- `GetPodForNode()` - Find agent pod for specific node
- `ExecInPod()` - Execute commands in agent pods (legacy routing)

### NVML Abstraction (`pkg/nvml/`)

**Interface Design:**
```go
type Interface interface {
    Init(ctx context.Context) error
    Shutdown(ctx context.Context) error
    GetDeviceCount(ctx context.Context) (int, error)
    GetDeviceByIndex(ctx context.Context, idx int) (Device, error)
}

type Device interface {
    GetName(ctx context.Context) (string, error)
    GetUUID(ctx context.Context) (string, error)
    GetTemperature(ctx context.Context) (uint32, error)
    GetPowerUsage(ctx context.Context) (uint32, error)
    GetMemoryInfo(ctx context.Context) (*MemoryInfo, error)
    GetUtilizationRates(ctx context.Context) (*Utilization, error)
    // ... more methods
}
```

**Implementations:**

| Implementation | File | Use Case |
|----------------|------|----------|
| **Mock** | `mock.go` | Testing, CI/CD, no GPU required |
| **Real** | `real.go` | Production, requires GPU + CGO |
| **Stub** | `real_stub.go` | Non-CGO builds, returns errors |

### Tool Handlers (`pkg/tools/`)

Nine MCP tools are available:

| Tool | File | Category | Description |
|------|------|----------|-------------|
| `get_gpu_inventory` | `gpu_inventory.go` | NVML | Hardware inventory + telemetry |
| `get_gpu_health` | `gpu_health.go` | NVML | Health monitoring with scoring |
| `analyze_xid_errors` | `analyze_xid.go` | NVML | XID error parsing from kernel logs |
| `get_nvlink_topology` | `nvlink_topology.go` | NVML | NVLink interconnect topology and health |
| `get_gpu_timeline` | `get_gpu_timeline.go` | NVML + Blackbox | Historical GPU metrics from flight recorder |
| `describe_gpu_node` | `describe_gpu_node.go` | K8s + NVML | Node-level diagnostics |
| `get_pod_gpu_allocation` | `pod_gpu_allocation.go` | K8s | GPU-to-Pod correlation |
| `explain_failure` | `explain_failure.go` | K8s + Incidents | Root cause analysis for failed workloads |
| `get_incident_report` | `get_incident_report.go` | K8s + Incidents | Detailed incident report with timeline |

**Tool Handler Pattern:**
```go
type XYZHandler struct {
    nvmlClient nvml.Interface  // or k8s clientset
}

func (h *XYZHandler) Handle(
    ctx context.Context,
    request mcp.CallToolRequest,
) (*mcp.CallToolResult, error) {
    // 1. Check context cancellation
    // 2. Extract and validate arguments
    // 3. Query NVML or K8s API
    // 4. Format response as JSON
    // 5. Return MCP result
}
```

### DCGM Integration (`pkg/dcgm/`)

Optional abstraction layer over NVIDIA DCGM (Data Center GPU Manager). Provides
advanced GPU telemetry features beyond NVML when DCGM is available, and degrades
gracefully to NVML-only mode when it is not.

**Key Types:**
- `Interface` - Contract for DCGM operations (Init, Shutdown, WatchFields, etc.)
- `Client` - Placeholder client for external DCGM daemon connection
- `Embedded` - Embedded mode that starts nv-hostengine internally
- `Stub` - No-op implementation for when DCGM is unavailable
- `Mock` - Test implementation with configurable responses

**Connection Modes:**

| Mode | Description | Use Case |
|------|-------------|----------|
| **Embedded** | Starts nv-hostengine internally | Self-contained deployments |
| **External** | Connects to existing DCGM daemon via socket | Shared DCGM infrastructure |
| **Stub** | No-op, returns empty data | Consumer GPUs, DCGM unavailable |

**DCGM-Exclusive Features:**
- Native XID error field (no `/dev/kmsg` log parsing required)
- GPU profiling metrics (SM occupancy, tensor core utilization)
- NVSwitch fabric monitoring
- Built-in time-series with statistical aggregation
- Health policies and configurable thresholds

**Key Files:**
- `interface.go` - Interface definition with all DCGM operations
- `client.go` - External socket connection client
- `embedded.go` - Embedded nv-hostengine mode
- `stub.go` - No-op stub for graceful degradation
- `mock.go` - Test mock with configurable responses
- `types.go` - Data types (ProfilingMetrics, NVSwitchStatus, HealthPolicy, etc.)

### Flight Recorder (`pkg/blackbox/`)

Continuously captures GPU telemetry into per-GPU ring buffers for historical
analysis. Named after aviation "black box" flight recorders, this package
retains a sliding window of GPU metrics used by the `get_gpu_timeline` and
`get_incident_report` tools.

**Key Types:**
- `Recorder` - Main recorder that polls NVML at configurable intervals
- `RingBuffer[T]` - Generic ring buffer for fixed-size rolling storage
- `GPUSnapshot` - Single telemetry sample (temperature, power, utilization, memory, throttle state)
- `RecorderConfig` - Configuration (poll interval, buffer size, GPU filters)

**How It Works:**
1. `Recorder.Start()` begins periodic polling of all GPUs via NVML
2. Each poll creates a `GPUSnapshot` stored in a per-GPU `RingBuffer`
3. Tools query historical data via `GetTimeline(uuid, duration)` or `GetAllTimelines(duration)`
4. `Recorder.Stop()` halts polling; data remains queryable until garbage collected

**Key Files:**
- `recorder.go` - Main recorder with lifecycle management
- `ring_buffer.go` - Generic ring buffer implementation
- `types.go` - GPUSnapshot and configuration types

### Event Correlation (`pkg/events/`)

Correlates events from multiple sources (Kubernetes, XID errors, GPU telemetry)
around a trigger event to build a unified incident timeline. Used by the
`explain_failure` and `get_incident_report` tools.

**Key Types:**
- `Correlator` - Main correlation engine that groups events within a time window
- `Event` - Unified event from any source (K8s, XID, GPU telemetry)
- `CorrelatedIncident` - Result of correlation: trigger, related events, timeline, affected pods
- `K8sEvent` - Kubernetes event data
- `TimelineEntry` - Single entry in the correlated timeline
- `AffectedPod` - Pod affected by an incident

**How It Works:**
1. A trigger event (e.g., pod failure) is submitted to the `Correlator`
2. The correlator queries multiple sources within a configurable time window (default: 5s)
3. Events are sorted chronologically and causality is assessed
4. A `CorrelatedIncident` is returned with timeline, affected pods, and GPU snapshots

**Key Files:**
- `correlator.go` - Event correlation engine
- `types.go` - Event, CorrelatedIncident, and related types

### Incident Analysis (`pkg/incidents/`)

Performs pattern-based root cause analysis on correlated incidents. Uses
known GPU failure patterns to determine whether a failure is hardware-related
or user-caused, with confidence scoring.

**Key Types:**
- `Analyzer` - Pattern matcher that scores root causes with confidence
- `Explainer` - Generates human-readable explanations from incidents
- `IncidentReport` - Analysis result with root cause, confidence, and recommendations
- `FailurePattern` - Known failure pattern with match criteria and scoring
- `Recommendation` - Actionable recommendation with priority
- `RootCause` - Identified cause with category, confidence, and `not_your_code` flag

**Known Failure Categories:**
- `gpu_hardware` - GPU hardware failures (XID errors, ECC errors)
- `oom` - GPU out-of-memory conditions
- `thermal` - Thermal throttling or shutdown
- `nvlink` - NVLink communication failures
- `driver` - GPU driver issues
- `user_code` - Application-level failures
- `scheduling` - Kubernetes scheduling failures

**Key Files:**
- `analyzer.go` - Root cause analysis with pattern matching
- `explainer.go` - Human-readable explanation generation
- `patterns.go` - Known GPU failure patterns
- `types.go` - IncidentReport, RootCause, Recommendation types

### Metrics (`pkg/metrics/`)

Prometheus metrics for observability:

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `mcp_gateway_request_duration_seconds` | Histogram | `node`, `transport`, `status` | Per-node request latency |
| `mcp_circuit_breaker_state` | Gauge | `node` | Circuit state (0=closed, 1=open, 2=half-open) |
| `mcp_node_healthy` | Gauge | `node` | Node health (0/1) |

## Data Flow

### HTTP Transport Flow (Production)

```
MCP Client
    │
    │ HTTP POST /mcp
    ▼
Gateway Pod
    │
    ├─► Router.RouteToAllNodes()
    │     │
    │     ├─► CircuitBreaker.Allow(node)
    │     │
    │     ├─► HTTPClient.CallMCP(endpoint, request)
    │     │         │
    │     │         │ HTTP POST http://<pod-ip>:8080/mcp
    │     │         ▼
    │     │   Agent Pod
    │     │         │
    │     │         ├─► MCP Server parses JSON-RPC
    │     │         ├─► Route to tool handler
    │     │         ├─► Query NVML
    │     │         ├─► Return JSON response
    │     │         │
    │     │   ◄─────┘
    │     │
    │     ├─► CircuitBreaker.RecordSuccess/Failure()
    │     ├─► metrics.RecordGatewayRequest()
    │     │
    │     └─► Aggregate results from all nodes
    │
    └─► Return aggregated JSON-RPC response
```

### Stdio Transport Flow (Direct Access)

```
kubectl exec -it <pod> -- /agent
    │
    │ stdin (JSON-RPC)
    ▼
Agent Process
    │
    ├─► MCP Server (stdio transport)
    │     │
    │     ├─► Parse JSON-RPC 2.0
    │     ├─► Route to tool handler
    │     │
    │     ▼
    │   Tool Handler
    │     │
    │     ├─► Validate arguments
    │     ├─► Call NVML client
    │     │
    │     ▼
    │   NVML Client
    │     │
    │     ├─► Query GPU hardware
    │     │
    │   ◄─┘
    │
    └─► stdout (JSON-RPC response)
```

### Logging Flow

```
Application Logs ──► stderr (klog structured JSON)
MCP Protocol    ──► stdout (JSON-RPC messages only)
```

**Critical**: Logs NEVER go to stdout (breaks MCP protocol)

## Security Model

### Read-Only Mode (Default)

```
Allowed:
✓ Query GPU properties (name, UUID, temp, memory)
✓ Read telemetry (power, utilization)
✓ Inspect topology (NVLink, PCIe)
✓ Read ECC counters
✓ Parse XID errors
✓ Query K8s node/pod metadata

Denied:
✗ Kill GPU processes
✗ Reset GPUs
✗ Modify settings
```

### Operator Mode (Explicit Flag)

```
--mode=operator enables:
✓ All read-only operations
✓ Kill GPU processes by PID (future)
✓ Trigger GPU reset (future)
```

### Kubernetes Security Context

```yaml
securityContext:
  runAsUser: 0                    # May be required for NVML
  allowPrivilegeEscalation: false
  readOnlyRootFilesystem: true
  capabilities:
    drop: ["ALL"]
    add: ["SYSLOG"]               # Required for /dev/kmsg (XID errors)
```

See [Security Model](security.md) for detailed RBAC configuration.

## Performance Considerations

### Latency Comparison

| Transport | P50 Latency | P95 Latency | Notes |
|-----------|-------------|-------------|-------|
| HTTP (gateway) | ~50ms | ~200ms | Includes network hop |
| HTTP (direct) | ~12ms | ~57ms | Direct to agent pod |
| Stdio (exec) | ~30s | ~45s | Process spawn overhead |

### Memory Footprint

| Mode | Memory | Notes |
|------|--------|-------|
| HTTP server (idle) | ~15-20MB | Persistent process |
| HTTP server (active) | ~20-25MB | During request processing |
| Exec mode | ~200MB spike | Per-request process spawn |

### Binary Size

| Component | Size | Strategy |
|-----------|------|----------|
| Go runtime | ~2MB | Stripped binaries (`-ldflags="-s -w"`) |
| MCP library | ~1MB | Minimal dependencies |
| NVML bindings | ~4MB | Dynamic linking to `libnvidia-ml.so` |
| **Total** | **~7-8MB** | 84% under 50MB target |

### Concurrency

- **Gateway**: Configurable `maxConcurrency` (default: 10 concurrent requests)
- **NVML**: Serialized calls (NVML is not thread-safe)
- **Circuit Breaker**: Per-node state with `sync.RWMutex`

## Design Decisions

### Decision 1: Interface vs Direct NVML

**Chosen**: Interface abstraction

**Rationale:**
- Enables testing without GPU (538 tests pass in CI)
- Isolates CGO complexity
- Future-proof for multi-vendor (AMD ROCm, Intel)

### Decision 2: HTTP vs Stdio Transport

**Chosen**: HTTP primary, Stdio secondary

**History:** Initially stdio-only (M1), HTTP added in M3 ([Epic #112](https://github.com/ArangoGutierrez/k8s-gpu-mcp-server/issues/112))

**Rationale:**
- HTTP: 150× faster than exec-based routing
- HTTP: Constant memory (~15-20MB vs 200MB spikes)
- HTTP: Enables circuit breaker and metrics
- Stdio: Still valuable for direct debugging

### Decision 3: HTTP Routing vs Exec Routing (Gateway)

**Chosen**: HTTP routing (default), Exec routing (legacy fallback)

**Rationale:**
- HTTP routing: Direct pod-to-pod, ~50ms latency
- Exec routing: Via API server, ~30s latency
- HTTP requires CNI to support cross-node pod networking
  (see [Cross-Node Networking](troubleshooting/cross-node-networking.md))

### Decision 4: DaemonSet vs kubectl debug

**Chosen**: DaemonSet with persistent HTTP server

**Alternatives Considered:**
1. `kubectl debug` ephemeral containers - Cannot access GPUs
2. On-demand Pod creation - 5-10s startup overhead
3. DaemonSet with sleeping container - Near-zero resource but exec overhead

**Rationale:**
- DaemonSet provides instant access
- HTTP server has low idle resource usage (~15MB)
- Gateway can route to any node immediately

### Decision 5: Runtime vs Compile-Time Mode Selection

**Chosen**: Runtime flags (`--nvml-mode`, `--routing-mode`)

**Rationale:**
- Single binary for all environments
- Explicit control over behavior
- Easy testing and debugging

### Decision 6: Go 1.25 (Latest Stable)

**Chosen**: Go 1.25.x

**Rationale:**
- Go 1.23 EOL in August 2025
- Latest features and security patches
- Better performance
- `klog/v2` requires Go 1.21+

## File Structure

```
k8s-gpu-mcp-server/
├── cmd/agent/                   # Entry point
│   └── main.go                  # CLI, flags, lifecycle
│
├── pkg/                         # Public packages
│   ├── gateway/                 # Gateway router (M3)
│   │   ├── router.go            # Request routing, node discovery
│   │   ├── circuit_breaker.go   # Per-node circuit breaker
│   │   ├── http_client.go       # HTTP client for agents
│   │   ├── proxy.go             # Tool proxy handlers
│   │   ├── tracing.go           # Correlation ID generation
│   │   └── framing.go           # MCP message framing
│   │
│   ├── k8s/                     # Kubernetes client (M3)
│   │   └── client.go            # Pod discovery, exec, node listing
│   │
│   ├── mcp/                     # MCP protocol layer
│   │   ├── server.go            # Server, tool registration
│   │   ├── http.go              # HTTP transport
│   │   ├── oneshot.go           # Single-request mode
│   │   └── metrics.go           # Request metrics
│   │
│   ├── metrics/                 # Prometheus metrics
│   │   └── metrics.go           # Metric definitions
│   │
│   ├── nvml/                    # NVML abstraction
│   │   ├── interface.go         # Interface definition
│   │   ├── mock.go              # Mock implementation
│   │   ├── real.go              # Real NVML (CGO)
│   │   └── real_stub.go         # Non-CGO stub
│   │
│   ├── tools/                   # MCP tool handlers
│   │   ├── gpu_inventory.go     # get_gpu_inventory
│   │   ├── gpu_health.go        # get_gpu_health
│   │   ├── analyze_xid.go       # analyze_xid_errors
│   │   ├── nvlink_topology.go   # get_nvlink_topology
│   │   ├── get_gpu_timeline.go  # get_gpu_timeline
│   │   ├── describe_gpu_node.go # describe_gpu_node
│   │   ├── pod_gpu_allocation.go# get_pod_gpu_allocation
│   │   ├── explain_failure.go   # explain_failure
│   │   ├── get_incident_report.go # get_incident_report
│   │   ├── pod_failure.go       # Pod failure extraction helpers
│   │   └── validation.go        # Input validation
│   │
│   ├── blackbox/                # Flight recorder
│   │   ├── recorder.go          # GPU telemetry recorder
│   │   ├── ring_buffer.go       # Generic ring buffer
│   │   └── types.go             # GPUSnapshot, config types
│   │
│   ├── dcgm/                    # DCGM integration (optional)
│   │   ├── interface.go         # DCGM interface definition
│   │   ├── client.go            # External DCGM client
│   │   ├── embedded.go          # Embedded nv-hostengine mode
│   │   ├── stub.go              # No-op stub
│   │   ├── mock.go              # Test mock
│   │   └── types.go             # DCGM data types
│   │
│   ├── events/                  # Event correlation
│   │   ├── correlator.go        # Event correlation engine
│   │   └── types.go             # Event, CorrelatedIncident types
│   │
│   ├── incidents/               # Incident analysis
│   │   ├── analyzer.go          # Root cause analysis
│   │   ├── explainer.go         # Human-readable explanations
│   │   ├── patterns.go          # Known failure patterns
│   │   └── types.go             # Report, RootCause types
│   │
│   └── xid/                     # XID error parsing
│       ├── codes.go             # XID code database
│       ├── parser.go            # Log parsing
│       └── kmsg.go              # /dev/kmsg reader
│
├── internal/                    # Private implementation
│   └── info/                    # Build-time version info
│
├── deployment/                  # Deployment manifests
│   ├── helm/                    # Helm chart
│   │   └── k8s-gpu-mcp-server/
│   ├── rbac/                    # Standalone RBAC manifests
│   └── Containerfile            # Container build
│
├── examples/                    # Sample JSON-RPC requests
│   ├── initialize.json
│   ├── gpu_inventory.json
│   ├── gpu_health.json
│   ├── analyze_xid.json
│   ├── describe_gpu_node.json
│   ├── pod_gpu_allocation.json
│   ├── nvlink_topology.json
│   ├── explain_failure.json
│   ├── get_incident_report.json
│   └── get_gpu_timeline.json
│
├── npm/                         # npm package
│   ├── package.json
│   └── bin/                     # Binary wrapper
│
└── docs/                        # Documentation
    ├── architecture.md          # This file
    ├── quickstart.md            # Getting started
    ├── mcp-usage.md             # MCP protocol guide
    ├── security.md              # Security model
    ├── troubleshooting/         # Troubleshooting guides
    └── reports/                 # Milestone reports
```

## Extension Points

### Adding New Tools

1. **Create handler** in `pkg/tools/`:

```go
// pkg/tools/my_tool.go
type MyToolHandler struct {
    nvmlClient nvml.Interface
}

func NewMyToolHandler(nvml nvml.Interface) *MyToolHandler {
    return &MyToolHandler{nvmlClient: nvml}
}

func (h *MyToolHandler) Handle(
    ctx context.Context,
    request mcp.CallToolRequest,
) (*mcp.CallToolResult, error) {
    // Implementation
    return mcp.NewToolResultText("result"), nil
}

func GetMyTool() mcp.Tool {
    return mcp.NewTool("my_tool",
        mcp.WithDescription("My tool description"),
        mcp.WithString("param1",
            mcp.Required(),
            mcp.Description("Parameter description"),
        ),
    )
}
```

2. **Register in `pkg/mcp/server.go`**:

```go
myToolHandler := tools.NewMyToolHandler(cfg.NVMLClient)
mcpServer.AddTool(tools.GetMyTool(), myToolHandler.Handle)
```

3. **Add tests** in `pkg/tools/my_tool_test.go`

4. **Add example** in `examples/my_tool.json`

### Supporting New GPU Vendors

The `nvml.Interface` abstraction allows adding support for other GPU vendors:

1. Implement `nvml.Interface` for vendor SDK (e.g., AMD ROCm)
2. Add runtime flag: `--gpu-vendor=nvidia|amd|intel`
3. Update `main.go` selection logic
4. Add tests

No changes needed to MCP layer or tool handlers!

### Adding New Metrics

1. Define metric in `pkg/metrics/metrics.go`:

```go
var myMetric = prometheus.NewGaugeVec(
    prometheus.GaugeOpts{
        Name: "mcp_my_metric",
        Help: "Description",
    },
    []string{"label1", "label2"},
)
```

2. Register in `init()`:

```go
prometheus.MustRegister(myMetric)
```

3. Record values:

```go
metrics.SetMyMetric(label1, label2, value)
```

## References

- [MCP Protocol Specification](https://modelcontextprotocol.io/)
- [NVIDIA NVML Documentation](https://docs.nvidia.com/deploy/nvml-api/)
- [go-nvml Library](https://github.com/NVIDIA/go-nvml)
- [mcp-go Library](https://github.com/mark3labs/mcp-go)
- [NVIDIA Device Plugin](https://github.com/NVIDIA/k8s-device-plugin)
- [NVIDIA GPU Operator](https://github.com/NVIDIA/gpu-operator)
- [Architecture Decision Report](reports/k8s-deploy-architecture-decision.md)
- [Cross-Node Networking Guide](troubleshooting/cross-node-networking.md)
