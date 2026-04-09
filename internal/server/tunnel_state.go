package server

import (
	"fmt"

	"netsgo/pkg/protocol"
)

func canonicalDesiredState(desiredState string) string {
	if desiredState == protocol.ProxyDesiredStatePaused {
		return protocol.ProxyDesiredStateStopped
	}
	return desiredState
}

func tunnelErrorForRuntimeState(runtimeState, errMsg string) string {
	if runtimeState == protocol.ProxyRuntimeStateError {
		return errMsg
	}
	return ""
}

func validateTunnelStates(desiredState, runtimeState, errMsg string) error {
	desiredState = canonicalDesiredState(desiredState)
	switch desiredState {
	case protocol.ProxyDesiredStateRunning:
		switch runtimeState {
		case protocol.ProxyRuntimeStatePending,
			protocol.ProxyRuntimeStateExposed,
			protocol.ProxyRuntimeStateOffline,
			protocol.ProxyRuntimeStateError:
		default:
			return fmt.Errorf("running desired_state does not support runtime_state=%q", runtimeState)
		}
	case protocol.ProxyDesiredStatePaused, protocol.ProxyDesiredStateStopped:
		if runtimeState != protocol.ProxyRuntimeStateIdle {
			return fmt.Errorf("desired_state=%q only allows runtime_state=idle, got %q", desiredState, runtimeState)
		}
	default:
		return fmt.Errorf("unknown desired_state=%q", desiredState)
	}

	if runtimeState != protocol.ProxyRuntimeStateError && errMsg != "" {
		return fmt.Errorf("error must be empty when runtime_state=%q", runtimeState)
	}

	return nil
}

func mustValidateTunnelStates(desiredState, runtimeState, errMsg string) {
	if err := validateTunnelStates(desiredState, runtimeState, errMsg); err != nil {
		panic(err)
	}
}

func setProxyConfigStates(config *protocol.ProxyConfig, desiredState, runtimeState, errMsg string) {
	desiredState = canonicalDesiredState(desiredState)
	mustValidateTunnelStates(desiredState, runtimeState, errMsg)
	config.DesiredState = desiredState
	config.RuntimeState = runtimeState
	config.Error = tunnelErrorForRuntimeState(runtimeState, errMsg)
}

func setStoredTunnelStates(tunnel *StoredTunnel, desiredState, runtimeState, errMsg string) {
	desiredState = canonicalDesiredState(desiredState)
	mustValidateTunnelStates(desiredState, runtimeState, errMsg)
	tunnel.DesiredState = desiredState
	tunnel.RuntimeState = runtimeState
	tunnel.Error = tunnelErrorForRuntimeState(runtimeState, errMsg)
}

func isTunnelExposed(config protocol.ProxyConfig) bool {
	return config.DesiredState == protocol.ProxyDesiredStateRunning &&
		config.RuntimeState == protocol.ProxyRuntimeStateExposed
}

func isTunnelOffline(config protocol.ProxyConfig) bool {
	return config.DesiredState == protocol.ProxyDesiredStateRunning &&
		config.RuntimeState == protocol.ProxyRuntimeStateOffline
}

func canPauseTunnel(config protocol.ProxyConfig) bool {
	return isTunnelExposed(config) || isTunnelOffline(config)
}

func canResumeTunnel(config protocol.ProxyConfig) bool {
	desiredState := canonicalDesiredState(config.DesiredState)
	return (desiredState == protocol.ProxyDesiredStateStopped && config.RuntimeState == protocol.ProxyRuntimeStateIdle) ||
		(config.DesiredState == protocol.ProxyDesiredStateRunning && config.RuntimeState == protocol.ProxyRuntimeStateError)
}

func canEditOrDeleteLiveTunnel(config protocol.ProxyConfig) bool {
	desiredState := canonicalDesiredState(config.DesiredState)
	return (desiredState == protocol.ProxyDesiredStateStopped && config.RuntimeState == protocol.ProxyRuntimeStateIdle) ||
		(config.DesiredState == protocol.ProxyDesiredStateRunning && config.RuntimeState == protocol.ProxyRuntimeStateError)
}

func computeTunnelCapabilities(config protocol.ProxyConfig) *protocol.TunnelCapabilities {
	canPause := false
	canResume := canResumeTunnel(config)
	canStop := isTunnelExposed(config) || isTunnelOffline(config)
	canEdit := canEditOrDeleteLiveTunnel(config) || isTunnelOffline(config)
	canDelete := config.RuntimeState != protocol.ProxyRuntimeStatePending
	return &protocol.TunnelCapabilities{
		CanPause:  canPause,
		CanResume: canResume,
		CanStop:   canStop,
		CanEdit:   canEdit,
		CanDelete: canDelete,
	}
}

func proxyConfigForClientView(config protocol.ProxyConfig, clientOnline bool) protocol.ProxyConfig {
	normalized := config
	setProxyConfigStates(&normalized, normalized.DesiredState, normalized.RuntimeState, normalized.Error)
	if !clientOnline &&
		normalized.DesiredState == protocol.ProxyDesiredStateRunning &&
		normalized.RuntimeState != protocol.ProxyRuntimeStateError {
		normalized.RuntimeState = protocol.ProxyRuntimeStateOffline
		normalized.Error = ""
	}
	normalized.Capabilities = computeTunnelCapabilities(normalized)
	return normalized
}
