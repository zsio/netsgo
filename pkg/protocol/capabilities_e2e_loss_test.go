//go:build e2e_capability_loss

package protocol

import "testing"

func TestE2ECapabilityLossBuildOmitsTCPServiceTarget(t *testing.T) {
	caps := DefaultClientCapabilities()
	if containsString(caps.TargetTypes, TargetTypeTCPService) {
		t.Fatalf("e2e capability-loss build must omit %q target: %+v", TargetTypeTCPService, caps.TargetTypes)
	}
	if !containsString(caps.TargetTypes, TargetTypeSOCKS5ConnectHandler) {
		t.Fatalf("e2e capability-loss build should keep %q target: %+v", TargetTypeSOCKS5ConnectHandler, caps.TargetTypes)
	}
}
