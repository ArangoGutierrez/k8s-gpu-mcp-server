// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

// Package incidents provides incident analysis and human-readable explanation
// generation for GPU failures.
//
// The package has two main components:
//
// # Analyzer
//
// The Analyzer performs pattern-based root cause analysis on CorrelatedIncident
// data, producing an IncidentReport with confidence scoring.
//
//	analyzer := incidents.NewAnalyzer()
//	report := analyzer.Analyze(incident)
//	fmt.Printf("Root cause: %s (confidence: %.0f%%)\n",
//	    report.RootCause.Category, report.RootCause.Confidence*100)
//
// Known failure patterns include:
//   - thermal_cascade: GPU overheating cascade (temp → throttle → XID)
//   - ecc_failure: Uncorrectable ECC memory errors
//   - software_oom: GPU memory exhaustion (user code issue)
//   - nvlink_failure: NVLink interconnect errors
//   - xid_79_bus_error: GPU fell off PCIe bus
//
// # Explainer
//
// The Explainer transforms structured CorrelatedIncident data from pkg/events
// into plain-English explanations suitable for DevOps teams and AI agents.
//
//	explainer := incidents.NewExplainer()
//	explanation := explainer.GenerateExplanation(incident)
//	fmt.Println(explanation)
//
// # Integration
//
// The Analyzer and Explainer can be used together:
//
//	analyzer := incidents.NewAnalyzer()
//	explainer := incidents.NewExplainer()
//
//	// Get structured analysis
//	report := analyzer.Analyze(incident)
//
//	// Get human-readable explanation
//	explanation := explainer.GenerateExplanation(incident)
//
// # Key Features
//
//   - Pattern matching with weighted indicators
//   - Confidence scoring for root cause determination
//   - Hardware vs software failure attribution ("not your code")
//   - Template-based explanation generation
//   - Actionable recommendations with kubectl commands
package incidents
