package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"

	"netsgo/pkg/protocol"
)

var (
	errManagedTunnelClientNotFound = errors.New("managed tunnel client not found")
	errManagedTunnelNotFound       = errors.New("managed tunnel not found")
)

func tunnelProvisionErrorMessage(err error) string {
	var rejected *tunnelProvisionRejectedError
	switch {
	case errors.As(err, &rejected):
		if rejected.message != "" {
			return rejected.message
		}
		return "client 拒绝隧道初始化"
	case errors.Is(err, errTunnelProvisionAckTimeout):
		return "等待 client provisioning ack 超时"
	case errors.Is(err, errTunnelProvisionAckCancelled):
		return "逻辑会话已失效"
	default:
		return err.Error()
	}
}

func (s *Server) markPendingTunnelErrorIfCurrent(client *ClientConn, name, message string) {
	if !s.isCurrentGeneration(client.ID, client.generation) {
		return
	}
	config, ok := s.setTunnelStates(client, name, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateError, message)
	if !ok {
		return
	}
	_ = s.persistTunnelStates(client.ID, name, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateError, message)
	s.emitTunnelChanged(client.ID, config, "error")
	_ = s.notifyClientProxyClose(client, name, "provision_failed")
}

func (s *Server) createManagedTunnel(client *ClientConn, req protocol.ProxyNewRequest, persist bool, action string) (protocol.ProxyConfig, error) {
	tunnel, err := s.prepareProxyTunnel(client, req, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStatePending)
	if err != nil {
		return protocol.ProxyConfig{}, err
	}
	s.emitTunnelChanged(client.ID, tunnel.Config, "pending")

	if _, err := s.waitForTunnelProvisionAck(client, tunnel.Config.ToProxyNewRequest()); err != nil {
		if s.isCurrentGeneration(client.ID, client.generation) {
			s.removeTunnelRuntime(client, req.Name)
			_ = s.notifyClientProxyClose(client, req.Name, "provision_failed")
		}
		return protocol.ProxyConfig{}, err
	}

	if !s.isCurrentGeneration(client.ID, client.generation) {
		return protocol.ProxyConfig{}, errTunnelProvisionAckCancelled
	}

	if err := s.activatePreparedTunnel(client, tunnel); err != nil {
		if s.isCurrentGeneration(client.ID, client.generation) {
			s.removeTunnelRuntime(client, req.Name)
			_ = s.notifyClientProxyClose(client, req.Name, "provision_failed")
		}
		return protocol.ProxyConfig{}, err
	}

	updated, err := s.mustGetTunnel(client, req.Name)
	if err != nil {
		s.removeTunnelRuntime(client, req.Name)
		return protocol.ProxyConfig{}, err
	}

	if persist && s.store != nil {
		if err := s.store.AddTunnel(storedTunnelFromRuntime(client, updated)); err != nil {
			s.removeTunnelRuntime(client, req.Name)
			_ = s.notifyClientProxyClose(client, req.Name, "provision_failed")
			return protocol.ProxyConfig{}, err
		}
	}

	s.emitTunnelChanged(client.ID, updated.Config, action)
	return updated.Config, nil
}

func (s *Server) createOfflineManagedTunnel(clientID string, req protocol.ProxyNewRequest) (protocol.ProxyConfig, error) {
	if s.adminStore == nil {
		return protocol.ProxyConfig{}, errManagedTunnelClientNotFound
	}
	record, ok := s.adminStore.GetRegisteredClient(clientID)
	if !ok {
		return protocol.ProxyConfig{}, errManagedTunnelClientNotFound
	}
	if s.store == nil {
		return protocol.ProxyConfig{}, fmt.Errorf("隧道存储未初始化")
	}

	if req.Type == protocol.ProxyTypeHTTP {
		req.RemotePort = 0
	}
	if err := s.validateProxyRequestWithExclusions(nil, req, "", clientID); err != nil {
		return protocol.ProxyConfig{}, err
	}

	stored := StoredTunnel{
		ProxyNewRequest: req,
		ClientID:        clientID,
		Hostname:        record.Info.Hostname,
		Binding:         TunnelBindingClientID,
	}
	setStoredTunnelStates(&stored, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateOffline, "")
	if err := s.store.AddTunnel(stored); err != nil {
		return protocol.ProxyConfig{}, err
	}

	config := storedTunnelToProxyConfig(stored)
	s.emitTunnelChanged(clientID, config, "created")
	return config, nil
}

func (s *Server) pauseManagedTunnel(client *ClientConn, name string) error {
	tunnel, err := s.mustGetTunnel(client, name)
	if err != nil {
		return err
	}

	previousDesired := tunnel.Config.DesiredState
	previousRuntime := tunnel.Config.RuntimeState
	previousError := tunnel.Config.Error
	if err := s.PauseProxy(client, name); err != nil {
		return err
	}
	if _, ok := s.setTunnelStates(client, name, protocol.ProxyDesiredStatePaused, protocol.ProxyRuntimeStateIdle, ""); !ok {
		return fmt.Errorf("隧道 %q 不存在", name)
	}

	if err := s.persistTunnelStates(client.ID, name, protocol.ProxyDesiredStatePaused, protocol.ProxyRuntimeStateIdle, ""); err != nil {
		_, _ = s.setTunnelStates(client, name, previousDesired, previousRuntime, previousError)
		_ = s.ResumeProxy(client, name)
		return err
	}

	if err := s.notifyClientProxyClose(client, name, "paused"); err != nil {
		_ = s.persistTunnelStates(client.ID, name, previousDesired, previousRuntime, previousError)
		_, _ = s.setTunnelStates(client, name, previousDesired, previousRuntime, previousError)
		_ = s.ResumeProxy(client, name)
		return err
	}

	updated, err := s.mustGetTunnel(client, name)
	if err != nil {
		return err
	}
	s.emitTunnelChanged(client.ID, updated.Config, "paused")
	return nil
}

func (s *Server) resumeManagedTunnel(client *ClientConn, name string) error {
	tunnel, err := s.mustGetTunnel(client, name)
	if err != nil {
		return err
	}

	previousDesired := tunnel.Config.DesiredState
	previousRuntime := tunnel.Config.RuntimeState
	previousError := tunnel.Config.Error
	pendingConfig, err := s.stageTunnelPending(client, name)
	if err != nil {
		return err
	}
	if err := s.persistTunnelStates(client.ID, name, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStatePending, ""); err != nil {
		_, _ = s.setTunnelStates(client, name, previousDesired, previousRuntime, previousError)
		return err
	}
	s.emitTunnelChanged(client.ID, pendingConfig, "pending")

	if _, err := s.waitForTunnelProvisionAck(client, pendingConfig.ToProxyNewRequest()); err != nil {
		if errors.Is(err, errTunnelProvisionAckCancelled) {
			return err
		}
		s.markPendingTunnelErrorIfCurrent(client, name, tunnelProvisionErrorMessage(err))
		return err
	}

	if !s.isCurrentGeneration(client.ID, client.generation) {
		return errTunnelProvisionAckCancelled
	}

	if err := s.ResumeProxy(client, name); err != nil {
		s.rollbackResumedTunnelAfterReady(client, name, previousDesired, previousRuntime, previousError)
		return err
	}

	if err := s.persistTunnelStates(client.ID, name, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateExposed, ""); err != nil {
		s.rollbackResumedTunnelAfterReady(client, name, previousDesired, previousRuntime, previousError)
		return err
	}
	if _, ok := s.setTunnelStates(client, name, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateExposed, ""); !ok {
		s.rollbackResumedTunnelAfterReady(client, name, previousDesired, previousRuntime, previousError)
		return fmt.Errorf("隧道 %q 不存在", name)
	}

	updated, err := s.mustGetTunnel(client, name)
	if err != nil {
		s.rollbackResumedTunnelAfterReady(client, name, previousDesired, previousRuntime, previousError)
		return err
	}

	s.emitTunnelChanged(client.ID, updated.Config, "resumed")
	return nil
}

func (s *Server) stopManagedTunnel(client *ClientConn, name string) error {
	tunnel, err := s.mustGetTunnel(client, name)
	if err != nil {
		return err
	}

	previousDesired := tunnel.Config.DesiredState
	previousRuntime := tunnel.Config.RuntimeState
	previousError := tunnel.Config.Error
	pausedDuringStop := false
	if isTunnelExposed(tunnel.Config) {
		if err := s.PauseProxy(client, name); err != nil {
			return err
		}
		pausedDuringStop = true
	}
	if _, ok := s.setTunnelStates(client, name, protocol.ProxyDesiredStateStopped, protocol.ProxyRuntimeStateIdle, ""); !ok {
		return fmt.Errorf("隧道 %q 不存在", name)
	}
	if err := s.persistTunnelStates(client.ID, name, protocol.ProxyDesiredStateStopped, protocol.ProxyRuntimeStateIdle, ""); err != nil {
		_, _ = s.setTunnelStates(client, name, previousDesired, previousRuntime, previousError)
		if pausedDuringStop {
			_ = s.ResumeProxy(client, name)
		}
		return err
	}

	if pausedDuringStop {
		if err := s.notifyClientProxyClose(client, name, "stopped"); err != nil {
			_ = s.persistTunnelStates(client.ID, name, previousDesired, previousRuntime, previousError)
			_, _ = s.setTunnelStates(client, name, previousDesired, previousRuntime, previousError)
			_ = s.ResumeProxy(client, name)
			return err
		}
	}

	updated, err := s.mustGetTunnel(client, name)
	if err != nil {
		return err
	}
	s.emitTunnelChanged(client.ID, updated.Config, "stopped")
	return nil
}

func (s *Server) deleteManagedTunnel(client *ClientConn, name string) error {
	tunnel, err := s.mustGetTunnel(client, name)
	if err != nil {
		return err
	}

	client.proxyMu.Lock()
	delete(client.proxies, name)
	client.proxyMu.Unlock()

	if s.store != nil {
		if err := s.store.RemoveTunnel(client.ID, name); err != nil {
			client.proxyMu.Lock()
			client.proxies[name] = tunnel
			client.proxyMu.Unlock()
			return err
		}
	}

	s.emitTunnelChanged(client.ID, protocol.ProxyConfig{
		Name:         tunnel.Config.Name,
		Type:         tunnel.Config.Type,
		ClientID:     client.ID,
		DesiredState: protocol.ProxyDesiredStateStopped,
		RuntimeState: protocol.ProxyRuntimeStateIdle,
	}, "deleted")
	return nil
}

func (s *Server) updateManagedTunnel(client *ClientConn, name string, localIP string, localPort, remotePort int, domain string) (protocol.ProxyConfig, error) {
	tunnel, err := s.mustGetTunnel(client, name)
	if err != nil {
		return protocol.ProxyConfig{}, err
	}

	wasError := tunnel.Config.DesiredState == protocol.ProxyDesiredStateRunning && tunnel.Config.RuntimeState == protocol.ProxyRuntimeStateError
	tunnelType := tunnel.Config.Type
	req := protocol.ProxyNewRequest{
		Name:       name,
		Type:       tunnelType,
		LocalIP:    localIP,
		LocalPort:  localPort,
		RemotePort: remotePort,
		Domain:     domain,
	}
	if err := s.validateProxyRequestWithExclusions(client, req, name, client.ID); err != nil {
		return protocol.ProxyConfig{}, err
	}

	// 更新运行时内存中的隧道配置
	client.proxyMu.Lock()
	tunnel.Config.LocalIP = localIP
	tunnel.Config.LocalPort = localPort
	tunnel.Config.RemotePort = remotePort
	tunnel.Config.Domain = domain
	if wasError {
		setProxyConfigStates(&tunnel.Config, protocol.ProxyDesiredStatePaused, protocol.ProxyRuntimeStateIdle, "")
	}
	updated := tunnel.Config
	client.proxyMu.Unlock()

	// 持久化配置变更到存储
	if s.store != nil {
		if err := s.store.UpdateTunnel(client.ID, name, localIP, localPort, remotePort, domain); err != nil {
			return protocol.ProxyConfig{}, err
		}
	}

	// 异常隧道编辑后自动重新启动：删掉旧的占位记录，重新创建隧道
	if wasError {
		client.proxyMu.Lock()
		delete(client.proxies, name)
		client.proxyMu.Unlock()

		config, err := s.createManagedTunnel(client, req, false, "updated")
		if err != nil {
			// 启动失败 → 放回 error 状态的占位记录
			errorConfig := s.upsertTunnelPlaceholder(client, req, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateError, err.Error())
			_ = s.persistTunnelStates(client.ID, name, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateError, err.Error())
			s.emitTunnelChanged(client.ID, errorConfig, "updated")
			return errorConfig, err
		}
		// 更新持久化状态为 active
		_ = s.persistTunnelStates(client.ID, name, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateExposed, "")
		return config, nil
	}

	s.emitTunnelChanged(client.ID, updated, "updated")
	return updated, nil
}

func (s *Server) restoreManagedTunnel(client *ClientConn, stored StoredTunnel) error {
	tunnel, err := s.prepareProxyTunnelWithExclusions(
		client,
		stored.ProxyNewRequest,
		protocol.ProxyDesiredStateRunning,
		protocol.ProxyRuntimeStatePending,
		stored.Name,
		client.ID,
	)
	if err != nil {
		return err
	}
	if err := s.persistTunnelStates(client.ID, stored.Name, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStatePending, ""); err != nil {
		s.removeTunnelRuntime(client, stored.Name)
		return err
	}
	s.emitTunnelChanged(client.ID, tunnel.Config, "pending")

	if _, err := s.waitForTunnelProvisionAck(client, tunnel.Config.ToProxyNewRequest()); err != nil {
		if errors.Is(err, errTunnelProvisionAckCancelled) {
			return err
		}
		s.markPendingTunnelErrorIfCurrent(client, stored.Name, tunnelProvisionErrorMessage(err))
		return err
	}

	if !s.isCurrentGeneration(client.ID, client.generation) {
		return errTunnelProvisionAckCancelled
	}

	if err := s.activatePreparedTunnel(client, tunnel); err != nil {
		s.failRestoredTunnelAfterReady(client, stored, err.Error())
		return err
	}
	if err := s.persistTunnelStates(client.ID, stored.Name, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateExposed, ""); err != nil {
		s.failRestoredTunnelAfterReady(client, stored, err.Error())
		return err
	}

	updated, err := s.mustGetTunnel(client, stored.Name)
	if err != nil {
		s.failRestoredTunnelAfterReady(client, stored, err.Error())
		return err
	}
	s.emitTunnelChanged(client.ID, updated.Config, "restored")
	return nil
}

func (s *Server) mustGetTunnel(client *ClientConn, name string) (*ProxyTunnel, error) {
	client.proxyMu.RLock()
	defer client.proxyMu.RUnlock()

	tunnel, ok := client.proxies[name]
	if !ok {
		return nil, fmt.Errorf("隧道 %q 不存在", name)
	}
	return tunnel, nil
}

func (s *Server) setTunnelStates(client *ClientConn, name, desiredState, runtimeState, errMsg string) (protocol.ProxyConfig, bool) {
	client.proxyMu.Lock()
	defer client.proxyMu.Unlock()
	tunnel, ok := client.proxies[name]
	if !ok {
		return protocol.ProxyConfig{}, false
	}
	setProxyConfigStates(&tunnel.Config, desiredState, runtimeState, errMsg)
	return tunnel.Config, true
}

func (s *Server) persistTunnelStates(clientID, name, desiredState, runtimeState, errMsg string) error {
	if s.store == nil {
		return nil
	}
	return s.store.UpdateStates(clientID, name, desiredState, runtimeState, errMsg)
}

func tunnelChangedActionForStates(desiredState, runtimeState string) string {
	switch {
	case desiredState == protocol.ProxyDesiredStatePaused && runtimeState == protocol.ProxyRuntimeStateIdle:
		return "paused"
	case desiredState == protocol.ProxyDesiredStateStopped && runtimeState == protocol.ProxyRuntimeStateIdle:
		return "stopped"
	case desiredState == protocol.ProxyDesiredStateRunning && runtimeState == protocol.ProxyRuntimeStateError:
		return "error"
	case desiredState == protocol.ProxyDesiredStateRunning && runtimeState == protocol.ProxyRuntimeStatePending:
		return "pending"
	case desiredState == protocol.ProxyDesiredStateRunning && runtimeState == protocol.ProxyRuntimeStateExposed:
		return "active"
	default:
		return "updated"
	}
}

func (s *Server) rollbackResumedTunnelAfterReady(client *ClientConn, name, previousDesired, previousRuntime, previousError string) {
	tunnel, err := s.mustGetTunnel(client, name)
	if err == nil && isTunnelExposed(tunnel.Config) {
		_ = s.PauseProxy(client, name)
	}
	config, ok := s.setTunnelStates(client, name, previousDesired, previousRuntime, previousError)
	if !ok {
		return
	}
	_ = s.persistTunnelStates(client.ID, name, previousDesired, previousRuntime, previousError)
	_ = s.notifyClientProxyClose(client, name, "provision_failed")
	s.emitTunnelChanged(client.ID, config, tunnelChangedActionForStates(previousDesired, previousRuntime))
}

func (s *Server) upsertTunnelPlaceholder(client *ClientConn, req protocol.ProxyNewRequest, desiredState, runtimeState, errMsg string) protocol.ProxyConfig {
	config := protocol.ProxyConfig{
		Name:       req.Name,
		Type:       req.Type,
		LocalIP:    req.LocalIP,
		LocalPort:  req.LocalPort,
		RemotePort: req.RemotePort,
		Domain:     req.Domain,
		ClientID:   client.ID,
	}
	setProxyConfigStates(&config, desiredState, runtimeState, errMsg)
	client.proxyMu.Lock()
	if client.proxies == nil {
		client.proxies = make(map[string]*ProxyTunnel)
	}
	client.proxies[req.Name] = &ProxyTunnel{
		Config: config,
		done:   make(chan struct{}),
	}
	client.proxyMu.Unlock()
	return config
}

func (s *Server) failRestoredTunnelAfterReady(client *ClientConn, stored StoredTunnel, message string) {
	s.removeTunnelRuntime(client, stored.Name)
	_ = s.notifyClientProxyClose(client, stored.Name, "provision_failed")
	config := s.upsertTunnelPlaceholder(client, stored.ProxyNewRequest, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateError, message)
	_ = s.persistTunnelStates(client.ID, stored.Name, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateError, message)
	s.emitTunnelChanged(client.ID, config, "error")
}

func storedTunnelToProxyConfig(stored StoredTunnel) protocol.ProxyConfig {
	config := protocol.ProxyConfig{
		Name:       stored.Name,
		Type:       stored.Type,
		LocalIP:    stored.LocalIP,
		LocalPort:  stored.LocalPort,
		RemotePort: stored.RemotePort,
		Domain:     stored.Domain,
		ClientID:   stored.ClientID,
	}
	setProxyConfigStates(&config, stored.DesiredState, stored.RuntimeState, stored.Error)
	return config
}

func storedTunnelFromRuntime(client *ClientConn, tunnel *ProxyTunnel) StoredTunnel {
	stored := StoredTunnel{
		ProxyNewRequest: tunnel.Config.ToProxyNewRequest(),
		ClientID:        client.ID,
		Hostname:        client.Info.Hostname,
		Binding:         TunnelBindingClientID,
	}
	stored.DesiredState = tunnel.Config.DesiredState
	stored.RuntimeState = tunnel.Config.RuntimeState
	stored.Error = tunnel.Config.Error
	_ = stored.normalize()
	return stored
}

func (s *Server) loadOfflineManagedTunnel(clientID, name string) (StoredTunnel, error) {
	if s.adminStore == nil {
		return StoredTunnel{}, errManagedTunnelClientNotFound
	}
	if _, ok := s.adminStore.GetRegisteredClient(clientID); !ok {
		return StoredTunnel{}, errManagedTunnelClientNotFound
	}
	if s.store == nil {
		return StoredTunnel{}, errManagedTunnelNotFound
	}

	stored, exists := s.store.GetTunnel(clientID, name)
	if !exists {
		return StoredTunnel{}, errManagedTunnelNotFound
	}

	return stored, nil
}

func offlineManagedStateAfterUpdate(stored StoredTunnel) (string, string, string) {
	switch stored.DesiredState {
	case protocol.ProxyDesiredStatePaused:
		return protocol.ProxyDesiredStatePaused, protocol.ProxyRuntimeStateIdle, ""
	case protocol.ProxyDesiredStateStopped:
		return protocol.ProxyDesiredStateStopped, protocol.ProxyRuntimeStateIdle, ""
	default:
		return protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateOffline, ""
	}
}

func (s *Server) updateOfflineManagedTunnel(clientID, name, localIP string, localPort, remotePort int, domain string) (protocol.ProxyConfig, error) {
	stored, err := s.loadOfflineManagedTunnel(clientID, name)
	if err != nil {
		return protocol.ProxyConfig{}, err
	}

	req := protocol.ProxyNewRequest{
		Name:       name,
		Type:       stored.Type,
		LocalIP:    localIP,
		LocalPort:  localPort,
		RemotePort: remotePort,
		Domain:     domain,
	}
	if req.Type == protocol.ProxyTypeHTTP {
		req.RemotePort = 0
	}
	if err := s.validateProxyRequestWithExclusions(nil, req, name, clientID); err != nil {
		return protocol.ProxyConfig{}, err
	}
	if err := s.store.UpdateTunnel(clientID, name, req.LocalIP, req.LocalPort, req.RemotePort, req.Domain); err != nil {
		return protocol.ProxyConfig{}, err
	}
	desiredState, runtimeState, errMsg := offlineManagedStateAfterUpdate(stored)
	if err := s.store.UpdateStates(clientID, name, desiredState, runtimeState, errMsg); err != nil {
		return protocol.ProxyConfig{}, err
	}

	updated, exists := s.store.GetTunnel(clientID, name)
	if !exists {
		return protocol.ProxyConfig{}, fmt.Errorf("隧道 %q 不存在", name)
	}

	config := storedTunnelToProxyConfig(updated)
	s.emitTunnelChanged(clientID, config, "updated")
	return config, nil
}

func (s *Server) pauseOfflineManagedTunnel(clientID, name string) (protocol.ProxyConfig, error) {
	stored, err := s.loadOfflineManagedTunnel(clientID, name)
	if err != nil {
		return protocol.ProxyConfig{}, err
	}
	if !canPauseTunnel(storedTunnelToProxyConfig(stored)) {
		return protocol.ProxyConfig{}, fmt.Errorf("只有 running/offline 或 running/exposed 状态的隧道才能暂停")
	}
	if err := s.store.UpdateStates(clientID, name, protocol.ProxyDesiredStatePaused, protocol.ProxyRuntimeStateIdle, ""); err != nil {
		return protocol.ProxyConfig{}, err
	}

	updated, exists := s.store.GetTunnel(clientID, name)
	if !exists {
		return protocol.ProxyConfig{}, fmt.Errorf("隧道 %q 不存在", name)
	}

	config := storedTunnelToProxyConfig(updated)
	s.emitTunnelChanged(clientID, config, "paused")
	return config, nil
}

func (s *Server) resumeOfflineManagedTunnel(clientID, name string) (protocol.ProxyConfig, error) {
	stored, err := s.loadOfflineManagedTunnel(clientID, name)
	if err != nil {
		return protocol.ProxyConfig{}, err
	}
	if !canResumeTunnel(storedTunnelToProxyConfig(stored)) {
		return protocol.ProxyConfig{}, fmt.Errorf("只有 paused/idle、stopped/idle 或 running/error 状态的隧道才能恢复")
	}
	if err := s.store.UpdateStates(clientID, name, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateOffline, ""); err != nil {
		return protocol.ProxyConfig{}, err
	}

	updated, exists := s.store.GetTunnel(clientID, name)
	if !exists {
		return protocol.ProxyConfig{}, fmt.Errorf("隧道 %q 不存在", name)
	}

	config := storedTunnelToProxyConfig(updated)
	s.emitTunnelChanged(clientID, config, "resumed")
	return config, nil
}

func (s *Server) stopOfflineManagedTunnel(clientID, name string) (protocol.ProxyConfig, error) {
	_, err := s.loadOfflineManagedTunnel(clientID, name)
	if err != nil {
		return protocol.ProxyConfig{}, err
	}
	if err := s.store.UpdateStates(clientID, name, protocol.ProxyDesiredStateStopped, protocol.ProxyRuntimeStateIdle, ""); err != nil {
		return protocol.ProxyConfig{}, err
	}

	updated, exists := s.store.GetTunnel(clientID, name)
	if !exists {
		return protocol.ProxyConfig{}, fmt.Errorf("隧道 %q 不存在", name)
	}

	config := storedTunnelToProxyConfig(updated)
	s.emitTunnelChanged(clientID, config, "stopped")
	return config, nil
}

func (s *Server) deleteOfflineManagedTunnel(clientID, name string) error {
	stored, err := s.loadOfflineManagedTunnel(clientID, name)
	if err != nil {
		return err
	}
	if err := s.store.RemoveTunnel(clientID, name); err != nil {
		return err
	}

	s.emitTunnelChanged(clientID, protocol.ProxyConfig{
		Name:         stored.Name,
		Type:         stored.Type,
		Domain:       stored.Domain,
		ClientID:     clientID,
		DesiredState: protocol.ProxyDesiredStateStopped,
		RuntimeState: protocol.ProxyRuntimeStateIdle,
	}, "deleted")
	return nil
}

func (s *Server) notifyClientProxyProvision(client *ClientConn, req protocol.ProxyNewRequest) error {
	message, err := protocol.NewMessage(protocol.MsgTypeProxyProvision, protocol.ProxyProvisionRequest(req))
	if err != nil {
		return err
	}
	return s.writeControlMessage(client, message)
}

func (s *Server) notifyClientProxyClose(client *ClientConn, name, reason string) error {
	message, err := protocol.NewMessage(protocol.MsgTypeProxyClose, protocol.ProxyCloseRequest{
		Name:   name,
		Reason: reason,
	})
	if err != nil {
		return err
	}
	return s.writeControlMessage(client, message)
}

func (s *Server) writeControlMessage(client *ClientConn, message *protocol.Message) error {
	if client.generation != 0 && !s.isCurrentLive(client.ID, client.generation) {
		return fmt.Errorf("client %s 当前不处于 live 会话", client.ID)
	}

	client.mu.Lock()
	defer client.mu.Unlock()

	if client.conn == nil {
		return fmt.Errorf("client %s 控制通道不可用", client.ID)
	}
	if err := client.conn.WriteJSON(message); err != nil {
		return fmt.Errorf("写入控制消息失败: %w", err)
	}
	return nil
}

func (s *Server) emitTunnelChanged(clientID string, tunnel protocol.ProxyConfig, action string) {
	setProxyConfigStates(&tunnel, tunnel.DesiredState, tunnel.RuntimeState, tunnel.Error)
	payload := map[string]any{
		"client_id": clientID,
		"action":    action,
		"tunnel":    tunnel,
	}
	s.events.PublishJSON("tunnel_changed", payload)
}

func (s *Server) readClientFromPath(w http.ResponseWriter, r *http.Request) (*ClientConn, bool) {
	clientID := r.PathValue("id")
	client, ok := s.loadLiveClient(clientID)
	if !ok {
		http.Error(w, `{"error":"client not found"}`, http.StatusNotFound)
		return nil, false
	}
	return client, true
}

func (s *Server) forceDisconnectClient(client *ClientConn) {
	_ = s.invalidateLogicalSessionIfCurrent(client.ID, client.generation, "force_disconnect")
}

func encodeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Printf("⚠️ JSON 响应编码失败: %v", err)
	}
}

// affectedTunnel 描述一条受端口白名单变更影响的隧道
type affectedTunnel struct {
	ClientID     string `json:"client_id"`
	Hostname     string `json:"hostname"`
	DisplayName  string `json:"display_name,omitempty"`
	TunnelName   string `json:"tunnel_name"`
	RemotePort   int    `json:"remote_port"`
	DesiredState string `json:"desired_state"`
	RuntimeState string `json:"runtime_state"`
	Error        string `json:"error,omitempty"`
}

// isPortInRanges 检查端口是否在给定的白名单范围内
func isPortInRanges(port int, ranges []PortRange) bool {
	for _, pr := range ranges {
		if port >= pr.Start && port <= pr.End {
			return true
		}
	}
	return false
}

// findTunnelsAffectedByPortChange 找出所有会被新端口白名单规则影响的隧道
// 扫描运行时内存中的隧道 + 持久化存储中的隧道（离线客户端的隧道）
func (s *Server) findTunnelsAffectedByPortChange(newPorts []PortRange) []affectedTunnel {
	// 新白名单为空 → 不限制端口，不会有受影响的隧道
	if len(newPorts) == 0 {
		return []affectedTunnel{}
	}

	affected := []affectedTunnel{}
	seen := map[string]bool{} // key: "clientID:tunnelName"

	// 1) 扫描运行时内存中的隧道（在线客户端）
	s.clients.Range(func(_, value any) bool {
		client := value.(*ClientConn)
		client.RangeProxies(func(name string, tunnel *ProxyTunnel) bool {
			if tunnel.Config.RemotePort != 0 && !isPortInRanges(tunnel.Config.RemotePort, newPorts) {
				// 已经是 error 状态的不重复通报（除非端口也变了）
				if tunnel.Config.RuntimeState == protocol.ProxyRuntimeStateError {
					return true
				}
				key := client.ID + ":" + name
				seen[key] = true
				// 尝试获取 display_name
				displayName := ""
				if s.adminStore != nil {
					if reg, ok := s.adminStore.GetRegisteredClient(client.ID); ok {
						displayName = reg.DisplayName
					}
				}
				affected = append(affected, affectedTunnel{
					ClientID:     client.ID,
					Hostname:     client.Info.Hostname,
					DisplayName:  displayName,
					TunnelName:   name,
					RemotePort:   tunnel.Config.RemotePort,
					DesiredState: tunnel.Config.DesiredState,
					RuntimeState: tunnel.Config.RuntimeState,
					Error:        tunnel.Config.Error,
				})
			}
			return true
		})
		return true
	})

	// 2) 扫描持久化存储中的隧道（包含离线客户端的隧道）
	if s.store != nil {
		allStored := s.store.GetAllTunnels()
		for _, st := range allStored {
			if st.RemotePort == 0 {
				continue
			}
			if st.RuntimeState == protocol.ProxyRuntimeStateError {
				continue
			}
			key := st.ClientID + ":" + st.Name
			if seen[key] {
				continue // 已在运行时中统计过
			}
			if !isPortInRanges(st.RemotePort, newPorts) {
				hostname := st.Hostname
				displayName := ""
				// 尝试从 adminStore 获取更详细的主机名和展示名
				if s.adminStore != nil && st.ClientID != "" {
					if reg, ok := s.adminStore.GetRegisteredClient(st.ClientID); ok {
						hostname = reg.Info.Hostname
						displayName = reg.DisplayName
					}
				}
				affected = append(affected, affectedTunnel{
					ClientID:     st.ClientID,
					Hostname:     hostname,
					DisplayName:  displayName,
					TunnelName:   st.Name,
					RemotePort:   st.RemotePort,
					DesiredState: st.DesiredState,
					RuntimeState: st.RuntimeState,
					Error:        st.Error,
				})
			}
		}
	}

	return affected
}

// markTunnelsPortNotAllowed 将受端口白名单变更影响的隧道标记为 error 状态
func (s *Server) markTunnelsPortNotAllowed(affected []affectedTunnel) {
	for _, a := range affected {
		errMsg := fmt.Sprintf("端口 %d 不在允许范围内", a.RemotePort)

		// 更新运行时状态（在线客户端）
		if value, ok := s.clients.Load(a.ClientID); ok {
			client := value.(*ClientConn)
			client.proxyMu.Lock()
			if tunnel, exists := client.proxies[a.TunnelName]; exists {
				if isTunnelExposed(tunnel.Config) {
					closeTunnelRuntimeResources(tunnel)
					go func(c *ClientConn, name string) {
						_ = s.notifyClientProxyClose(c, name, "port_not_allowed")
					}(client, a.TunnelName)
				}
				setProxyConfigStates(&tunnel.Config, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateError, errMsg)
			}
			client.proxyMu.Unlock()

			s.emitTunnelChanged(a.ClientID, protocol.ProxyConfig{
				Name:         a.TunnelName,
				RemotePort:   a.RemotePort,
				ClientID:     a.ClientID,
				DesiredState: protocol.ProxyDesiredStateRunning,
				RuntimeState: protocol.ProxyRuntimeStateError,
				Error:        errMsg,
			}, "port_not_allowed")
		}

		_ = s.persistTunnelStates(a.ClientID, a.TunnelName, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateError, errMsg)

		log.Printf("⚠️ 隧道 %s (端口 %d, 客户端 %s) 因端口白名单变更被标记为异常",
			a.TunnelName, a.RemotePort, a.ClientID)
	}
}
