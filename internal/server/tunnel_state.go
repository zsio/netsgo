package server

import "netsgo/pkg/protocol"

func tunnelErrorForRuntimeState(runtimeState, errMsg string) string {
	if runtimeState == protocol.ProxyRuntimeStateError {
		return errMsg
	}
	return ""
}

func normalizeProxyConfigState(config *protocol.ProxyConfig) {
	desiredState, runtimeState := protocol.NormalizeProxyStates(config.Status, config.DesiredState, config.RuntimeState)
	config.DesiredState = desiredState
	config.RuntimeState = runtimeState
	config.Status = protocol.LegacyProxyStatusFromStates(desiredState, runtimeState)
	config.Error = tunnelErrorForRuntimeState(runtimeState, config.Error)
}

func setProxyConfigLegacyStatus(config *protocol.ProxyConfig, status, errMsg string) {
	config.Status = status
	config.DesiredState = ""
	config.RuntimeState = ""
	config.Error = errMsg
	normalizeProxyConfigState(config)
}

func normalizeStoredTunnelState(tunnel *StoredTunnel) {
	desiredState, runtimeState := protocol.NormalizeProxyStates(tunnel.Status, tunnel.DesiredState, tunnel.RuntimeState)
	tunnel.DesiredState = desiredState
	tunnel.RuntimeState = runtimeState
	tunnel.Status = protocol.LegacyProxyStatusFromStates(desiredState, runtimeState)
	tunnel.Error = tunnelErrorForRuntimeState(runtimeState, tunnel.Error)
}

func setStoredTunnelLegacyStatus(tunnel *StoredTunnel, status, errMsg string) {
	tunnel.Status = status
	tunnel.DesiredState = ""
	tunnel.RuntimeState = ""
	tunnel.Error = errMsg
	normalizeStoredTunnelState(tunnel)
}

func setStoredTunnelStates(tunnel *StoredTunnel, desiredState, runtimeState, errMsg string) {
	tunnel.DesiredState = desiredState
	tunnel.RuntimeState = runtimeState
	tunnel.Status = ""
	tunnel.Error = errMsg
	normalizeStoredTunnelState(tunnel)
}

func proxyConfigForClientView(config protocol.ProxyConfig, clientOnline bool) protocol.ProxyConfig {
	normalized := config
	normalizeProxyConfigState(&normalized)
	if !clientOnline &&
		normalized.DesiredState == protocol.ProxyDesiredStateRunning &&
		normalized.RuntimeState != protocol.ProxyRuntimeStateError {
		normalized.RuntimeState = protocol.ProxyRuntimeStateOffline
		normalized.Status = protocol.LegacyProxyStatusFromStates(normalized.DesiredState, normalized.RuntimeState)
		normalized.Error = ""
	}
	return normalized
}
