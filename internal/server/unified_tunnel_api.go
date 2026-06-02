package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"

	"netsgo/pkg/protocol"
)

const (
	tunnelTopologyServerExpose   = protocol.TunnelTopologyServerExpose
	tunnelTopologyClientToClient = protocol.TunnelTopologyClientToClient

	tunnelEndpointLocationServer = protocol.EndpointLocationServer
	tunnelEndpointLocationClient = protocol.EndpointLocationClient

	tunnelIngressTypeTCPListen = protocol.IngressTypeTCPListen
	tunnelIngressTypeUDPListen = protocol.IngressTypeUDPListen
	tunnelIngressTypeHTTPHost  = protocol.IngressTypeHTTPHost

	tunnelTargetTypeTCPService = protocol.TargetTypeTCPService
	tunnelTargetTypeUDPService = protocol.TargetTypeUDPService

	tunnelTransportPolicyServerRelayOnly = protocol.TransportPolicyServerRelayOnly
	tunnelTransportPolicyDirectPreferred = protocol.TransportPolicyDirectPreferred
	tunnelTransportPolicyDirectOnly      = protocol.TransportPolicyDirectOnly

	tunnelActualTransportUnknown     = protocol.ActualTransportUnknown
	tunnelActualTransportServerRelay = protocol.ActualTransportServerRelay

	tunnelP2PStateIdle = protocol.P2PStateIdle

	tunnelRuntimeStateActive = protocol.TunnelRuntimeStateActive

	unifiedEndpointConfigMaxBytes = 16 * 1024
	unifiedEndpointConfigMaxDepth = 16
)

var errTunnelRevisionConflict = errors.New("tunnel revision conflict")

type endpointSpecAPI struct {
	Location string          `json:"location"`
	ClientID string          `json:"client_id,omitempty"`
	Type     string          `json:"type"`
	Config   json.RawMessage `json:"config"`
}

type p2pStateAPI struct {
	State     string `json:"state"`
	Error     string `json:"error,omitempty"`
	SessionID string `json:"session_id,omitempty"`
}

type participantRuntimeAPI struct {
	ClientID string `json:"client_id"`
	Role     string `json:"role"`
	State    string `json:"state"`
	Revision int64  `json:"revision"`
	Error    string `json:"error,omitempty"`
}

type tunnelParticipantsAPI struct {
	Ingress participantRuntimeAPI `json:"ingress"`
	Target  participantRuntimeAPI `json:"target"`
}

type transportRuntimeAPI struct {
	Policy          string    `json:"policy"`
	Actual          string    `json:"actual"`
	P2PState        string    `json:"p2p_state,omitempty"`
	P2PError        string    `json:"p2p_error,omitempty"`
	FallbackSince   time.Time `json:"fallback_since,omitempty"`
	LastDirectOK    time.Time `json:"last_direct_ok,omitempty"`
	LastDirectError string    `json:"last_direct_error,omitempty"`
}

type tunnelSpecAPI struct {
	ID                string                       `json:"id"`
	Name              string                       `json:"name"`
	Revision          int64                        `json:"revision"`
	Topology          string                       `json:"topology"`
	OwnerClientID     string                       `json:"owner_client_id"`
	Ingress           endpointSpecAPI              `json:"ingress"`
	Target            endpointSpecAPI              `json:"target"`
	TransportPolicy   string                       `json:"transport_policy"`
	ActualTransport   string                       `json:"actual_transport"`
	P2P               p2pStateAPI                  `json:"p2p"`
	DesiredState      string                       `json:"desired_state"`
	RuntimeState      string                       `json:"runtime_state"`
	Error             string                       `json:"error,omitempty"`
	Issues            []protocol.TunnelIssue       `json:"issues,omitempty"`
	Participants      tunnelParticipantsAPI        `json:"participants,omitempty"`
	Transport         transportRuntimeAPI          `json:"transport,omitempty"`
	BandwidthSettings protocol.BandwidthSettings   `json:"bandwidth_settings"`
	CreatedAt         time.Time                    `json:"created_at"`
	UpdatedAt         time.Time                    `json:"updated_at"`
	Capabilities      *protocol.TunnelCapabilities `json:"capabilities,omitempty"`
}

type tunnelCreateRequestAPI struct {
	ID                string                     `json:"id,omitempty"`
	Name              string                     `json:"name"`
	Revision          int64                      `json:"revision,omitempty"`
	Topology          string                     `json:"topology"`
	OwnerClientID     string                     `json:"owner_client_id,omitempty"`
	Ingress           endpointSpecAPI            `json:"ingress"`
	Target            endpointSpecAPI            `json:"target"`
	TransportPolicy   string                     `json:"transport_policy"`
	BandwidthSettings protocol.BandwidthSettings `json:"bandwidth_settings"`
}

type tunnelUpdateRequestAPI struct {
	ExpectedRevision int64                  `json:"expected_revision"`
	Spec             tunnelCreateRequestAPI `json:"spec"`
}

type tcpListenConfigAPI struct {
	BindIP string `json:"bind_ip"`
	Port   int    `json:"port"`
}

type ingressEndpointConfigAPI struct {
	BindIP string
	Port   int
	Domain string
}

type httpHostConfigAPI struct {
	Domain string `json:"domain"`
}

type serviceConfigAPI struct {
	IP   string `json:"ip,omitempty"`
	Host string `json:"host,omitempty"`
	Port int    `json:"port"`
}

func (s *Server) handleUnifiedTunnelCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleListUnifiedTunnels(w, r)
	case http.MethodPost:
		s.handleCreateUnifiedTunnel(w, r)
	default:
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
	}
}

func (s *Server) handleUnifiedTunnelItem(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleGetUnifiedTunnel(w, r)
	case http.MethodPut:
		s.handleUpdateUnifiedTunnel(w, r)
	case http.MethodDelete:
		s.handleDeleteUnifiedTunnel(w, r)
	default:
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
	}
}

func (s *Server) handleUnifiedTunnelAction(w http.ResponseWriter, r *http.Request) {
	current, ok, err := s.findUnifiedTunnelSpecByID(r.PathValue("tunnel_id"))
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "tunnel_lookup_failed", err.Error())
		return
	}
	if !ok {
		writeAPIError(w, http.StatusNotFound, "tunnel_not_found", "tunnel not found")
		return
	}

	switch r.PathValue("action") {
	case "resume":
		s.resumeUnifiedTunnel(w, current)
	case "stop":
		s.stopUnifiedTunnel(w, current)
	default:
		writeAPIError(w, http.StatusNotFound, "unknown_tunnel_action", "unknown tunnel action")
	}
}

func (s *Server) resumeUnifiedTunnel(w http.ResponseWriter, current tunnelSpecAPI) {
	stored, err := s.loadOfflineManagedTunnelBySelector(current.OwnerClientID, current.ID)
	if err != nil {
		status, payload := tunnelMutationErrorStatusAndBody(err)
		encodeJSON(w, status, payload)
		return
	}
	if err := s.store.UpdateStates(current.OwnerClientID, stored.Name, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateOffline, ""); err != nil {
		status, payload := tunnelMutationErrorStatusAndBody(err)
		encodeJSON(w, status, payload)
		return
	}
	stored.DesiredState = protocol.ProxyDesiredStateRunning
	stored.RuntimeState = protocol.ProxyRuntimeStateOffline
	s.scheduleUnifiedTunnelReconcile(stored, "resume")
	stored, err = s.store.GetTunnelByIDE(current.OwnerClientID, current.ID)
	if err != nil {
		status, payload := tunnelMutationErrorStatusAndBody(err)
		encodeJSON(w, status, payload)
		return
	}
	encodeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "tunnel resumed", "tunnel": specFromStoredTunnel(stored, s)})
}

func (s *Server) stopUnifiedTunnel(w http.ResponseWriter, current tunnelSpecAPI) {
	stored, err := s.loadOfflineManagedTunnelBySelector(current.OwnerClientID, current.ID)
	if err != nil {
		status, payload := tunnelMutationErrorStatusAndBody(err)
		encodeJSON(w, status, payload)
		return
	}
	config, err := s.stopOfflineManagedTunnel(current.OwnerClientID, current.ID)
	if err != nil {
		status, payload := tunnelMutationErrorStatusAndBody(err)
		encodeJSON(w, status, payload)
		return
	}
	if err := s.unprovisionStoredUnifiedTunnel(stored, "stopped", false); err != nil {
		logUnifiedRuntimeCleanupFailure("stop", stored, err)
	}
	encodeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "tunnel stopped", "tunnel": specFromStoredTunnelConfig(config, s)})
}

func (s *Server) handleListUnifiedTunnels(w http.ResponseWriter, _ *http.Request) {
	tunnels, err := s.allUnifiedTunnelSpecs()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "tunnel_list_failed", err.Error())
		return
	}
	encodeJSON(w, http.StatusOK, tunnels)
}

func (s *Server) handleGetUnifiedTunnel(w http.ResponseWriter, r *http.Request) {
	spec, ok, err := s.findUnifiedTunnelSpecByID(r.PathValue("tunnel_id"))
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "tunnel_lookup_failed", err.Error())
		return
	}
	if !ok {
		writeAPIError(w, http.StatusNotFound, "tunnel_not_found", "tunnel not found")
		return
	}
	encodeJSON(w, http.StatusOK, spec)
}

func (s *Server) handleClientTunnels(w http.ResponseWriter, r *http.Request) {
	clientID := r.PathValue("id")
	role := strings.TrimSpace(r.URL.Query().Get("role"))
	if role == "" {
		role = "owner"
	}
	if role != "owner" && role != "ingress" && role != "target" && role != "related" {
		writeAPIError(w, http.StatusBadRequest, "invalid_tunnel_role", "role must be owner, ingress, target, or related")
		return
	}

	tunnels, err := s.allUnifiedTunnelProxyConfigs()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "tunnel_list_failed", err.Error())
		return
	}

	filtered := make([]protocol.ProxyConfig, 0, len(tunnels))
	for _, tunnel := range tunnels {
		if unifiedTunnelProxyConfigMatchesClientRole(tunnel, clientID, role) {
			filtered = append(filtered, tunnel)
		}
	}
	encodeJSON(w, http.StatusOK, filtered)
}

func unifiedTunnelProxyConfigMatchesClientRole(tunnel protocol.ProxyConfig, clientID, role string) bool {
	ownerClientID := tunnel.OwnerClientID
	if ownerClientID == "" {
		ownerClientID = tunnel.ClientID
	}
	ingressClientID := ""
	if tunnel.Ingress != nil && tunnel.Ingress.Location == tunnelEndpointLocationClient {
		ingressClientID = tunnel.Ingress.ClientID
	}
	targetClientID := tunnel.ClientID
	if tunnel.Target != nil && tunnel.Target.Location == tunnelEndpointLocationClient {
		targetClientID = tunnel.Target.ClientID
		if targetClientID == "" {
			targetClientID = tunnel.ClientID
		}
	}

	switch role {
	case "owner":
		return ownerClientID == clientID
	case "ingress":
		return ingressClientID == clientID
	case "target":
		return targetClientID == clientID
	case "related":
		return ownerClientID == clientID || ingressClientID == clientID || targetClientID == clientID
	default:
		return false
	}
}

func (s *Server) handleCreateUnifiedTunnel(w http.ResponseWriter, r *http.Request) {
	var req tunnelCreateRequestAPI
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_body", "invalid request body")
		return
	}

	config, err := s.createUnifiedStoredTunnel(req)
	if err != nil {
		status, payload := tunnelMutationErrorStatusAndBody(err)
		encodeJSON(w, status, payload)
		return
	}
	encodeJSON(w, http.StatusCreated, specFromStoredTunnel(config, s))
}

func (s *Server) handleUpdateUnifiedTunnel(w http.ResponseWriter, r *http.Request) {
	tunnelID := r.PathValue("tunnel_id")
	current, ok, err := s.findUnifiedTunnelSpecByID(tunnelID)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "tunnel_lookup_failed", err.Error())
		return
	}
	if !ok {
		writeAPIError(w, http.StatusNotFound, "tunnel_not_found", "tunnel not found")
		return
	}

	var req tunnelUpdateRequestAPI
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_body", "invalid request body")
		return
	}
	if req.ExpectedRevision <= 0 {
		encodeJSON(w, http.StatusBadRequest, revisionConflictPayload("expected_revision is required", current.Revision))
		return
	}
	if req.ExpectedRevision != current.Revision {
		encodeJSON(w, http.StatusConflict, revisionConflictPayload(errTunnelRevisionConflict.Error(), current.Revision))
		return
	}

	updated, err := s.updateUnifiedStoredTunnel(current, req.ExpectedRevision, req.Spec)
	if err != nil {
		if errors.Is(err, ErrTunnelRevisionConflict) {
			encodeJSON(w, http.StatusConflict, revisionConflictPayload(errTunnelRevisionConflict.Error(), 0))
			return
		}
		status, payload := tunnelMutationErrorStatusAndBody(err)
		encodeJSON(w, status, payload)
		return
	}
	encodeJSON(w, http.StatusOK, map[string]any{"success": true, "tunnel": specFromStoredTunnel(updated, s)})
}

func revisionConflictPayload(message string, currentRevision int64) map[string]any {
	if message == "" {
		message = errTunnelRevisionConflict.Error()
	}
	payload := map[string]any{
		"success":    false,
		"error":      message,
		"message":    message,
		"error_code": protocol.TunnelMutationErrorCodeRevisionConflict,
		"code":       protocol.TunnelMutationErrorCodeRevisionConflict,
		"field":      "expected_revision",
	}
	if currentRevision > 0 {
		payload["current_revision"] = currentRevision
	}
	return payload
}

func (s *Server) handleDeleteUnifiedTunnel(w http.ResponseWriter, r *http.Request) {
	current, ok, err := s.findUnifiedTunnelSpecByID(r.PathValue("tunnel_id"))
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "tunnel_lookup_failed", err.Error())
		return
	}
	if !ok {
		writeAPIError(w, http.StatusNotFound, "tunnel_not_found", "tunnel not found")
		return
	}

	stored, err := s.loadOfflineManagedTunnelBySelector(current.OwnerClientID, current.ID)
	if err != nil {
		status, payload := tunnelMutationErrorStatusAndBody(err)
		encodeJSON(w, status, payload)
		return
	}
	if err := s.deleteStoredUnifiedTunnel(stored); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "tunnel_delete_failed", err.Error())
		return
	}
	s.unifiedRuntime.clearTunnelIssues(stored.ID)
	if err := s.unprovisionStoredUnifiedTunnel(stored, "deleted", true); err != nil {
		logUnifiedRuntimeCleanupFailure("delete", stored, err)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteStoredUnifiedTunnel(stored StoredTunnel) error {
	if s.store == nil {
		return fmt.Errorf("tunnel store not initialized")
	}
	clientID := stored.OwnerClientID
	if clientID == "" {
		clientID = stored.ClientID
	}
	if err := s.store.RemoveTunnelByID(clientID, stored.ID); err != nil {
		return err
	}
	deletedConfig := storedTunnelToProxyConfig(stored)
	setProxyConfigStates(&deletedConfig, protocol.ProxyDesiredStateStopped, protocol.ProxyRuntimeStateIdle, "")
	s.emitTunnelChanged(clientID, deletedConfig, "deleted")
	return nil
}

func (s *Server) unprovisionStoredUnifiedTunnel(stored StoredTunnel, reason string, removeServerRuntime bool) error {
	if stored.Topology == TunnelTopologyClientToClient {
		return s.unprovisionClientRelayTunnel(stored, reason)
	}
	return s.unprovisionServerExposeTunnel(stored, reason, removeServerRuntime)
}

func logUnifiedRuntimeCleanupFailure(action string, stored StoredTunnel, err error) {
	if err == nil {
		return
	}
	log.Printf("⚠️ unified tunnel runtime cleanup failed: action=%s id=%s name=%s topology=%s revision=%d err=%v", action, stored.ID, stored.Name, stored.Topology, stored.Revision, err)
}

func deriveUnifiedTunnelOwner(topology string, ingress, target endpointSpecAPI) (string, error) {
	switch topology {
	case tunnelTopologyServerExpose:
		if target.ClientID == "" {
			return "", newProxyRequestValidationError(fmt.Errorf("target.client_id is required for server_expose"), "target.client_id", "missing_client_id", http.StatusBadRequest)
		}
		return target.ClientID, nil
	case tunnelTopologyClientToClient:
		if target.ClientID == "" {
			return "", newProxyRequestValidationError(fmt.Errorf("target.client_id is required for client_to_client"), "target.client_id", "missing_client_id", http.StatusBadRequest)
		}
		return target.ClientID, nil
	default:
		return "", newProxyRequestValidationError(fmt.Errorf("unsupported topology %q", topology), "topology", protocol.TunnelMutationErrorCodeUnsupportedTopology, http.StatusBadRequest)
	}
}

func validateUnifiedEndpointCombination(topology string, ingress, target endpointSpecAPI) error {
	if target.Location != tunnelEndpointLocationClient {
		return newProxyRequestValidationError(fmt.Errorf("target.location must be client"), "target.location", "unsupported_target_location", http.StatusBadRequest)
	}
	if strings.TrimSpace(target.ClientID) == "" {
		return newProxyRequestValidationError(fmt.Errorf("target.client_id is required"), "target.client_id", "missing_client_id", http.StatusBadRequest)
	}
	if target.Type != tunnelTargetTypeTCPService && target.Type != tunnelTargetTypeUDPService {
		return newProxyRequestValidationError(fmt.Errorf("unsupported target type %q", target.Type), "target.type", protocol.TunnelMutationErrorCodeUnsupportedEndpointType, http.StatusBadRequest)
	}

	switch topology {
	case tunnelTopologyServerExpose:
		if ingress.Location != tunnelEndpointLocationServer {
			return newProxyRequestValidationError(fmt.Errorf("server_expose ingress.location must be server"), "ingress.location", "invalid_ingress_location", http.StatusBadRequest)
		}
		if strings.TrimSpace(ingress.ClientID) != "" {
			return newProxyRequestValidationError(fmt.Errorf("server ingress cannot include client_id"), "ingress.client_id", "invalid_client_id", http.StatusBadRequest)
		}
		if ingress.Type != tunnelIngressTypeTCPListen && ingress.Type != tunnelIngressTypeUDPListen && ingress.Type != tunnelIngressTypeHTTPHost {
			return newProxyRequestValidationError(fmt.Errorf("unsupported ingress type %q", ingress.Type), "ingress.type", protocol.TunnelMutationErrorCodeUnsupportedEndpointType, http.StatusBadRequest)
		}
	case tunnelTopologyClientToClient:
		if ingress.Location != tunnelEndpointLocationClient {
			return newProxyRequestValidationError(fmt.Errorf("client_to_client ingress.location must be client"), "ingress.location", "invalid_ingress_location", http.StatusBadRequest)
		}
		if strings.TrimSpace(ingress.ClientID) == "" {
			return newProxyRequestValidationError(fmt.Errorf("ingress.client_id is required"), "ingress.client_id", "missing_client_id", http.StatusBadRequest)
		}
		if ingress.ClientID == target.ClientID {
			return newProxyRequestValidationError(fmt.Errorf("ingress and target clients must differ"), "ingress.client_id", protocol.TunnelMutationErrorCodeSameIngressAndTargetClient, http.StatusBadRequest)
		}
		if ingress.Type == tunnelIngressTypeHTTPHost {
			return newProxyRequestValidationError(fmt.Errorf("client_to_client does not support http_host ingress"), "ingress.type", protocol.TunnelMutationErrorCodeUnsupportedEndpointType, http.StatusBadRequest)
		}
		if ingress.Type != tunnelIngressTypeTCPListen && ingress.Type != tunnelIngressTypeUDPListen {
			return newProxyRequestValidationError(fmt.Errorf("unsupported ingress type %q", ingress.Type), "ingress.type", protocol.TunnelMutationErrorCodeUnsupportedEndpointType, http.StatusBadRequest)
		}
	default:
		return newProxyRequestValidationError(fmt.Errorf("unsupported topology %q", topology), "topology", protocol.TunnelMutationErrorCodeUnsupportedTopology, http.StatusBadRequest)
	}

	switch ingress.Type {
	case tunnelIngressTypeHTTPHost:
		if target.Type != tunnelTargetTypeTCPService {
			return newProxyRequestValidationError(fmt.Errorf("http_host ingress requires tcp_service target"), "target.type", "invalid_target_type", http.StatusBadRequest)
		}
	case tunnelIngressTypeTCPListen:
		if target.Type != tunnelTargetTypeTCPService {
			return newProxyRequestValidationError(fmt.Errorf("tcp_listen ingress requires tcp_service target"), "target.type", "invalid_target_type", http.StatusBadRequest)
		}
	case tunnelIngressTypeUDPListen:
		if target.Type != tunnelTargetTypeUDPService {
			return newProxyRequestValidationError(fmt.Errorf("udp_listen ingress requires udp_service target"), "target.type", "invalid_target_type", http.StatusBadRequest)
		}
	}
	return nil
}

func decodeListenEndpointConfig(endpoint endpointSpecAPI, topology string) (ingressEndpointConfigAPI, error) {
	switch endpoint.Type {
	case tunnelIngressTypeHTTPHost:
		var cfg httpHostConfigAPI
		if err := decodeStrictEndpointConfig(endpoint.Config, &cfg); err != nil {
			return ingressEndpointConfigAPI{}, newProxyRequestValidationError(fmt.Errorf("invalid http_host config: %w", err), "ingress.config", "invalid_endpoint_config", http.StatusBadRequest)
		}
		cfg.Domain = strings.TrimSpace(cfg.Domain)
		if err := validateDomain(cfg.Domain); err != nil {
			return ingressEndpointConfigAPI{}, newProxyRequestValidationError(err, protocol.TunnelMutationFieldDomain, protocol.TunnelMutationErrorCodeDomainInvalid, http.StatusBadRequest)
		}
		return ingressEndpointConfigAPI{Domain: cfg.Domain}, nil
	case tunnelIngressTypeTCPListen, tunnelIngressTypeUDPListen:
		var cfg tcpListenConfigAPI
		if err := decodeStrictEndpointConfig(endpoint.Config, &cfg); err != nil {
			return ingressEndpointConfigAPI{}, newProxyRequestValidationError(fmt.Errorf("invalid listen config: %w", err), "ingress.config", "invalid_endpoint_config", http.StatusBadRequest)
		}
		if cfg.BindIP == "" {
			if topology == tunnelTopologyClientToClient {
				return ingressEndpointConfigAPI{}, newProxyRequestValidationError(fmt.Errorf("bind_ip is required for client_to_client ingress"), "ingress.config.bind_ip", protocol.TunnelMutationErrorCodeInvalidBindIP, http.StatusBadRequest)
			}
			cfg.BindIP = "0.0.0.0"
		}
		ip := net.ParseIP(cfg.BindIP)
		if ip == nil || ip.To4() == nil {
			return ingressEndpointConfigAPI{}, newProxyRequestValidationError(fmt.Errorf("bind_ip must be a valid IPv4 address"), "ingress.config.bind_ip", protocol.TunnelMutationErrorCodeInvalidBindIP, http.StatusBadRequest)
		}
		if cfg.Port < 1 || cfg.Port > 65535 {
			return ingressEndpointConfigAPI{}, newProxyRequestValidationError(fmt.Errorf("port must be in range 1-65535"), "ingress.config.port", "invalid_endpoint_config", http.StatusBadRequest)
		}
		return ingressEndpointConfigAPI{BindIP: cfg.BindIP, Port: cfg.Port}, nil
	default:
		return ingressEndpointConfigAPI{}, newProxyRequestValidationError(fmt.Errorf("unsupported ingress type %q", endpoint.Type), "ingress.type", protocol.TunnelMutationErrorCodeUnsupportedEndpointType, http.StatusBadRequest)
	}
}

func decodeServiceEndpointConfig(endpoint endpointSpecAPI) (serviceConfigAPI, error) {
	var cfg serviceConfigAPI
	if err := decodeStrictEndpointConfig(endpoint.Config, &cfg); err != nil {
		return serviceConfigAPI{}, newProxyRequestValidationError(fmt.Errorf("invalid service config: %w", err), "target.config", "invalid_endpoint_config", http.StatusBadRequest)
	}
	cfg.Host = strings.TrimSpace(cfg.Host)
	cfg.IP = strings.TrimSpace(cfg.IP)
	if cfg.Host != "" && cfg.IP != "" && cfg.Host != cfg.IP {
		return serviceConfigAPI{}, newProxyRequestValidationError(fmt.Errorf("target host and ip must match when both are provided"), "target.config.host", "invalid_endpoint_config", http.StatusBadRequest)
	}
	if cfg.Host == "" {
		cfg.Host = cfg.IP
	}
	if cfg.Host == "" {
		cfg.Host = "127.0.0.1"
	}
	if cfg.Host == "" {
		return serviceConfigAPI{}, newProxyRequestValidationError(fmt.Errorf("target host is required"), "target.config.host", "invalid_endpoint_config", http.StatusBadRequest)
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		return serviceConfigAPI{}, newProxyRequestValidationError(fmt.Errorf("target port must be in range 1-65535"), "target.config.port", "invalid_endpoint_config", http.StatusBadRequest)
	}
	cfg.IP = cfg.Host
	return cfg, nil
}

func decodeStrictEndpointConfig(raw json.RawMessage, target any) error {
	if len(raw) == 0 {
		raw = []byte(`{}`)
	}
	if err := validateEndpointConfigComplexity(raw); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("config must contain a single JSON object")
	}
	return nil
}

func validateEndpointConfigComplexity(raw json.RawMessage) error {
	if len(raw) > unifiedEndpointConfigMaxBytes {
		return fmt.Errorf("config exceeds %d bytes", unifiedEndpointConfigMaxBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	depth := 0
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		delim, ok := token.(json.Delim)
		if !ok {
			continue
		}
		switch delim {
		case '{', '[':
			depth++
			if depth > unifiedEndpointConfigMaxDepth {
				return fmt.Errorf("config nesting depth exceeds %d", unifiedEndpointConfigMaxDepth)
			}
		case '}', ']':
			depth--
		}
	}
}

func (s *Server) createUnifiedStoredTunnel(req tunnelCreateRequestAPI) (StoredTunnel, error) {
	stored, err := s.storedTunnelFromUnifiedRequest(req, "")
	if err != nil {
		return StoredTunnel{}, err
	}
	if s.store == nil {
		return StoredTunnel{}, fmt.Errorf("tunnel store not initialized")
	}
	if err := s.store.AddTunnel(stored); err != nil {
		return StoredTunnel{}, err
	}
	s.scheduleUnifiedTunnelReconcile(stored, "created")
	if reloaded, err := s.store.GetTunnelByIDE(stored.OwnerClientID, stored.ID); err == nil {
		stored = reloaded
	}
	s.emitTunnelChanged(stored.OwnerClientID, storedTunnelToProxyConfig(stored), "created")
	return stored, nil
}

func (s *Server) updateUnifiedStoredTunnel(current tunnelSpecAPI, expectedRevision int64, req tunnelCreateRequestAPI) (StoredTunnel, error) {
	stored, err := s.storedTunnelFromUnifiedRequest(req, current.ID)
	if err != nil {
		return StoredTunnel{}, err
	}
	if stored.OwnerClientID != current.OwnerClientID {
		return StoredTunnel{}, newProxyRequestValidationError(fmt.Errorf("tunnel owner cannot be changed"), "target.client_id", "owner_change_not_supported", http.StatusBadRequest)
	}
	if s.store == nil {
		return StoredTunnel{}, fmt.Errorf("tunnel store not initialized")
	}

	existing, err := s.store.GetTunnelByIDE(current.OwnerClientID, current.ID)
	if err != nil {
		return StoredTunnel{}, err
	}
	if existing.Revision != expectedRevision {
		return StoredTunnel{}, ErrTunnelRevisionConflict
	}
	stored.Revision = expectedRevision + 1
	stored.CreatedAt = existing.CreatedAt
	stored.UpdatedAt = time.Now().UTC()
	stored.DesiredState = existing.DesiredState
	stored.RuntimeState = protocol.ProxyRuntimeStateOffline
	if stored.DesiredState == protocol.ProxyDesiredStateStopped {
		stored.RuntimeState = protocol.ProxyRuntimeStateIdle
	}
	stored.Error = ""

	if err := s.store.ReplaceTunnelByID(current.OwnerClientID, current.ID, expectedRevision, stored); err != nil {
		return StoredTunnel{}, err
	}
	if err := s.unprovisionStoredUnifiedTunnel(existing, "updated", true); err != nil {
		logUnifiedRuntimeCleanupFailure("update", existing, err)
	}
	s.scheduleUnifiedTunnelReconcile(stored, "updated")
	if reloaded, err := s.store.GetTunnelByIDE(stored.OwnerClientID, stored.ID); err == nil {
		stored = reloaded
	}
	s.emitTunnelChanged(stored.OwnerClientID, storedTunnelToProxyConfig(stored), "updated")
	return stored, nil
}

func (s *Server) storedTunnelFromUnifiedRequest(req tunnelCreateRequestAPI, existingID string) (StoredTunnel, error) {
	if strings.TrimSpace(req.ID) != "" {
		return StoredTunnel{}, newProxyRequestValidationError(fmt.Errorf("id is server-owned and cannot be submitted"), "id", "server_owned_field", http.StatusBadRequest)
	}
	if req.Revision != 0 {
		return StoredTunnel{}, newProxyRequestValidationError(fmt.Errorf("revision is server-owned and cannot be submitted"), "revision", "server_owned_field", http.StatusBadRequest)
	}
	if strings.TrimSpace(req.OwnerClientID) != "" {
		return StoredTunnel{}, newProxyRequestValidationError(fmt.Errorf("owner_client_id is server-derived and cannot be submitted"), "owner_client_id", "server_owned_field", http.StatusBadRequest)
	}
	if strings.TrimSpace(req.Name) == "" {
		return StoredTunnel{}, newProxyRequestValidationError(fmt.Errorf("tunnel name is required"), protocol.TunnelMutationFieldName, "", http.StatusBadRequest)
	}
	if req.TransportPolicy == "" {
		req.TransportPolicy = tunnelTransportPolicyServerRelayOnly
	}
	if req.TransportPolicy != tunnelTransportPolicyServerRelayOnly {
		return StoredTunnel{}, newProxyRequestValidationError(fmt.Errorf("transport policy %q requires direct transport support, which is not available in this build", req.TransportPolicy), "transport_policy", protocol.TunnelMutationErrorCodeDirectTransportUnavailable, http.StatusBadRequest)
	}
	if err := validateUnifiedEndpointCombination(req.Topology, req.Ingress, req.Target); err != nil {
		return StoredTunnel{}, err
	}
	ownerClientID, err := deriveUnifiedTunnelOwner(req.Topology, req.Ingress, req.Target)
	if err != nil {
		return StoredTunnel{}, err
	}
	ingressConfig, err := decodeListenEndpointConfig(req.Ingress, req.Topology)
	if err != nil {
		return StoredTunnel{}, err
	}
	targetConfig, err := decodeServiceEndpointConfig(req.Target)
	if err != nil {
		return StoredTunnel{}, err
	}
	if err := s.validateUnifiedClientsAndCapabilities(req); err != nil {
		return StoredTunnel{}, err
	}
	if err := s.validateUnifiedIngressResourceAvailable(req, existingID); err != nil {
		return StoredTunnel{}, err
	}
	if err := s.preflightClientIngress(req, existingID); err != nil {
		return StoredTunnel{}, err
	}

	proxyType := protocol.ProxyTypeTCP
	switch req.Ingress.Type {
	case tunnelIngressTypeUDPListen:
		proxyType = protocol.ProxyTypeUDP
	case tunnelIngressTypeHTTPHost:
		proxyType = protocol.ProxyTypeHTTP
	}
	if req.Topology == tunnelTopologyClientToClient && proxyType == protocol.ProxyTypeHTTP {
		return StoredTunnel{}, newProxyRequestValidationError(fmt.Errorf("client_to_client does not support http_host ingress"), "ingress.type", protocol.TunnelMutationErrorCodeUnsupportedEndpointType, http.StatusBadRequest)
	}
	id := existingID
	if id == "" {
		id = generateUUID()
	}
	now := time.Now().UTC()
	ingress := EndpointSpec{
		Location: req.Ingress.Location,
		ClientID: req.Ingress.ClientID,
		Type:     req.Ingress.Type,
		Config:   normalizedIngressConfigRaw(req.Ingress.Type, ingressConfig),
	}
	target := EndpointSpec{
		Location: req.Target.Location,
		ClientID: req.Target.ClientID,
		Type:     req.Target.Type,
		Config:   mustRawJSON(serviceConfigAPI{Host: targetConfig.Host, IP: targetConfig.Host, Port: targetConfig.Port}),
	}
	stored := StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{
			ID:                id,
			Name:              strings.TrimSpace(req.Name),
			Type:              proxyType,
			LocalIP:           targetConfig.Host,
			LocalPort:         targetConfig.Port,
			RemotePort:        ingressConfig.Port,
			Domain:            ingressConfig.Domain,
			BandwidthSettings: req.BandwidthSettings,
		},
		ClientID:        ownerClientID,
		Binding:         TunnelBindingClientID,
		Revision:        1,
		Topology:        req.Topology,
		OwnerClientID:   ownerClientID,
		Ingress:         ingress,
		Target:          target,
		TransportPolicy: req.TransportPolicy,
		ActualTransport: TunnelActualTransportUnknown,
		P2P:             P2PState{State: TunnelP2PStateIdle},
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if proxyType == protocol.ProxyTypeHTTP {
		stored.RemotePort = 0
	}
	setStoredTunnelStates(&stored, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateOffline, "")
	if liveTarget, ok := s.loadLiveClient(req.Target.ClientID); ok && req.Topology == tunnelTopologyClientToClient {
		if _, ingressLive := s.loadLiveClient(req.Ingress.ClientID); ingressLive && clientHasDataSession(liveTarget) {
			stored.RuntimeState = protocol.ProxyRuntimeStatePending
		}
	}
	if err := stored.normalize(); err != nil {
		return StoredTunnel{}, err
	}
	return stored, nil
}

func normalizedIngressConfigRaw(endpointType string, cfg ingressEndpointConfigAPI) json.RawMessage {
	if endpointType == tunnelIngressTypeHTTPHost {
		return mustRawJSON(httpHostConfigAPI{Domain: cfg.Domain})
	}
	return mustRawJSON(tcpListenConfigAPI{BindIP: cfg.BindIP, Port: cfg.Port})
}

func ingressResourceCandidateFromUnifiedRequest(req tunnelCreateRequestAPI, id string) (StoredTunnel, error) {
	cfg, err := decodeListenEndpointConfig(req.Ingress, req.Topology)
	if err != nil {
		return StoredTunnel{}, err
	}
	return StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{ID: id},
		Topology:        req.Topology,
		Ingress: EndpointSpec{
			Location: req.Ingress.Location,
			ClientID: req.Ingress.ClientID,
			Type:     req.Ingress.Type,
			Config:   normalizedIngressConfigRaw(req.Ingress.Type, cfg),
		},
	}, nil
}

func (s *Server) validateUnifiedClientsAndCapabilities(req tunnelCreateRequestAPI) error {
	target, ok := s.registeredClientInfo(req.Target.ClientID)
	if !ok {
		return newProxyRequestValidationError(fmt.Errorf("unknown target client %q", req.Target.ClientID), "target.client_id", protocol.TunnelMutationErrorCodeUnknownClient, http.StatusBadRequest)
	}
	if !clientSupportsTargetType(target.Info.Capabilities, req.Target.Type) {
		return newProxyRequestValidationError(fmt.Errorf("target client does not support %s", req.Target.Type), "target.type", protocol.TunnelMutationErrorCodeCapabilityNotSupported, http.StatusBadRequest)
	}
	if req.Topology == tunnelTopologyClientToClient {
		ingress, ok := s.registeredClientInfo(req.Ingress.ClientID)
		if !ok {
			return newProxyRequestValidationError(fmt.Errorf("unknown ingress client %q", req.Ingress.ClientID), "ingress.client_id", protocol.TunnelMutationErrorCodeUnknownClient, http.StatusBadRequest)
		}
		if !clientSupportsIngressType(ingress.Info.Capabilities, req.Ingress.Type) {
			return newProxyRequestValidationError(fmt.Errorf("ingress client does not support %s", req.Ingress.Type), "ingress.type", protocol.TunnelMutationErrorCodeCapabilityNotSupported, http.StatusBadRequest)
		}
	}
	return nil
}

func (s *Server) validateUnifiedIngressResourceAvailable(req tunnelCreateRequestAPI, excludeID string) error {
	if s.store == nil {
		return nil
	}
	switch req.Ingress.Type {
	case tunnelIngressTypeTCPListen, tunnelIngressTypeUDPListen, tunnelIngressTypeHTTPHost:
	default:
		return nil
	}

	var current StoredTunnel
	hasCurrent := false
	if excludeID != "" {
		found, ok, err := s.findStoredTunnelByID(excludeID)
		if err != nil {
			return fmt.Errorf("failed to load current tunnel for ingress resource validation: %w", err)
		}
		current = found
		hasCurrent = ok
	}

	candidate, err := ingressResourceCandidateFromUnifiedRequest(req, excludeID)
	if err != nil {
		return err
	}
	conflict, ok, err := s.store.findIngressResourceConflict(candidate, excludeID)
	if err != nil {
		return fmt.Errorf("failed to check ingress resource conflicts: %w", err)
	}
	if ok {
		return newProxyRequestValidationError(fmt.Errorf("ingress resource conflicts with tunnel %q", conflict.Name), ingressResourceConflictField(req.Ingress.Type), protocol.TunnelMutationErrorCodeIngressResourceConflict, http.StatusConflict)
	}

	if req.Topology != tunnelTopologyServerExpose || req.Ingress.Location != tunnelEndpointLocationServer {
		return nil
	}
	if req.Ingress.Type == tunnelIngressTypeHTTPHost {
		cfg, err := decodeListenEndpointConfig(req.Ingress, req.Topology)
		if err != nil {
			return err
		}
		excludeName := ""
		excludeClientID := ""
		if hasCurrent {
			excludeName = current.Name
			excludeClientID = current.OwnerClientID
		}
		return checkDomainConflict(cfg.Domain, excludeName, excludeClientID, s)
	}
	if hasCurrent && sameUnifiedIngressResource(current.Ingress, req.Ingress, current.Topology, req.Topology) {
		return nil
	}

	return s.preflightServerIngressResource(req)
}

func ingressResourceConflictField(ingressType string) string {
	switch ingressType {
	case tunnelIngressTypeHTTPHost:
		return protocol.TunnelMutationFieldDomain
	case tunnelIngressTypeTCPListen, tunnelIngressTypeUDPListen:
		return "ingress.config.port"
	default:
		return "ingress.config"
	}
}

func sameUnifiedIngressResource(current EndpointSpec, next endpointSpecAPI, currentTopology, nextTopology string) bool {
	if current.Location != next.Location || current.ClientID != next.ClientID || current.Type != next.Type {
		return false
	}
	switch current.Type {
	case tunnelIngressTypeHTTPHost:
		currentCfg, err := decodeListenEndpointConfig(endpointSpecAPI(current), currentTopology)
		if err != nil {
			return false
		}
		nextCfg, err := decodeListenEndpointConfig(next, nextTopology)
		if err != nil {
			return false
		}
		return canonicalHost(currentCfg.Domain) == canonicalHost(nextCfg.Domain)
	case tunnelIngressTypeTCPListen, tunnelIngressTypeUDPListen:
		currentCfg, err := decodeListenEndpointConfig(endpointSpecAPI(current), currentTopology)
		if err != nil {
			return false
		}
		nextCfg, err := decodeListenEndpointConfig(next, nextTopology)
		if err != nil {
			return false
		}
		return currentCfg.BindIP == nextCfg.BindIP && currentCfg.Port == nextCfg.Port
	default:
		return false
	}
}

func (s *Server) preflightServerIngressResource(req tunnelCreateRequestAPI) error {
	switch req.Ingress.Type {
	case tunnelIngressTypeHTTPHost:
		return nil
	case tunnelIngressTypeTCPListen, tunnelIngressTypeUDPListen:
	default:
		return nil
	}
	cfg, err := decodeListenEndpointConfig(req.Ingress, req.Topology)
	if err != nil {
		return err
	}
	if cfg.Port <= 0 {
		return nil
	}
	if cfg.Port == 80 || cfg.Port == 443 {
		return newProxyRequestValidationError(fmt.Errorf("TCP/UDP tunnels cannot use reserved port %d", cfg.Port), "ingress.config.port", "", http.StatusBadRequest)
	}
	if listenPort := serverListenPort(s); listenPort > 0 && cfg.Port == listenPort {
		return newProxyRequestValidationError(fmt.Errorf("port %d conflicts with the NetsGo management service listen port", cfg.Port), "ingress.config.port", protocol.TunnelMutationErrorCodeIngressResourceConflict, http.StatusConflict)
	}
	if s.auth.adminStore != nil {
		initialized, err := s.auth.adminStore.IsInitializedE()
		if err != nil {
			return newProxyRequestValidationError(fmt.Errorf("failed to read initialization state: %w", err), "ingress.config.port", "", http.StatusServiceUnavailable)
		}
		if initialized && !s.auth.adminStore.IsPortAllowed(cfg.Port) {
			return newProxyRequestValidationError(fmt.Errorf("port %d is not in the allowed range", cfg.Port), "ingress.config.port", "", http.StatusBadRequest)
		}
	}
	addr := fmt.Sprintf(":%d", cfg.Port)
	if req.Ingress.Type == tunnelIngressTypeUDPListen {
		conn, err := net.ListenPacket("udp", addr)
		if err != nil {
			return newProxyRequestValidationError(fmt.Errorf("server UDP ingress port %d is not available: %w", cfg.Port, err), "ingress.config.port", protocol.TunnelMutationErrorCodeIngressPortInUse, http.StatusConflict)
		}
		_ = conn.Close()
		return nil
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return newProxyRequestValidationError(fmt.Errorf("server TCP ingress port %d is not available: %w", cfg.Port, err), "ingress.config.port", protocol.TunnelMutationErrorCodeIngressPortInUse, http.StatusConflict)
	}
	_ = ln.Close()
	return nil
}

func (s *Server) registeredClientInfo(clientID string) (RegisteredClient, bool) {
	if live, ok := s.loadLiveClient(clientID); ok {
		return RegisteredClient{ID: clientID, Info: live.GetInfo()}, true
	}
	if s.auth.adminStore == nil {
		return RegisteredClient{}, false
	}
	return s.auth.adminStore.GetRegisteredClient(clientID)
}

func clientSupportsTargetType(capabilities *protocol.ClientCapabilities, targetType string) bool {
	if capabilities == nil {
		return false
	}
	for _, supported := range capabilities.TargetTypes {
		if supported == targetType {
			return true
		}
	}
	return false
}

func clientSupportsIngressType(capabilities *protocol.ClientCapabilities, ingressType string) bool {
	if capabilities == nil {
		return false
	}
	for _, supported := range capabilities.IngressTypes {
		if supported == ingressType {
			return true
		}
	}
	return false
}

func clientHasDataSession(client *ClientConn) bool {
	client.dataMu.RLock()
	defer client.dataMu.RUnlock()
	return client.dataSession != nil && !client.dataSession.IsClosed()
}

func unifiedSpecFromProxyConfig(config protocol.ProxyConfig) tunnelSpecAPI {
	revision := config.Revision
	if revision <= 0 {
		revision = 1
	}
	createdAt := config.CreatedAt
	updatedAt := config.CreatedAt
	if updatedAt.IsZero() {
		updatedAt = createdAt
	}

	topology := tunnelTopologyServerExpose
	ownerClientID := config.ClientID
	ingress := endpointSpecAPI{Location: tunnelEndpointLocationServer}
	target := endpointSpecAPI{Location: tunnelEndpointLocationClient, ClientID: config.ClientID}
	actualTransport := tunnelActualTransportServerRelay
	if config.RuntimeState != protocol.ProxyRuntimeStateExposed {
		actualTransport = tunnelActualTransportUnknown
	}

	switch config.Type {
	case protocol.ProxyTypeUDP:
		ingress.Type = tunnelIngressTypeUDPListen
		ingress.Config = mustRawJSON(tcpListenConfigAPI{BindIP: "0.0.0.0", Port: config.RemotePort})
		target.Type = tunnelTargetTypeUDPService
		target.Config = mustRawJSON(serviceConfigAPI{IP: config.LocalIP, Port: config.LocalPort})
	case protocol.ProxyTypeHTTP:
		ingress.Type = tunnelIngressTypeHTTPHost
		ingress.Config = mustRawJSON(httpHostConfigAPI{Domain: config.Domain})
		target.Type = tunnelTargetTypeTCPService
		target.Config = mustRawJSON(serviceConfigAPI{IP: config.LocalIP, Port: config.LocalPort})
	default:
		ingress.Type = tunnelIngressTypeTCPListen
		ingress.Config = mustRawJSON(tcpListenConfigAPI{BindIP: "0.0.0.0", Port: config.RemotePort})
		target.Type = tunnelTargetTypeTCPService
		target.Config = mustRawJSON(serviceConfigAPI{IP: config.LocalIP, Port: config.LocalPort})
	}

	runtimeState := config.RuntimeState
	if runtimeState == protocol.ProxyRuntimeStateExposed {
		runtimeState = tunnelRuntimeStateActive
	}

	return tunnelSpecAPI{
		ID:              config.ID,
		Name:            config.Name,
		Revision:        revision,
		Topology:        topology,
		OwnerClientID:   ownerClientID,
		Ingress:         ingress,
		Target:          target,
		TransportPolicy: tunnelTransportPolicyServerRelayOnly,
		ActualTransport: actualTransport,
		P2P:             p2pStateAPI{State: tunnelP2PStateIdle},
		DesiredState:    config.DesiredState,
		RuntimeState:    runtimeState,
		Error:           config.Error,
		Issues:          issuesFromTunnelError(config.Error, runtimeState, config.ClientID),
		Participants: tunnelParticipantsAPI{
			Ingress: participantRuntimeAPI{Role: "ingress", State: runtimeState, Revision: revision},
			Target:  participantRuntimeAPI{ClientID: config.ClientID, Role: "target", State: runtimeState, Revision: revision},
		},
		Transport: transportRuntimeAPI{
			Policy:   tunnelTransportPolicyServerRelayOnly,
			Actual:   actualTransport,
			P2PState: tunnelP2PStateIdle,
		},
		BandwidthSettings: config.BandwidthSettings,
		CreatedAt:         createdAt,
		UpdatedAt:         updatedAt,
		Capabilities:      config.Capabilities,
	}
}

func issuesFromTunnelError(message, runtimeState, clientID string) []protocol.TunnelIssue {
	if strings.TrimSpace(message) == "" || runtimeState != protocol.ProxyRuntimeStateError {
		return nil
	}
	issue := protocol.TunnelIssue{
		Code:       "runtime_error",
		Scope:      "transport",
		ClientID:   clientID,
		Severity:   "error",
		Message:    message,
		Retryable:  true,
		ObservedAt: time.Now().UTC(),
	}
	return []protocol.TunnelIssue{issue}
}

func specFromStoredTunnel(stored StoredTunnel, s *Server) tunnelSpecAPI {
	config := storedTunnelToProxyConfig(stored)
	computedRuntime := computedRuntimeStateForStoredTunnel(stored, s)
	setProxyConfigStates(&config, stored.DesiredState, runtimeStateForProxyConfig(computedRuntime), "")
	config.Capabilities = computeTunnelCapabilities(config)
	spec := unifiedSpecFromProxyConfig(config)
	spec.Topology = stored.Topology
	spec.OwnerClientID = stored.OwnerClientID
	spec.Ingress = endpointSpecAPI{
		Location: stored.Ingress.Location,
		ClientID: stored.Ingress.ClientID,
		Type:     stored.Ingress.Type,
		Config:   stored.Ingress.Config,
	}
	spec.Target = endpointSpecAPI{
		Location: stored.Target.Location,
		ClientID: stored.Target.ClientID,
		Type:     stored.Target.Type,
		Config:   stored.Target.Config,
	}
	spec.TransportPolicy = stored.TransportPolicy
	spec.ActualTransport = tunnelActualTransportUnknown
	if computedRuntime == tunnelRuntimeStateActive {
		spec.ActualTransport = protocol.ActualTransportServerRelay
	}
	spec.P2P = p2pStateAPI{State: stored.P2P.State, Error: stored.P2P.Error, SessionID: stored.P2P.SessionID}
	if spec.P2P.State == "" {
		spec.P2P.State = tunnelP2PStateIdle
	}
	spec.RuntimeState = computedRuntime
	spec.Error = ""
	spec.Issues = s.issuesForStoredTunnel(stored)
	spec.Participants = tunnelParticipantsAPI{
		Ingress: participantRuntimeAPI{ClientID: stored.Ingress.ClientID, Role: "ingress", State: participantStateForSpecRuntime(stored.Ingress.ClientID, computedRuntime), Revision: stored.Revision},
		Target:  participantRuntimeAPI{ClientID: stored.Target.ClientID, Role: "target", State: participantStateForSpecRuntime(stored.Target.ClientID, computedRuntime), Revision: stored.Revision},
	}
	spec.Transport = transportRuntimeAPI{
		Policy:   stored.TransportPolicy,
		Actual:   spec.ActualTransport,
		P2PState: spec.P2P.State,
	}
	spec.UpdatedAt = stored.UpdatedAt
	return spec
}

func computedRuntimeStateForStoredTunnel(stored StoredTunnel, s *Server) string {
	if stored.DesiredState == protocol.ProxyDesiredStateStopped {
		return protocol.ProxyRuntimeStateIdle
	}
	if !requiredTunnelClientsReady(stored, s) {
		return protocol.ProxyRuntimeStateOffline
	}
	if len(s.capabilityIssuesForStoredTunnel(stored)) > 0 {
		return protocol.ProxyRuntimeStateError
	}
	if s.unifiedRuntime.hasIssuesForStoredTunnel(stored, true) {
		return protocol.ProxyRuntimeStateError
	}
	if stored.Topology == TunnelTopologyClientToClient {
		if _, ok := s.c2c.get(stored.ID); ok && isActiveRuntimeState(stored.RuntimeState) {
			return tunnelRuntimeStateActive
		}
		return protocol.ProxyRuntimeStatePending
	}
	if stored.Topology == TunnelTopologyServerExpose || stored.Topology == "" {
		if client, ok := s.loadLiveClient(stored.OwnerClientID); ok {
			if name, tunnel, exists := findTunnelBySelector(client, stored.ID); exists {
				config, runtimeHeld, stillExists := serverExposeTunnelSnapshot(client, name, tunnel)
				if !stillExists {
					return protocol.ProxyRuntimeStatePending
				}
				if config.DesiredState == protocol.ProxyDesiredStateRunning && runtimeHeld {
					return tunnelRuntimeStateActive
				}
				if config.RuntimeState == protocol.ProxyRuntimeStatePending {
					return protocol.ProxyRuntimeStatePending
				}
			}
		}
	}
	return protocol.ProxyRuntimeStatePending
}

func (s *Server) issuesForStoredTunnel(stored StoredTunnel) []protocol.TunnelIssue {
	if stored.DesiredState == protocol.ProxyDesiredStateStopped || !requiredTunnelClientsReady(stored, s) {
		return nil
	}
	if issues := s.capabilityIssuesForStoredTunnel(stored); len(issues) > 0 {
		return issues
	}
	return s.unifiedRuntime.issuesForStoredTunnel(stored, true)
}

func (s *Server) capabilityIssuesForStoredTunnel(stored StoredTunnel) []protocol.TunnelIssue {
	if stored.DesiredState == protocol.ProxyDesiredStateStopped {
		return nil
	}
	issues := make([]protocol.TunnelIssue, 0, 2)
	if stored.Target.ClientID != "" {
		target, ok := s.registeredClientInfo(stored.Target.ClientID)
		if !ok || !clientSupportsTargetType(target.Info.Capabilities, stored.Target.Type) {
			issues = append(issues, capabilityIssue("target_client", stored.Target.ClientID, stored.Target.Type, "Service source client does not support this target service type"))
		}
	}
	if stored.Ingress.Location == tunnelEndpointLocationClient && stored.Ingress.ClientID != "" {
		ingress, ok := s.registeredClientInfo(stored.Ingress.ClientID)
		if !ok || !clientSupportsIngressType(ingress.Info.Capabilities, stored.Ingress.Type) {
			issues = append(issues, capabilityIssue("ingress_client", stored.Ingress.ClientID, stored.Ingress.Type, "Ingress client does not support this ingress type"))
		}
	}
	return issues
}

func capabilityIssue(scope, clientID, endpointType, message string) protocol.TunnelIssue {
	details, _ := json.Marshal(map[string]string{"endpoint_type": endpointType})
	return protocol.TunnelIssue{
		Code:       protocol.TunnelIssueCodeCapabilityNotSupported,
		Scope:      scope,
		ClientID:   clientID,
		Severity:   "error",
		Message:    message,
		Retryable:  true,
		ObservedAt: time.Now().UTC(),
		Details:    details,
	}
}

func runtimeStateForProxyConfig(runtimeState string) string {
	if runtimeState == tunnelRuntimeStateActive {
		return protocol.ProxyRuntimeStateExposed
	}
	return runtimeState
}

func specFromStoredTunnelConfig(config protocol.ProxyConfig, s *Server) tunnelSpecAPI {
	if s.store != nil && config.ID != "" && config.ClientID != "" {
		if stored, err := s.store.GetTunnelByIDE(config.ClientID, config.ID); err == nil {
			return specFromStoredTunnel(stored, s)
		}
	}
	return unifiedSpecFromProxyConfig(proxyConfigForClientView(config, s.isClientOnline(config.ClientID)))
}

func requiredTunnelClientsReady(stored StoredTunnel, s *Server) bool {
	if stored.Target.ClientID != "" {
		client, ok := s.loadLiveClient(stored.Target.ClientID)
		if !ok || !clientHasDataSession(client) {
			return false
		}
	}
	if stored.Ingress.Location == tunnelEndpointLocationClient && stored.Ingress.ClientID != "" {
		client, ok := s.loadLiveClient(stored.Ingress.ClientID)
		if !ok || !clientHasDataSession(client) {
			return false
		}
	}
	return true
}

func participantStateForSpecRuntime(_ string, runtimeState string) string {
	switch runtimeState {
	case tunnelRuntimeStateActive, protocol.ProxyRuntimeStateExposed:
		return protocol.ParticipantStateReady
	case protocol.ProxyRuntimeStatePending:
		return protocol.ParticipantStateProvisionPending
	case protocol.ProxyRuntimeStateOffline:
		return protocol.ParticipantStateOffline
	case protocol.ProxyRuntimeStateIdle:
		return protocol.ParticipantStateIdle
	case protocol.ProxyRuntimeStateError:
		return protocol.ParticipantStateError
	default:
		return runtimeState
	}
}

func mustRawJSON(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return data
}

func unifiedTunnelViewKey(id, clientID, name string) string {
	if strings.TrimSpace(id) != "" {
		return "id:" + id
	}
	return "client:" + clientID + ":name:" + name
}

func (s *Server) allUnifiedTunnelSpecs() ([]tunnelSpecAPI, error) {
	byID := map[string]tunnelSpecAPI{}
	appendConfig := func(config protocol.ProxyConfig, online bool) {
		view := proxyConfigForClientView(config, online)
		key := unifiedTunnelViewKey(view.ID, view.ClientID, view.Name)
		if view.ID == "" {
			view.ID = view.Name
		}
		if _, exists := byID[key]; exists {
			return
		}
		byID[key] = unifiedSpecFromProxyConfig(view)
	}

	if s.store != nil {
		stored, err := s.store.GetAllTunnels()
		if err != nil {
			return nil, err
		}
		for _, tunnel := range stored {
			spec := specFromStoredTunnel(tunnel, s)
			key := unifiedTunnelViewKey(spec.ID, spec.OwnerClientID, spec.Name)
			if spec.ID == "" {
				spec.ID = spec.Name
			}
			byID[key] = spec
		}
	}

	s.RangeClients(func(_ string, client *ClientConn) bool {
		online := client.isLive()
		for _, config := range client.ProxyConfigsSnapshot() {
			appendConfig(config, online)
		}
		return true
	})

	tunnels := make([]tunnelSpecAPI, 0, len(byID))
	for _, tunnel := range byID {
		tunnels = append(tunnels, tunnel)
	}
	sort.Slice(tunnels, func(i, j int) bool {
		if !tunnels[i].CreatedAt.Equal(tunnels[j].CreatedAt) {
			return tunnels[i].CreatedAt.After(tunnels[j].CreatedAt)
		}
		return tunnels[i].Name < tunnels[j].Name
	})
	return tunnels, nil
}

func (s *Server) allUnifiedTunnelProxyConfigs() ([]protocol.ProxyConfig, error) {
	byID := map[string]protocol.ProxyConfig{}
	appendConfig := func(config protocol.ProxyConfig, online bool) {
		view := proxyConfigForClientView(config, online)
		key := unifiedTunnelViewKey(view.ID, view.ClientID, view.Name)
		if view.ID == "" {
			view.ID = view.Name
		}
		if _, exists := byID[key]; exists {
			return
		}
		byID[key] = view
	}

	if s.store != nil {
		stored, err := s.store.GetAllTunnels()
		if err != nil {
			return nil, err
		}
		for _, tunnel := range stored {
			view := s.storedTunnelViewConfig(tunnel)
			key := unifiedTunnelViewKey(view.ID, view.ClientID, view.Name)
			if view.ID == "" {
				view.ID = view.Name
			}
			byID[key] = view
		}
	}

	s.RangeClients(func(_ string, client *ClientConn) bool {
		online := client.isLive()
		for _, config := range client.ProxyConfigsSnapshot() {
			appendConfig(config, online)
		}
		return true
	})

	tunnels := make([]protocol.ProxyConfig, 0, len(byID))
	for _, tunnel := range byID {
		tunnels = append(tunnels, tunnel)
	}
	sort.Slice(tunnels, func(i, j int) bool {
		if !tunnels[i].CreatedAt.Equal(tunnels[j].CreatedAt) {
			return tunnels[i].CreatedAt.After(tunnels[j].CreatedAt)
		}
		return tunnels[i].Name < tunnels[j].Name
	})
	return tunnels, nil
}

func (s *Server) findUnifiedTunnelSpecByID(id string) (tunnelSpecAPI, bool, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return tunnelSpecAPI{}, false, nil
	}
	if s.store != nil {
		stored, err := s.store.GetTunnelByID(id)
		if err == nil {
			return specFromStoredTunnel(stored, s), true, nil
		}
		if !errors.Is(err, ErrTunnelNotFound) {
			return tunnelSpecAPI{}, false, err
		}
	}

	var found tunnelSpecAPI
	var ok bool
	s.RangeClients(func(_ string, client *ClientConn) bool {
		online := client.isLive()
		for _, config := range client.ProxyConfigsSnapshot() {
			view := proxyConfigForClientView(config, online)
			if view.ID == "" {
				view.ID = view.Name
			}
			if view.ID != id {
				continue
			}
			found = unifiedSpecFromProxyConfig(view)
			ok = true
			return false
		}
		return true
	})
	if ok {
		return found, true, nil
	}
	return tunnelSpecAPI{}, false, nil
}

func (s *Server) isClientOnline(clientID string) bool {
	_, ok := s.loadLiveClient(clientID)
	return ok
}
