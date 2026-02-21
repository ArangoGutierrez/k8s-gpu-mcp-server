# MCP Usage Guide

Learn how to interact with `k8s-gpu-mcp-server` using the Model Context Protocol.

## Table of Contents

- [Introduction](#introduction)
- [MCP Protocol Basics](#mcp-protocol-basics)
- [Using with Claude Desktop](#using-with-claude-desktop)
- [Using with Cursor IDE](#using-with-cursor-ide)
- [Manual JSON-RPC](#manual-json-rpc)
- [Available Tools](#available-tools)
- [Available Prompts](#available-prompts)
- [Error Handling](#error-handling)
- [Best Practices](#best-practices)

## Introduction

The Model Context Protocol (MCP) is an open protocol that enables AI
assistants to securely interact with external tools and data sources.

`k8s-gpu-mcp-server` implements MCP over **stdio** (standard input/output),
making it compatible with:

- **Claude Desktop** - Anthropic's AI assistant
- **Cursor IDE** - AI-powered code editor
- **Custom MCP clients** - Any tool that speaks JSON-RPC 2.0

## MCP Protocol Basics

### Protocol Version

**Current:** `2025-06-18`

Always specify the protocol version in initialization:

```json
{
  "jsonrpc": "2.0",
  "method": "initialize",
  "params": {
    "protocolVersion": "2025-06-18",
    "capabilities": {},
    "clientInfo": {
      "name": "your-client",
      "version": "1.0.0"
    }
  },
  "id": 0
}
```

### Message Format

All messages use **JSON-RPC 2.0**:

```json
{
  "jsonrpc": "2.0",          // Protocol version
  "method": "method_name",    // Method to call
  "params": {...},            // Parameters (optional)
  "id": 123                   // Request ID (for matching responses)
}
```

### Response Format

```json
{
  "jsonrpc": "2.0",
  "id": 123,                  // Matches request ID
  "result": {...}             // Success response
}

// OR

{
  "jsonrpc": "2.0",
  "id": 123,
  "error": {                  // Error response
    "code": -32600,
    "message": "Error description"
  }
}
```

## Using with Claude Desktop

### Configuration

Add to your Claude Desktop MCP configuration:

**macOS:** `~/Library/Application Support/Claude/claude_desktop_config.json`

**Linux:** `~/.config/Claude/claude_desktop_config.json`

```json
{
  "mcpServers": {
    "k8s-gpu-agent": {
      "command": "kubectl",
      "args": [
        "debug",
        "node/gpu-node-5",
        "--image=ghcr.io/arangogutierrez/k8s-gpu-mcp-server:latest",
        "--profile=sysadmin",
        "--",
        "/agent",
        "--mode=read-only",
        "--nvml-mode=real"
      ]
    }
  }
}
```

### Usage in Claude

Once configured, you can ask Claude:

```
You: "Check the GPU temperatures on node gpu-node-5"

Claude: [Calls get_gpu_inventory tool]
        
        "GPU 0 (Tesla T4) is at 29°C - within normal range.
         Memory usage is at 3% (447MB / 15GB).
         The GPU is idle (0% utilization)."
```

### Example Prompts

```
"What GPUs are available on this node?"
"Show me the memory usage for all GPUs"
"Is GPU 2 thermal throttling?"
"Check for XID errors on the GPUs" (M2 Phase 2)
"What's the NVLink topology?" (M2 Phase 4)
```

## Using with Cursor IDE

### Setup

1. Add to Cursor MCP configuration
2. Set agent as available tool
3. Use in AI chat

### Configuration

```json
{
  "mcp": {
    "k8s-gpu-agent": {
      "command": "/path/to/bin/agent",
      "args": ["--mode=read-only", "--nvml-mode=mock"]
    }
  }
}
```

### Usage

```typescript
// Ask Cursor AI:
// "Query the GPU inventory using k8s-gpu-mcp-server"

// Cursor will:
// 1. Initialize MCP session
// 2. Call get_gpu_inventory tool
// 3. Display results in chat
```

## Manual JSON-RPC

For debugging or custom clients:

### 1. Initialize Session

```bash
echo '{"jsonrpc":"2.0","method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"my-client","version":"1.0"}},"id":0}' | ./bin/agent
```

**Response:**
```json
{
  "jsonrpc": "2.0",
  "id": 0,
  "result": {
    "protocolVersion": "2025-06-18",
    "capabilities": {
      "tools": {
        "listChanged": true
      }
    },
    "serverInfo": {
      "name": "k8s-gpu-mcp-server",
      "version": "0.1.0-alpha"
    }
  }
}
```

### 2. List Available Tools

```bash
echo '{"jsonrpc":"2.0","method":"tools/list","params":{},"id":1}' | ./bin/agent
```

**Response:**
```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "tools": [
      {
        "name": "get_gpu_inventory",
        "description": "Returns hardware inventory and telemetry for all GPUs",
        "inputSchema": {"type": "object", "properties": {}}
      },
      {
        "name": "get_gpu_health",
        "description": "GPU health monitoring with scoring and recommendations",
        "inputSchema": {"type": "object", "properties": {}}
      },
      {
        "name": "analyze_xid_errors",
        "description": "Parse GPU XID error codes from kernel logs",
        "inputSchema": {"type": "object", "properties": {}}
      },
      {
        "name": "get_nvlink_topology",
        "description": "Get NVLink interconnect topology and health status",
        "inputSchema": {"type": "object", "properties": {}}
      },
      {
        "name": "describe_gpu_node",
        "description": "Comprehensive view of a GPU node with K8s metadata",
        "inputSchema": {
          "type": "object",
          "properties": {"node_name": {"type": "string"}},
          "required": ["node_name"]
        }
      },
      {
        "name": "get_pod_gpu_allocation",
        "description": "Shows GPU allocation for pods on a specific node",
        "inputSchema": {
          "type": "object",
          "properties": {
            "node_name": {"type": "string"},
            "namespace": {"type": "string"}
          },
          "required": ["node_name"]
        }
      },
      {
        "name": "explain_failure",
        "description": "Analyze why a GPU workload failed with root cause analysis",
        "inputSchema": {
          "type": "object",
          "properties": {
            "pod_name": {"type": "string"},
            "namespace": {"type": "string"}
          },
          "required": ["pod_name"]
        }
      },
      {
        "name": "get_incident_report",
        "description": "Get detailed incident report with timeline and hardware snapshots",
        "inputSchema": {
          "type": "object",
          "properties": {
            "incident_id": {"type": "string"},
            "pod_name": {"type": "string"},
            "namespace": {"type": "string"},
            "include_snapshots": {"type": "boolean"},
            "include_raw_events": {"type": "boolean"}
          }
        }
      },
      {
        "name": "get_gpu_timeline",
        "description": "Get historical GPU metrics from the flight recorder",
        "inputSchema": {
          "type": "object",
          "properties": {
            "gpu_uuid": {"type": "string"},
            "gpu_index": {"type": "number"},
            "duration": {"type": "string"},
            "include_stats": {"type": "boolean"}
          }
        }
      }
    ]
  }
}
```

### 3. Call a Tool

```bash
echo '{"jsonrpc":"2.0","method":"tools/call","params":{"name":"get_gpu_inventory","arguments":{}},"id":2}' | ./bin/agent --nvml-mode=real
```

**Response:**
```json
{
  "jsonrpc": "2.0",
  "id": 2,
  "result": {
    "content": [
      {
        "type": "text",
        "text": "{\"status\":\"success\",\"device_count\":1,\"devices\":[...]}"
      }
    ]
  }
}
```

## Available Tools

### XID Error Analysis Requirements

The `analyze_xid_errors` tool reads kernel logs to detect NVIDIA XID errors.
It uses two methods:

1. **`/dev/kmsg`** (preferred) - Direct kernel log access, works in
   distroless containers when `/dev/kmsg` is mounted from the host.

2. **`dmesg` command** (fallback) - Used when `/dev/kmsg` is not available.
   Requires a non-distroless container with `dmesg` installed.

**Helm configuration for XID analysis:**

```yaml
# values.yaml - XID error analysis configuration
xidAnalysis:
  enabled: true  # Mounts /dev/kmsg from host

securityContext:
  privileged: true  # Required to read /dev/kmsg
```

**Full Helm install with XID analysis enabled:**

```bash
helm install k8s-gpu-mcp-server \
  oci://ghcr.io/arangogutierrez/charts/k8s-gpu-mcp-server \
  --namespace gpu-diagnostics --create-namespace \
  --set xidAnalysis.enabled=true \
  --set securityContext.privileged=true
```

When deployed with the Helm chart, `/dev/kmsg` is mounted by default, enabling
XID error analysis in distroless containers without external dependencies.

**Note:** Reading `/dev/kmsg` requires privileged mode due to kernel security
restrictions (seccomp/AppArmor). The CAP_SYSLOG capability alone is not
sufficient in most Kubernetes deployments.

### get_gpu_inventory

**Purpose:** Get complete GPU hardware inventory with telemetry

**Arguments:** None

**Example:**
```json
{
  "jsonrpc": "2.0",
  "method": "tools/call",
  "params": {
    "name": "get_gpu_inventory",
    "arguments": {}
  },
  "id": 2
}
```

**Response:**
```json
{
  "status": "success",
  "device_count": 1,
  "devices": [
    {
      "Index": 0,
      "Name": "Tesla T4",
      "UUID": "GPU-d129fc5b-2d51-cec7-d985-49168c12716f",
      "BusID": "0000:00:1E.0",
      "MemoryTotal": 16106127360,
      "MemoryUsed": 469041152,
      "MemoryFree": 15637086208,
      "Temperature": 29,
      "PowerUsage": 13929,
      "GPUUtil": 0,
      "MemoryUtil": 0
    }
  ]
}
```

**Field Descriptions:**
- `MemoryTotal/Used/Free`: Bytes
- `Temperature`: Celsius
- `PowerUsage`: Milliwatts
- `GPUUtil`: Percentage (0-100)
- `MemoryUtil`: Percentage (0-100)

### get_gpu_health

**Purpose:** GPU health monitoring with scoring and recommendations

**Arguments:** None

**Example:**
```json
{
  "jsonrpc": "2.0",
  "method": "tools/call",
  "params": {
    "name": "get_gpu_health",
    "arguments": {}
  },
  "id": 3
}
```

**Response:**
```json
{
  "status": "success",
  "devices": [
    {
      "index": 0,
      "name": "Tesla T4",
      "uuid": "GPU-xxx",
      "health_score": 95,
      "health_status": "healthy",
      "checks": {
        "temperature": {"status": "ok", "value": 45, "threshold": 83},
        "power": {"status": "ok", "value": 70, "threshold": 70},
        "memory": {"status": "ok", "used_percent": 12},
        "ecc": {"status": "ok", "correctable": 0, "uncorrectable": 0}
      },
      "recommendations": []
    }
  ],
  "overall_health": "healthy"
}
```

### analyze_xid_errors

**Purpose:** Parse GPU XID error codes from kernel logs

**Arguments:** None (reads from `/dev/kmsg` or `dmesg`)

**Example:**
```json
{
  "jsonrpc": "2.0",
  "method": "tools/call",
  "params": {
    "name": "analyze_xid_errors",
    "arguments": {}
  },
  "id": 4
}
```

**Response (with errors):**
```json
{
  "status": "success",
  "errors": [
    {
      "timestamp": "2026-01-15T10:30:00Z",
      "xid": 48,
      "gpu_index": 0,
      "description": "Double Bit ECC Error",
      "severity": "critical",
      "recommendation": "GPU memory error detected. Consider draining workloads and replacing GPU."
    }
  ],
  "summary": {
    "total_errors": 1,
    "by_severity": {"critical": 1},
    "by_xid": {"48": 1}
  }
}
```

### describe_gpu_node

**Purpose:** Comprehensive view of a GPU node combining Kubernetes metadata
with NVML hardware data

**Arguments:**
- `node_name` (required): Node name to describe

**Example:**
```json
{
  "jsonrpc": "2.0",
  "method": "tools/call",
  "params": {
    "name": "describe_gpu_node",
    "arguments": {
      "node_name": "gpu-node-1"
    }
  },
  "id": 5
}
```

**Response:**
```json
{
  "status": "success",
  "node": {
    "name": "gpu-node-1",
    "labels": {
      "nvidia.com/gpu.product": "Tesla-T4",
      "nvidia.com/gpu.count": "1",
      "node.kubernetes.io/instance-type": "g4dn.xlarge"
    },
    "taints": [],
    "conditions": {
      "Ready": true,
      "MemoryPressure": false,
      "DiskPressure": false
    },
    "capacity": {
      "cpu": "4",
      "memory": "16Gi",
      "nvidia.com/gpu": "1"
    },
    "allocatable": {
      "cpu": "3920m",
      "memory": "15Gi",
      "nvidia.com/gpu": "1"
    }
  },
  "driver": {
    "version": "535.154.05",
    "cuda_version": "12.2"
  },
  "gpus": [
    {
      "index": 0,
      "name": "Tesla T4",
      "uuid": "GPU-xxx",
      "health_score": 95,
      "temperature": 45,
      "utilization": 0,
      "memory_used_percent": 3
    }
  ],
  "pods": [
    {
      "name": "training-job-abc",
      "namespace": "ml-workloads",
      "gpu_count": 1,
      "status": "Running"
    }
  ],
  "summary": {
    "total_gpus": 1,
    "allocated_gpus": 1,
    "available_gpus": 0,
    "overall_health": "healthy"
  }
}
```

**Use Case:** Get a complete picture of a GPU node for troubleshooting,
including hardware status, Kubernetes metadata, and running workloads.

### get_pod_gpu_allocation

**Purpose:** Shows GPU allocation for pods on a specific node

**Arguments:**
- `node_name` (required): Node name to query
- `namespace` (optional): Namespace filter (default: all namespaces)

**Example:**
```json
{
  "jsonrpc": "2.0",
  "method": "tools/call",
  "params": {
    "name": "get_pod_gpu_allocation",
    "arguments": {
      "node_name": "gpu-node-1",
      "namespace": "ml-workloads"
    }
  },
  "id": 6
}
```

**Response:**
```json
{
  "status": "success",
  "node_name": "gpu-node-1",
  "pods": [
    {
      "name": "training-job-abc",
      "namespace": "ml-workloads",
      "status": "Running",
      "node": "gpu-node-1",
      "containers": [
        {
          "name": "trainer",
          "gpu_request": 1,
          "gpu_limit": 1,
          "gpu_uuids": ["GPU-d129fc5b-2d51-cec7-d985-49168c12716f"]
        }
      ]
    }
  ],
  "summary": {
    "total_pods": 1,
    "total_gpus_allocated": 1
  }
}
```

**Use Case:** Correlate GPU hardware with Kubernetes workloads. Useful for
identifying which pods are using specific GPUs, debugging resource contention,
and capacity planning.

### get_nvlink_topology

**Purpose:** Get NVLink interconnect topology and health status for multi-GPU systems

**Arguments:** None

**Example:**
```json
{
  "jsonrpc": "2.0",
  "method": "tools/call",
  "params": {
    "name": "get_nvlink_topology",
    "arguments": {}
  },
  "id": 7
}
```

**Response:**
```json
{
  "gpu_count": 4,
  "links": [
    {
      "gpu1": 0,
      "gpu2": 1,
      "link_type": "NVLink",
      "active": true,
      "error_count": 0
    },
    {
      "gpu1": 0,
      "gpu2": 2,
      "link_type": "NVLink",
      "active": true,
      "error_count": 0
    }
  ],
  "summary": "4 GPUs with 2 NVLink connection(s): GPU 0↔GPU 1, GPU 0↔GPU 2",
  "health_status": "healthy"
}
```

**Field Descriptions:**
- `gpu_count`: Total GPU devices on the node
- `links`: Array of NVLink connections between GPU pairs
- `link_type`: Connection type (currently always "NVLink")
- `error_count`: Cumulative NVLink errors on the link
- `health_status`: Overall NVLink health: `healthy` (0 errors), `warning` (>0 errors), or `degraded` (>1000 errors)

**Use Case:** Diagnose silent NVLink degradation in multi-GPU training setups.
Useful for verifying GPU-to-GPU connectivity before large distributed training jobs.

### explain_failure

**Purpose:** Analyze why a GPU workload failed and provide human-readable
explanation with root cause analysis and actionable recommendations

**Arguments:**
- `pod_name` (required): Name of the failed pod
- `namespace` (optional): Kubernetes namespace (default: current namespace)

**Example:**
```json
{
  "jsonrpc": "2.0",
  "method": "tools/call",
  "params": {
    "name": "explain_failure",
    "arguments": {
      "pod_name": "training-job-abc",
      "namespace": "ml-workloads"
    }
  },
  "id": 8
}
```

**Response:**
```json
{
  "pod": {
    "name": "training-job-abc",
    "namespace": "ml-workloads",
    "node": "gpu-node-5",
    "container": "trainer"
  },
  "gpu": {
    "uuid": "GPU-d129fc5b-2d51-cec7-d985-49168c12716f",
    "index": 0
  },
  "root_cause": {
    "category": "gpu_hardware",
    "confidence": 0.85,
    "not_your_code": true,
    "evidence": ["XID 48 detected within 5s of pod failure", "Uncorrectable ECC error"]
  },
  "explanation": "The training job failed due to a GPU hardware error (XID 48: Double Bit ECC Error). This is not a code issue - the GPU has uncorrectable memory errors.",
  "timeline": [
    {"t": "0s", "event": "[BackOff] Back-off restarting failed container"},
    {"t": "-2s", "event": "XID 48 detected on GPU 0"}
  ],
  "recommendations": [
    {"action": "drain_node", "priority": "high", "description": "Drain workloads from this node"},
    {"action": "replace_gpu", "priority": "medium", "description": "Schedule GPU replacement"}
  ]
}
```

**Key Fields:**
- `not_your_code`: Boolean indicating whether the failure was caused by hardware (true) or user code (false)
- `confidence`: Root cause confidence score from 0.0 to 1.0
- `category`: Failure category (e.g., `gpu_hardware`, `oom`, `user_code`, `scheduling`)

**Use Case:** Quickly determine if a failed GPU workload is due to hardware issues
or application bugs. Reduces triage time for SREs.

### get_incident_report

**Purpose:** Get a detailed incident report with full timeline, hardware snapshots,
and correlated events. More detailed than `explain_failure`.

**Arguments:**
- `incident_id` (optional): Incident ID from a previous `explain_failure` call
- `pod_name` (optional): Pod name (alternative to `incident_id`). At least one of `incident_id` or `pod_name` is required
- `namespace` (optional): Kubernetes namespace
- `include_snapshots` (optional): Include full GPU telemetry snapshots (default: true)
- `include_raw_events` (optional): Include raw K8s and XID events (default: false)

**Example:**
```json
{
  "jsonrpc": "2.0",
  "method": "tools/call",
  "params": {
    "name": "get_incident_report",
    "arguments": {
      "pod_name": "training-job-abc",
      "namespace": "ml-workloads",
      "include_snapshots": true,
      "include_raw_events": false
    }
  },
  "id": 9
}
```

**Response:**
```json
{
  "incident_id": "report-1706000000000",
  "timestamp": "2026-01-23T10:30:00Z",
  "duration_analyzed": "30m",
  "pod": {
    "name": "training-job-abc",
    "namespace": "ml-workloads",
    "uid": "abc-123",
    "node": "gpu-node-5",
    "termination_reason": "Error"
  },
  "gpu": {
    "uuid": "GPU-d129fc5b-2d51-cec7-d985-49168c12716f"
  },
  "root_cause": {
    "category": "gpu_hardware",
    "confidence": 0.85,
    "not_your_code": true,
    "evidence": ["XID 48 detected within 5s of pod failure"]
  },
  "timeline": [
    {
      "timestamp": "2026-01-23T10:29:58Z",
      "relative": "-2s",
      "event_type": "xid",
      "severity": "critical",
      "description": "XID 48: Double Bit ECC Error on GPU 0"
    },
    {
      "timestamp": "2026-01-23T10:30:00Z",
      "relative": "0s",
      "event_type": "k8s",
      "severity": "critical",
      "description": "[BackOff] Back-off restarting failed container"
    }
  ],
  "gpu_snapshots": [
    {
      "timestamp": "2026-01-23T10:29:00Z",
      "temperature": 72,
      "power_mw": 250000,
      "gpu_util": 98,
      "mem_util": 85
    }
  ],
  "recommendations": [
    {"action": "drain_node", "priority": "high", "description": "Drain workloads from this node"}
  ],
  "explanation": "The training job failed due to a GPU hardware error..."
}
```

**Use Case:** Deep debugging, post-mortem analysis, or generating detailed
incident reports for escalation. The `gpu_snapshots` field provides historical
telemetry from the flight recorder around the time of failure.

### get_gpu_timeline

**Purpose:** Get historical GPU metrics for a specific time window from the
flight recorder. Returns time-series data for temperature, power, utilization,
and memory.

**Arguments:**
- `gpu_uuid` (optional): GPU UUID to query (defaults to all GPUs)
- `gpu_index` (optional): GPU device index (alternative to `gpu_uuid`)
- `duration` (optional): Time window to query (default: `30m`). Examples: `10m`, `1h`, `2h30m`. Maximum: `24h`
- `include_stats` (optional): Include min/max/avg statistics (default: true)

**Example:**
```json
{
  "jsonrpc": "2.0",
  "method": "tools/call",
  "params": {
    "name": "get_gpu_timeline",
    "arguments": {
      "gpu_index": 0,
      "duration": "1h",
      "include_stats": true
    }
  },
  "id": 10
}
```

**Response (single GPU):**
```json
{
  "gpu_uuid": "GPU-d129fc5b-2d51-cec7-d985-49168c12716f",
  "gpu_index": 0,
  "gpu_name": "Tesla T4",
  "duration": "1h0m0s",
  "sample_count": 60,
  "sample_interval": "1m0s",
  "data_points": [
    {
      "timestamp": "2026-01-23T09:30:00Z",
      "temperature_celsius": 45,
      "power_mw": 70000,
      "gpu_util_percent": 95,
      "mem_util_percent": 80,
      "mem_used_bytes": 12884901888,
      "mem_total_bytes": 16106127360,
      "throttling": false
    }
  ],
  "statistics": {
    "temperature": {"min": 42, "max": 78, "avg": 55.3},
    "power_mw": {"min": 15000, "max": 250000, "avg": 120000},
    "gpu_util": {"min": 0, "max": 100, "avg": 72.5},
    "mem_util": {"min": 10, "max": 95, "avg": 60.2}
  },
  "events": [
    {
      "timestamp": "2026-01-23T10:15:00Z",
      "type": "throttle_start",
      "description": "GPU throttling started"
    }
  ]
}
```

**Response (all GPUs, when no `gpu_uuid` or `gpu_index` specified):**
```json
{
  "gpu_count": 2,
  "duration": "30m0s",
  "timelines": [
    {"gpu_uuid": "GPU-xxx", "gpu_index": 0, "...": "..."},
    {"gpu_uuid": "GPU-yyy", "gpu_index": 1, "...": "..."}
  ]
}
```

**Key Fields:**
- `data_points`: Time-series GPU telemetry samples
- `statistics`: Aggregated min/max/avg for key metrics
- `events`: Throttling state changes and XID events detected in the window
- `sample_interval`: Time between consecutive data points

**Use Case:** Answer questions like "What was the GPU temperature 10 minutes ago?"
or "Show me the power usage trend over the last hour." The flight recorder must
be running for this tool to return data.

## Available Prompts

MCP Prompts provide guided diagnostic workflows. Unlike tools (which perform
single actions), prompts orchestrate multi-step workflows with contextual
instructions for AI assistants.

**Key Features:**
- **Context-aware**: Prompts respect context cancellation for graceful timeouts
- **Parameterized**: Arguments with sensible defaults for flexible invocation
- **Structured output**: Returns formatted workflow instructions for AI assistants

### Listing Prompts

```bash
echo '{"jsonrpc":"2.0","method":"prompts/list","params":{},"id":1}' | ./bin/agent --nvml-mode=mock 2>/dev/null
```

**Response:**
```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "prompts": [
      {
        "name": "gpu-health-check",
        "description": "Comprehensive GPU health assessment with recommendations",
        "arguments": [
          {"name": "node", "description": "Optional: specific node name", "required": false}
        ]
      },
      {
        "name": "diagnose-xid-errors",
        "description": "Analyze NVIDIA XID errors from kernel logs",
        "arguments": [
          {"name": "time_range", "description": "Time range (e.g., '24h')", "required": false}
        ]
      },
      {
        "name": "gpu-triage",
        "description": "Standard GPU triage workflow: inventory → health → XID analysis",
        "arguments": [
          {"name": "node", "description": "Node name to triage", "required": false},
          {"name": "incident_id", "description": "Optional incident ID", "required": false}
        ]
      }
    ]
  }
}
```

### Getting a Prompt

```bash
echo '{"jsonrpc":"2.0","method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}},"id":0}
{"jsonrpc":"2.0","method":"prompts/get","params":{"name":"gpu-health-check","arguments":{"node":"gpu-worker-1"}},"id":1}' | ./bin/agent --nvml-mode=mock 2>/dev/null
```

**Response:**
```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "description": "Comprehensive GPU health assessment with recommendations",
    "messages": [
      {
        "role": "user",
        "content": {
          "type": "text",
          "text": "## GPU Health Check Request\n\nPlease perform a comprehensive GPU health assessment on gpu-worker-1.\n\n### Workflow\n..."
        }
      }
    ]
  }
}
```

### gpu-health-check

**Purpose:** Comprehensive GPU health assessment

**Arguments:**
- `node` (optional): Specific node name to check. Default: "all nodes"

**Workflow:**
1. Inventory check using `get_gpu_inventory`
2. Health assessment using `get_gpu_health`
3. Analysis of thermal and memory status
4. Summary with recommendations

**Example:**
```json
{
  "jsonrpc": "2.0",
  "method": "prompts/get",
  "params": {
    "name": "gpu-health-check",
    "arguments": {"node": "gpu-node-5"}
  },
  "id": 1
}
```

### diagnose-xid-errors

**Purpose:** Analyze NVIDIA XID errors with remediation guidance

**Arguments:**
- `time_range` (optional): Time range to analyze. Default: "24h"

**Workflow:**
1. Error collection using `analyze_xid_errors`
2. Classification by XID code and severity
3. Root cause analysis
4. Remediation guidance

**Example:**
```json
{
  "jsonrpc": "2.0",
  "method": "prompts/get",
  "params": {
    "name": "diagnose-xid-errors",
    "arguments": {"time_range": "7d"}
  },
  "id": 1
}
```

### gpu-triage

**Purpose:** Standard SRE triage procedure

**Arguments:**
- `node` (optional): Node name to triage. Default: "cluster-wide"
- `incident_id` (optional): Incident/ticket ID for tracking

**Workflow:**
1. Hardware inventory
2. Health assessment
3. Error analysis
4. Workload correlation (if K8s available)
5. Triage decision based on findings

**Example:**
```json
{
  "jsonrpc": "2.0",
  "method": "prompts/get",
  "params": {
    "name": "gpu-triage",
    "arguments": {
      "node": "gpu-worker-42",
      "incident_id": "INC-12345"
    }
  },
  "id": 1
}
```

### Using Prompts with Claude Desktop

When prompts are available, Claude can execute guided workflows:

```
You: "Run the GPU triage workflow for node gpu-worker-5"

Claude: [Calls prompts/get for gpu-triage]
        [Follows the workflow steps]
        [Calls get_gpu_inventory, get_gpu_health, analyze_xid_errors]
        
        "## GPU Triage Report
         
         Node: gpu-worker-5
         Status: ⚠️ Degraded
         
         ### Findings:
         - GPU 0: Healthy (score: 98)
         - GPU 1: Warning (score: 75) - elevated temperature
         
         ### Recommendations:
         1. Monitor GPU 1 temperature
         2. Check cooling system
         ..."
```

## Error Handling

### Error Types

**1. Protocol Errors** (JSON-RPC level)
```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "error": {
    "code": -32700,
    "message": "Parse error"
  }
}
```

**Common codes:**
- `-32700`: Parse error (invalid JSON)
- `-32600`: Invalid request
- `-32601`: Method not found
- `-32602`: Invalid params

**2. Tool Errors** (Application level)
```json
{
  "jsonrpc": "2.0",
  "id": 2,
  "result": {
    "content": [
      {
        "type": "text",
        "text": "{\"error\":\"NVML not initialized\"}"
      }
    ],
    "isError": true
  }
}
```

### Handling Errors in Clients

```python
# Python example
response = call_mcp_tool("get_gpu_inventory", {})

if response.get("error"):
    # JSON-RPC protocol error
    print(f"Protocol error: {response['error']['message']}")
elif response.get("result", {}).get("isError"):
    # Tool execution error
    content = response["result"]["content"][0]["text"]
    print(f"Tool error: {content}")
else:
    # Success
    data = json.loads(response["result"]["content"][0]["text"])
    print(f"GPU Count: {data['device_count']}")
```

## Best Practices

### 1. Always Initialize

Send `initialize` before any other requests:

```bash
# ✅ Good
echo '{"jsonrpc":"2.0","method":"initialize",...}' | ./bin/agent
echo '{"jsonrpc":"2.0","method":"tools/call",...}' | ./bin/agent

# ❌ Bad - will fail
echo '{"jsonrpc":"2.0","method":"tools/call",...}' | ./bin/agent
```

### 2. Use Timeouts

The agent blocks on stdin. Always use timeouts:

```bash
# With timeout
timeout 30s ./bin/agent --nvml-mode=real < requests.jsonl

# In Python
process = subprocess.Popen(..., stdin=PIPE, stdout=PIPE, timeout=30)
```

### 3. Handle Stdio Separation

```bash
# ✅ Good: Separate stdout (protocol) and stderr (logs)
./bin/agent 2>agent.log | process_responses.sh

# ✅ Good: View logs separately
cat agent.log | jq .

# ❌ Bad: Mixing stdout and stderr
./bin/agent 2>&1  # Logs corrupt JSON-RPC responses
```

### 4. Validate Responses

Always check for both JSON-RPC errors and tool errors:

```javascript
const response = JSON.parse(stdout);

if (response.error) {
  // Protocol error
  throw new Error(`MCP Error: ${response.error.message}`);
}

if (response.result?.isError) {
  // Tool error
  const errorText = response.result.content[0].text;
  throw new Error(`Tool Error: ${errorText}`);
}

// Success
const data = JSON.parse(response.result.content[0].text);
```

### 5. Use Mock for Development

```bash
# During development, use mock mode
./bin/agent --nvml-mode=mock

# Switch to real for production
./bin/agent --nvml-mode=real
```

## Integration Examples

### Python Client

```python
import json
import subprocess

def call_mcp_tool(tool_name, arguments):
    """Call an MCP tool and return the result."""
    
    # Start agent process
    proc = subprocess.Popen(
        ['./bin/agent', '--nvml-mode=real'],
        stdin=subprocess.PIPE,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True
    )
    
    # Initialize
    init_req = {
        "jsonrpc": "2.0",
        "method": "initialize",
        "params": {
            "protocolVersion": "2025-06-18",
            "capabilities": {},
            "clientInfo": {"name": "python-client", "version": "1.0"}
        },
        "id": 0
    }
    proc.stdin.write(json.dumps(init_req) + '\n')
    proc.stdin.flush()
    
    # Read init response
    init_response = json.loads(proc.stdout.readline())
    
    # Call tool
    tool_req = {
        "jsonrpc": "2.0",
        "method": "tools/call",
        "params": {
            "name": tool_name,
            "arguments": arguments
        },
        "id": 1
    }
    proc.stdin.write(json.dumps(tool_req) + '\n')
    proc.stdin.flush()
    
    # Read tool response
    tool_response = json.loads(proc.stdout.readline())
    
    proc.terminate()
    
    return tool_response

# Usage
response = call_mcp_tool("get_gpu_inventory", {})
data = json.loads(response["result"]["content"][0]["text"])
print(f"Found {data['device_count']} GPUs")
```

### Bash Client

```bash
#!/bin/bash
# mcp_client.sh - Simple MCP client in bash

AGENT="./bin/agent"
MODE="--nvml-mode=real"

# Function to call MCP tool
call_tool() {
    local tool_name="$1"
    local args="$2"
    
    {
        # Initialize
        echo "{\"jsonrpc\":\"2.0\",\"method\":\"initialize\",\"params\":{\"protocolVersion\":\"2025-06-18\",\"capabilities\":{},\"clientInfo\":{\"name\":\"bash-client\",\"version\":\"1.0\"}},\"id\":0}"
        
        # Call tool
        echo "{\"jsonrpc\":\"2.0\",\"method\":\"tools/call\",\"params\":{\"name\":\"$tool_name\",\"arguments\":$args},\"id\":1}"
    } | $AGENT $MODE 2>/tmp/agent.log | tail -1
}

# Usage
result=$(call_tool "get_gpu_inventory" "{}")
echo "$result" | jq -r '.result.content[0].text' | jq .
```

### Go Client

```go
package main

import (
    "bufio"
    "encoding/json"
    "fmt"
    "os/exec"
)

type MCPClient struct {
    cmd    *exec.Cmd
    stdin  io.WriteCloser
    stdout io.ReadCloser
}

func NewMCPClient(nvmlMode string) (*MCPClient, error) {
    cmd := exec.Command("./bin/agent", "--nvml-mode="+nvmlMode)
    
    stdin, _ := cmd.StdinPipe()
    stdout, _ := cmd.StdoutPipe()
    
    if err := cmd.Start(); err != nil {
        return nil, err
    }
    
    client := &MCPClient{cmd: cmd, stdin: stdin, stdout: stdout}
    
    // Initialize
    initReq := map[string]interface{}{
        "jsonrpc": "2.0",
        "method":  "initialize",
        "params": map[string]interface{}{
            "protocolVersion": "2025-06-18",
            "capabilities":    map[string]interface{}{},
            "clientInfo": map[string]interface{}{
                "name": "go-client", "version": "1.0",
            },
        },
        "id": 0,
    }
    
    json.NewEncoder(client.stdin).Encode(initReq)
    
    // Read init response
    scanner := bufio.NewScanner(client.stdout)
    scanner.Scan()
    
    return client, nil
}

func (c *MCPClient) CallTool(name string, args map[string]interface{}) (map[string]interface{}, error) {
    req := map[string]interface{}{
        "jsonrpc": "2.0",
        "method":  "tools/call",
        "params": map[string]interface{}{
            "name":      name,
            "arguments": args,
        },
        "id": 1,
    }
    
    json.NewEncoder(c.stdin).Encode(req)
    
    scanner := bufio.NewScanner(c.stdout)
    scanner.Scan()
    
    var response map[string]interface{}
    json.Unmarshal(scanner.Bytes(), &response)
    
    return response, nil
}

// Usage
client, _ := NewMCPClient("real")
response, _ := client.CallTool("get_gpu_inventory", map[string]interface{}{})
fmt.Println(response)
```

## Advanced Usage

### Streaming Multiple Requests

Send multiple tool calls in sequence:

```bash
{
  echo '{"jsonrpc":"2.0","method":"initialize",...,"id":0}'
  echo '{"jsonrpc":"2.0","method":"tools/call","params":{"name":"get_gpu_inventory",...},"id":1}'
  echo '{"jsonrpc":"2.0","method":"tools/call","params":{"name":"get_gpu_health",...},"id":2}'
} | ./bin/agent --nvml-mode=real
```

Each request gets a response with matching `id`.

### Context and Cancellation

The agent respects context cancellation:

```bash
# Agent stops gracefully on SIGINT/SIGTERM
./bin/agent & 
PID=$!

# Send requests...

# Stop agent
kill $PID  # Graceful shutdown
```

### Logging

Logs go to stderr in structured JSON format:

```bash
# Capture logs separately
./bin/agent --log-level=debug 2>agent.log | your_client.sh

# View logs
cat agent.log | jq '.level,.msg,.tool'
```

**Log Levels:**
- `debug`: Verbose (all tool calls, parameters)
- `info`: Normal (tool invocations, completion)
- `warn`: Warnings (slow operations, retries)
- `error`: Errors (NVML failures, invalid requests)

## Debugging

### Enable Debug Logging

```bash
./bin/agent --log-level=debug --nvml-mode=mock 2>&1 | tee debug.log
```

### Validate JSON-RPC Messages

```bash
# Pretty-print requests
echo '{"jsonrpc":"2.0",...}' | jq . | ./bin/agent

# Validate responses
./bin/agent < requests.jsonl | jq .
```

### Test Protocol Compliance

```bash
# Use MCP Inspector (if available)
mcp-inspector ./bin/agent --nvml-mode=mock

# Or manual validation
./bin/agent < test_requests.jsonl > responses.jsonl
cat responses.jsonl | jq -e '.jsonrpc == "2.0"'
```

## References

- [Model Context Protocol Specification](https://modelcontextprotocol.io/)
- [JSON-RPC 2.0 Specification](https://www.jsonrpc.org/specification)
- [Claude Desktop MCP Configuration](https://docs.anthropic.com/claude/docs/model-context-protocol)
- [Project Examples](../examples/)

