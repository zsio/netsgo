//go:build e2e_capability_loss

package protocol

func init() {
	defaultClientCapabilities = func() ClientCapabilities {
		caps := productionDefaultClientCapabilities()
		caps.TargetTypes = removeCapabilityString(caps.TargetTypes, TargetTypeTCPService)
		return caps
	}
}

func removeCapabilityString(values []string, remove string) []string {
	filtered := values[:0]
	for _, value := range values {
		if value != remove {
			filtered = append(filtered, value)
		}
	}
	return filtered
}
