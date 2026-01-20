// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

// Package incidents provides incident analysis and human-readable explanation
// generation for GPU failures.
//
// The Explainer transforms structured CorrelatedIncident data from pkg/events
// into plain-English explanations suitable for DevOps teams and AI agents.
//
// # Key Features
//
//   - Template-based explanation generation
//   - Root cause attribution ("not your code" vs software issue)
//   - Chronological timeline formatting
//   - Actionable recommendations with kubectl commands
//
// # Usage
//
//	explainer := incidents.NewExplainer()
//	explanation := explainer.GenerateExplanation(incident)
//	fmt.Println(explanation)
//
// # Templates
//
// The package includes templates for common failure patterns:
//   - hardware_thermal: GPU overheating and thermal throttling
//   - hardware_memory: ECC errors and memory corruption
//   - software_oom: GPU memory exhaustion (user code issue)
//   - unknown: No pattern matched
package incidents
