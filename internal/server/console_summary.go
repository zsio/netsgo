package server

import "netsgo/pkg/protocol"

type consoleSummaryView struct {
	TotalClients    int `json:"total_clients"`
	OnlineClients   int `json:"online_clients"`
	OfflineClients  int `json:"offline_clients"`
	TotalTunnels    int `json:"total_tunnels"`
	ActiveTunnels   int `json:"active_tunnels"`
	InactiveTunnels int `json:"inactive_tunnels"`
	PendingTunnels  int `json:"pending_tunnels"`
	OfflineTunnels  int `json:"offline_tunnels"`
	PausedTunnels   int `json:"paused_tunnels"`
	StoppedTunnels  int `json:"stopped_tunnels"`
	ErrorTunnels    int `json:"error_tunnels"`
}

type consoleData struct {
	Clients []clientView
	Summary consoleSummaryView
}

func (s *Server) collectConsoleData() consoleData {
	clients := s.collectClientViews()
	return consoleData{
		Clients: clients,
		Summary: summarizeConsoleClients(clients),
	}
}

func summarizeConsoleClients(clients []clientView) consoleSummaryView {
	summary := consoleSummaryView{}
	for _, client := range clients {
		summary.TotalClients++
		if client.Online {
			summary.OnlineClients++
		} else {
			summary.OfflineClients++
		}

		for _, tunnel := range client.Proxies {
			summary.TotalTunnels++
			switch consoleTunnelStatusKey(tunnel, client.Online) {
			case protocol.ProxyRuntimeStateExposed:
				summary.ActiveTunnels++
			case protocol.ProxyRuntimeStatePending:
				summary.PendingTunnels++
			case protocol.ProxyRuntimeStateOffline:
				summary.OfflineTunnels++
			case "paused":
				summary.PausedTunnels++
			case "stopped":
				summary.StoppedTunnels++
			default:
				summary.ErrorTunnels++
			}
		}
	}
	summary.InactiveTunnels = summary.TotalTunnels - summary.ActiveTunnels
	return summary
}

func consoleTunnelStatusKey(tunnel protocol.ProxyConfig, clientOnline bool) string {
	runtimeState := tunnel.RuntimeState
	if !clientOnline && tunnel.DesiredState == protocol.ProxyDesiredStateRunning && runtimeState != protocol.ProxyRuntimeStateError {
		runtimeState = protocol.ProxyRuntimeStateOffline
	}

	switch runtimeState {
	case protocol.ProxyRuntimeStatePending,
		protocol.ProxyRuntimeStateExposed,
		protocol.ProxyRuntimeStateOffline,
		protocol.ProxyRuntimeStateError:
		return runtimeState
	case protocol.ProxyRuntimeStateIdle:
		if tunnel.DesiredState == protocol.ProxyDesiredStatePaused {
			return "paused"
		}
		return "stopped"
	default:
		return protocol.ProxyRuntimeStateError
	}
}
