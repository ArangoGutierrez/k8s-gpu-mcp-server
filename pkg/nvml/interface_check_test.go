// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

package nvml

// Compile-time interface satisfaction checks for all Interface implementations.
// These ensure that adding a method to the Interface or Device interfaces
// produces a compile error until all implementations are updated.
//
// Source files already contain checks (real.go, real_stub.go, mock.go,
// unimplemented.go). This file consolidates them in the test package as
// an additional safety net.
var (
	// Interface implementations
	_ Interface = (*Real)(nil)
	_ Interface = (*Mock)(nil)
	_ Interface = UnimplementedInterface{}

	// Device implementations
	_ Device = (*RealDevice)(nil)
	_ Device = (*MockDevice)(nil)
	_ Device = UnimplementedDevice{}
)
