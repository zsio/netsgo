package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"

	"netsgo/pkg/protocol"
)

const (
	tunnelTopologyServerExpose   = "server_expose"
	tunnelTopologyClientToClient = "client_to_client"

	tunnelEndpointLocationServer = "server"
	tunnelEndpointLocationClient = "client"

	tunnelIngressTypeTCPListen = "tcp_listen"
	tunnelIngressTypeUDPListen = "udp_listen"
	tunnelIngressTypeHTTPHost  = "http_host"

	tunnelTargetTypeTCPService = "tcp_service"
	tunnelTargetTypeUDPService = "udp_service"

	tunnelTransportPolicyServerRelayOnly = "server_relay_only"
	tunnelTransportPolicyDirectPreferred = "direct_preferred"
	tunnelTransportPolicyDirectOnly      = "direct_only"

	tunnelActualTransportUnknown     = "unknown"
	tunnelActualTransportServerRelay = "server_relay"

	tunnelP2PStateIdle = "idle"

	tunnelRuntimeStateActive = "active"
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
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
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
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleUnifiedTunnelAction(w http.ResponseWriter, r *http.Request) {
	current, ok, err := s.findUnifiedTunnelSpecByID(r.PathValue("tunnel_id"))
	if err != nil {
		encodeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if !ok {
		encodeJSON(w, http.StatusNotFound, map[string]any{"error": "tunnel not found"})
		return
	}

	switch r.PathValue("action") {
	case "resume":
		s.resumeUnifiedTunnel(w, current)
	case "stop":
		s.stopUnifiedTunnel(w, current)
	default:
		encodeJSON(w, http.StatusNotFound, map[string]any{"error": "unknown tunnel action"})
	}
}

func (s *Server) resumeUnifiedTunnel(w http.ResponseWriter, current tunnelSpecAPI) {
	if current.Topology == tunnelTopologyClientToClient {
		config, err := s.resumeOfflineManagedTunnel(current.OwnerClientID, current.ID)
		if err != nil {
			status, payload := tunnelMutationErrorStatusAndBody(err)
			encodeJSON(w, status, payload)
			return
		}
		encodeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "tunnel resumed", "tunnel": specFromStoredTunnelConfig(config, s)})
		return
	}
	if client, ok := s.loadLiveClient(current.OwnerClientID); ok {
		tunnelName, tunnel, exists := findTunnelBySelector(client, current.ID)
		if !exists {
			encodeJSON(w, http.StatusNotFound, map[string]any{"error": "tunnel not found"})
			return
		}
		if !canResumeTunnel(tunnel.Config) {
			encodeJSON(w, http.StatusBadRequest, map[string]any{"error": "only stopped/idle or running/error tunnels can be resumed"})
			return
		}
		if err := s.resumeManagedTunnel(client, tunnelName); err != nil {
			status, payload := tunnelMutationErrorStatusAndBody(err)
			encodeJSON(w, status, payload)
			return
		}
		encodeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "tunnel resumed"})
		return
	}

	if _, err := s.resumeOfflineManagedTunnel(current.OwnerClientID, current.ID); err != nil {
		status, payload := tunnelMutationErrorStatusAndBody(err)
		encodeJSON(w, status, payload)
		return
	}
	encodeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "tunnel resumed"})
}

func (s *Server) stopUnifiedTunnel(w http.ResponseWriter, current tunnelSpecAPI) {
	if current.Topology == tunnelTopologyClientToClient {
		config, err := s.stopOfflineManagedTunnel(current.OwnerClientID, current.ID)
		if err != nil {
			status, payload := tunnelMutationErrorStatusAndBody(err)
			encodeJSON(w, status, payload)
			return
		}
		encodeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "tunnel stopped", "tunnel": specFromStoredTunnelConfig(config, s)})
		return
	}
	if client, ok := s.loadLiveClient(current.OwnerClientID); ok {
		tunnelName, _, exists := findTunnelBySelector(client, current.ID)
		if !exists {
			encodeJSON(w, http.StatusNotFound, map[string]any{"error": "tunnel not found"})
			return
		}
		if err := s.stopManagedTunnel(client, tunnelName); err != nil {
			encodeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		encodeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "tunnel stopped"})
		return
	}

	if _, err := s.stopOfflineManagedTunnel(current.OwnerClientID, current.ID); err != nil {
		status, payload := tunnelMutationErrorStatusAndBody(err)
		encodeJSON(w, status, payload)
		return
	}
	encodeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "tunnel stopped"})
}

func (s *Server) handleListUnifiedTunnels(w http.ResponseWriter, _ *http.Request) {
	tunnels, err := s.allUnifiedTunnelSpecs()
	if err != nil {
		encodeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	encodeJSON(w, http.StatusOK, tunnels)
}

func (s *Server) handleGetUnifiedTunnel(w http.ResponseWriter, r *http.Request) {
	spec, ok, err := s.findUnifiedTunnelSpecByID(r.PathValue("tunnel_id"))
	if err != nil {
		encodeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if !ok {
		encodeJSON(w, http.StatusNotFound, map[string]any{"error": "tunnel not found"})
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
		encodeJSON(w, http.StatusBadRequest, map[string]any{"error": "role must be owner, ingress, target, or related"})
		return
	}

	tunnels, err := s.allUnifiedTunnelSpecs()
	if err != nil {
		encodeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	filtered := make([]tunnelSpecAPI, 0, len(tunnels))
	for _, tunnel := range tunnels {
		if unifiedTunnelMatchesClientRole(tunnel, clientID, role) {
			filtered = append(filtered, tunnel)
		}
	}
	encodeJSON(w, http.StatusOK, filtered)
}

func unifiedTunnelMatchesClientRole(tunnel tunnelSpecAPI, clientID, role string) bool {
	ingressClientID := ""
	if tunnel.Ingress.Location == tunnelEndpointLocationClient {
		ingressClientID = tunnel.Ingress.ClientID
	}
	targetClientID := ""
	if tunnel.Target.Location == tunnelEndpointLocationClient {
		targetClientID = tunnel.Target.ClientID
	}

	switch role {
	case "owner":
		return tunnel.OwnerClientID == clientID
	case "ingress":
		return ingressClientID == clientID
	case "target":
		return targetClientID == clientID
	case "related":
		return tunnel.OwnerClientID == clientID || ingressClientID == clientID || targetClientID == clientID
	default:
		return false
	}
}

func (s *Server) handleCreateUnifiedTunnel(w http.ResponseWriter, r *http.Request) {
	var req tunnelCreateRequestAPI
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		encodeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body"})
		return
	}

	if req.Topology == tunnelTopologyClientToClient {
		config, err := s.createUnifiedStoredTunnel(req)
		if err != nil {
			status, payload := tunnelMutationErrorStatusAndBody(err)
			encodeJSON(w, status, payload)
			return
		}
		encodeJSON(w, http.StatusCreated, specFromStoredTunnel(config, s))
		return
	}

	proxyReq, ownerClientID, err := s.proxyRequestFromUnifiedCreate(req, "")
	if err != nil {
		status, payload := tunnelMutationErrorStatusAndBody(err)
		encodeJSON(w, status, payload)
		return
	}

	var config protocol.ProxyConfig
	if client, ok := s.loadLiveClient(ownerClientID); ok {
		config, err = s.createManagedTunnel(client, proxyReq, true, "created")
	} else {
		config, err = s.createOfflineManagedTunnel(ownerClientID, proxyReq)
	}
	if err != nil {
		status, payload := tunnelMutationErrorStatusAndBody(err)
		encodeJSON(w, status, payload)
		return
	}

	spec := unifiedSpecFromProxyConfig(proxyConfigForClientView(config, s.isClientOnline(ownerClientID)))
	encodeJSON(w, http.StatusCreated, spec)
}

func (s *Server) handleUpdateUnifiedTunnel(w http.ResponseWriter, r *http.Request) {
	tunnelID := r.PathValue("tunnel_id")
	current, ok, err := s.findUnifiedTunnelSpecByID(tunnelID)
	if err != nil {
		encodeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if !ok {
		encodeJSON(w, http.StatusNotFound, map[string]any{"error": "tunnel not found"})
		return
	}

	var req tunnelUpdateRequestAPI
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		encodeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body"})
		return
	}
	if req.ExpectedRevision <= 0 {
		encodeJSON(w, http.StatusBadRequest, map[string]any{"error": "expected_revision is required"})
		return
	}
	if req.ExpectedRevision != current.Revision {
		encodeJSON(w, http.StatusConflict, map[string]any{"error": errTunnelRevisionConflict.Error(), "error_code": "revision_conflict", "current_revision": current.Revision})
		return
	}

	if current.Topology == tunnelTopologyClientToClient || req.Spec.Topology == tunnelTopologyClientToClient {
		updated, err := s.updateUnifiedStoredTunnel(current, req.ExpectedRevision, req.Spec)
		if err != nil {
			if errors.Is(err, ErrTunnelRevisionConflict) {
				encodeJSON(w, http.StatusConflict, map[string]any{"error": errTunnelRevisionConflict.Error(), "error_code": "revision_conflict"})
				return
			}
			status, payload := tunnelMutationErrorStatusAndBody(err)
			encodeJSON(w, status, payload)
			return
		}
		encodeJSON(w, http.StatusOK, map[string]any{"success": true, "tunnel": specFromStoredTunnel(updated, s)})
		return
	}

	proxyReq, ownerClientID, err := s.proxyRequestFromUnifiedCreate(req.Spec, current.ID)
	if err != nil {
		status, payload := tunnelMutationErrorStatusAndBody(err)
		encodeJSON(w, status, payload)
		return
	}
	if ownerClientID != current.OwnerClientID {
		encodeJSON(w, http.StatusBadRequest, map[string]any{"error": "tunnel owner cannot be changed"})
		return
	}

	var updated protocol.ProxyConfig
	if client, ok := s.loadLiveClient(ownerClientID); ok {
		updated, err = s.updateManagedTunnelWithRevision(client, current.ID, req.ExpectedRevision, proxyReq.Name, proxyReq.LocalIP, proxyReq.LocalPort, proxyReq.RemotePort, proxyReq.Domain, proxyReq.IngressBPS, proxyReq.EgressBPS)
	} else {
		updated, err = s.updateOfflineManagedTunnelWithRevision(ownerClientID, current.ID, req.ExpectedRevision, proxyReq.Name, proxyReq.LocalIP, proxyReq.LocalPort, proxyReq.RemotePort, proxyReq.Domain, proxyReq.IngressBPS, proxyReq.EgressBPS)
	}
	if err != nil {
		if errors.Is(err, ErrTunnelRevisionConflict) {
			encodeJSON(w, http.StatusConflict, map[string]any{"error": errTunnelRevisionConflict.Error(), "error_code": "revision_conflict"})
			return
		}
		status, payload := tunnelMutationErrorStatusAndBody(err)
		encodeJSON(w, status, payload)
		return
	}

	spec := unifiedSpecFromProxyConfig(proxyConfigForClientView(updated, s.isClientOnline(ownerClientID)))
	encodeJSON(w, http.StatusOK, map[string]any{"success": true, "tunnel": spec})
}

func (s *Server) handleDeleteUnifiedTunnel(w http.ResponseWriter, r *http.Request) {
	current, ok, err := s.findUnifiedTunnelSpecByID(r.PathValue("tunnel_id"))
	if err != nil {
		encodeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if !ok {
		encodeJSON(w, http.StatusNotFound, map[string]any{"error": "tunnel not found"})
		return
	}

	if current.Topology == tunnelTopologyClientToClient {
		if err := s.deleteOfflineManagedTunnel(current.OwnerClientID, current.ID); err != nil {
			status, payload := tunnelMutationErrorStatusAndBody(err)
			encodeJSON(w, status, payload)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if client, ok := s.loadLiveClient(current.OwnerClientID); ok {
		tunnelName, tunnel, exists := findTunnelBySelector(client, current.ID)
		if !exists {
			encodeJSON(w, http.StatusNotFound, map[string]any{"error": "tunnel not found"})
			return
		}
		if !canEditOrDeleteLiveTunnel(tunnel.Config) {
			encodeJSON(w, http.StatusBadRequest, tunnelDeleteBlockedErrorBody(tunnel.Config))
			return
		}
		if err := s.deleteManagedTunnel(client, tunnelName); err != nil {
			encodeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if err := s.deleteOfflineManagedTunnel(current.OwnerClientID, current.ID); err != nil {
		status, payload := tunnelMutationErrorStatusAndBody(err)
		encodeJSON(w, status, payload)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) proxyRequestFromUnifiedCreate(req tunnelCreateRequestAPI, existingID string) (protocol.ProxyNewRequest, string, error) {
	if strings.TrimSpace(req.ID) != "" {
		return protocol.ProxyNewRequest{}, "", newProxyRequestValidationError(fmt.Errorf("id is server-owned and cannot be submitted"), "id", "server_owned_field", http.StatusBadRequest)
	}
	if req.Revision != 0 {
		return protocol.ProxyNewRequest{}, "", newProxyRequestValidationError(fmt.Errorf("revision is server-owned and cannot be submitted"), "revision", "server_owned_field", http.StatusBadRequest)
	}
	if strings.TrimSpace(req.OwnerClientID) != "" {
		return protocol.ProxyNewRequest{}, "", newProxyRequestValidationError(fmt.Errorf("owner_client_id is server-derived and cannot be submitted"), "owner_client_id", "server_owned_field", http.StatusBadRequest)
	}
	if strings.TrimSpace(req.Name) == "" {
		return protocol.ProxyNewRequest{}, "", newProxyRequestValidationError(fmt.Errorf("tunnel name is required"), protocol.TunnelMutationFieldName, "", http.StatusBadRequest)
	}

	if req.TransportPolicy == "" {
		req.TransportPolicy = tunnelTransportPolicyServerRelayOnly
	}
	if req.TransportPolicy != tunnelTransportPolicyServerRelayOnly && req.TransportPolicy != tunnelTransportPolicyDirectPreferred && req.TransportPolicy != tunnelTransportPolicyDirectOnly {
		return protocol.ProxyNewRequest{}, "", newProxyRequestValidationError(fmt.Errorf("unsupported transport_policy %q", req.TransportPolicy), "transport_policy", "unsupported_transport_policy", http.StatusBadRequest)
	}
	if req.TransportPolicy != tunnelTransportPolicyServerRelayOnly {
		return protocol.ProxyNewRequest{}, "", newProxyRequestValidationError(fmt.Errorf("transport policy %q requires direct transport support, which is not available in this build", req.TransportPolicy), "transport_policy", "direct_transport_unavailable", http.StatusBadRequest)
	}

	ownerClientID, err := deriveUnifiedTunnelOwner(req.Topology, req.Ingress, req.Target)
	if err != nil {
		return protocol.ProxyNewRequest{}, "", err
	}
	if err := validateUnifiedEndpointCombination(req.Topology, req.Ingress, req.Target); err != nil {
		return protocol.ProxyNewRequest{}, "", err
	}

	ingressConfig, err := decodeListenEndpointConfig(req.Ingress, req.Topology)
	if err != nil {
		return protocol.ProxyNewRequest{}, "", err
	}
	targetConfig, err := decodeServiceEndpointConfig(req.Target)
	if err != nil {
		return protocol.ProxyNewRequest{}, "", err
	}

	proxyType := ""
	switch req.Ingress.Type {
	case tunnelIngressTypeTCPListen:
		proxyType = protocol.ProxyTypeTCP
	case tunnelIngressTypeUDPListen:
		proxyType = protocol.ProxyTypeUDP
	case tunnelIngressTypeHTTPHost:
		proxyType = protocol.ProxyTypeHTTP
	}

	proxyReq := protocol.ProxyNewRequest{
		ID:                existingID,
		Name:              strings.TrimSpace(req.Name),
		Type:              proxyType,
		LocalIP:           targetConfig.IP,
		LocalPort:         targetConfig.Port,
		RemotePort:        ingressConfig.Port,
		Domain:            ingressConfig.Domain,
		BandwidthSettings: req.BandwidthSettings,
	}
	if proxyType == protocol.ProxyTypeHTTP {
		proxyReq.RemotePort = 0
	}
	return proxyReq, ownerClientID, nil
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
		return "", newProxyRequestValidationError(fmt.Errorf("unsupported topology %q", topology), "topology", "unsupported_topology", http.StatusBadRequest)
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
		code := "unsupported_target_type"
		if target.Type == "unix_socket" || target.Type == "static_file" || target.Type == "serial_device" {
			code = "future_target_type"
		}
		return newProxyRequestValidationError(fmt.Errorf("unsupported target type %q", target.Type), "target.type", code, http.StatusBadRequest)
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
			return newProxyRequestValidationError(fmt.Errorf("unsupported ingress type %q", ingress.Type), "ingress.type", "unsupported_ingress_type", http.StatusBadRequest)
		}
	case tunnelTopologyClientToClient:
		if ingress.Location != tunnelEndpointLocationClient {
			return newProxyRequestValidationError(fmt.Errorf("client_to_client ingress.location must be client"), "ingress.location", "invalid_ingress_location", http.StatusBadRequest)
		}
		if strings.TrimSpace(ingress.ClientID) == "" {
			return newProxyRequestValidationError(fmt.Errorf("ingress.client_id is required"), "ingress.client_id", "missing_client_id", http.StatusBadRequest)
		}
		if ingress.ClientID == target.ClientID {
			return newProxyRequestValidationError(fmt.Errorf("ingress and target clients must differ"), "ingress.client_id", "same_ingress_and_target_client", http.StatusBadRequest)
		}
		if ingress.Type == tunnelIngressTypeHTTPHost {
			return newProxyRequestValidationError(fmt.Errorf("client_to_client does not support http_host ingress"), "ingress.type", "unsupported_ingress_type", http.StatusBadRequest)
		}
		if ingress.Type != tunnelIngressTypeTCPListen && ingress.Type != tunnelIngressTypeUDPListen {
			return newProxyRequestValidationError(fmt.Errorf("unsupported ingress type %q", ingress.Type), "ingress.type", "unsupported_ingress_type", http.StatusBadRequest)
		}
	default:
		return newProxyRequestValidationError(fmt.Errorf("unsupported topology %q", topology), "topology", "unsupported_topology", http.StatusBadRequest)
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
				return ingressEndpointConfigAPI{}, newProxyRequestValidationError(fmt.Errorf("bind_ip is required for client_to_client ingress"), "ingress.config.bind_ip", "invalid_bind_ip", http.StatusBadRequest)
			}
			cfg.BindIP = "0.0.0.0"
		}
		ip := net.ParseIP(cfg.BindIP)
		if ip == nil || ip.To4() == nil {
			return ingressEndpointConfigAPI{}, newProxyRequestValidationError(fmt.Errorf("bind_ip must be a valid IPv4 address"), "ingress.config.bind_ip", "invalid_bind_ip", http.StatusBadRequest)
		}
		if cfg.Port < 1 || cfg.Port > 65535 {
			return ingressEndpointConfigAPI{}, newProxyRequestValidationError(fmt.Errorf("port must be in range 1-65535"), "ingress.config.port", "invalid_endpoint_config", http.StatusBadRequest)
		}
		return ingressEndpointConfigAPI{BindIP: cfg.BindIP, Port: cfg.Port}, nil
	default:
		return ingressEndpointConfigAPI{}, newProxyRequestValidationError(fmt.Errorf("unsupported ingress type %q", endpoint.Type), "ingress.type", "unsupported_ingress_type", http.StatusBadRequest)
	}
}

func decodeServiceEndpointConfig(endpoint endpointSpecAPI) (serviceConfigAPI, error) {
	var cfg serviceConfigAPI
	if err := decodeStrictEndpointConfig(endpoint.Config, &cfg); err != nil {
		return serviceConfigAPI{}, newProxyRequestValidationError(fmt.Errorf("invalid service config: %w", err), "target.config", "invalid_endpoint_config", http.StatusBadRequest)
	}
	if cfg.Host == "" {
		cfg.Host = cfg.IP
	}
	if cfg.Host == "" {
		cfg.Host = "127.0.0.1"
	}
	cfg.Host = strings.TrimSpace(cfg.Host)
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
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
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
		return StoredTunnel{}, newProxyRequestValidationError(fmt.Errorf("transport policy %q requires direct transport support, which is not available in this build", req.TransportPolicy), "transport_policy", "direct_transport_unavailable", http.StatusBadRequest)
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

	proxyType := protocol.ProxyTypeTCP
	switch req.Ingress.Type {
	case tunnelIngressTypeUDPListen:
		proxyType = protocol.ProxyTypeUDP
	case tunnelIngressTypeHTTPHost:
		proxyType = protocol.ProxyTypeHTTP
	}
	if req.Topology == tunnelTopologyClientToClient && proxyType == protocol.ProxyTypeHTTP {
		return StoredTunnel{}, newProxyRequestValidationError(fmt.Errorf("client_to_client does not support http_host ingress"), "ingress.type", "unsupported_endpoint_type", http.StatusBadRequest)
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

func (s *Server) validateUnifiedClientsAndCapabilities(req tunnelCreateRequestAPI) error {
	target, ok := s.registeredClientInfo(req.Target.ClientID)
	if !ok {
		return newProxyRequestValidationError(fmt.Errorf("unknown target client %q", req.Target.ClientID), "target.client_id", "unknown_client", http.StatusBadRequest)
	}
	if !clientSupportsTargetType(target.Info.Capabilities, req.Target.Type) {
		return newProxyRequestValidationError(fmt.Errorf("target client does not support %s", req.Target.Type), "target.type", "capability_not_supported", http.StatusBadRequest)
	}
	if req.Topology == tunnelTopologyClientToClient {
		ingress, ok := s.registeredClientInfo(req.Ingress.ClientID)
		if !ok {
			return newProxyRequestValidationError(fmt.Errorf("unknown ingress client %q", req.Ingress.ClientID), "ingress.client_id", "unknown_client", http.StatusBadRequest)
		}
		if !clientSupportsIngressType(ingress.Info.Capabilities, req.Ingress.Type) {
			return newProxyRequestValidationError(fmt.Errorf("ingress client does not support %s", req.Ingress.Type), "ingress.type", "capability_not_supported", http.StatusBadRequest)
		}
	}
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

func specFromStoredTunnel(stored StoredTunnel, s *Server) tunnelSpecAPI {
	config := storedTunnelToProxyConfig(stored)
	spec := unifiedSpecFromProxyConfig(proxyConfigForClientView(config, s.isClientOnline(stored.OwnerClientID)))
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
	spec.ActualTransport = stored.ActualTransport
	if spec.ActualTransport == "" {
		spec.ActualTransport = tunnelActualTransportUnknown
	}
	spec.P2P = p2pStateAPI{State: stored.P2P.State, Error: stored.P2P.Error, SessionID: stored.P2P.SessionID}
	if spec.P2P.State == "" {
		spec.P2P.State = tunnelP2PStateIdle
	}
	runtimeState := stored.RuntimeState
	if runtimeState == protocol.ProxyRuntimeStateExposed {
		runtimeState = tunnelRuntimeStateActive
	}
	if stored.DesiredState == protocol.ProxyDesiredStateRunning && !requiredTunnelClientsOnline(stored, s) && runtimeState != protocol.ProxyRuntimeStateError {
		runtimeState = protocol.ProxyRuntimeStateOffline
	}
	spec.RuntimeState = runtimeState
	spec.Participants = tunnelParticipantsAPI{
		Ingress: participantRuntimeAPI{ClientID: stored.Ingress.ClientID, Role: "ingress", State: participantStateForSpecRuntime(stored.Ingress.ClientID, runtimeState), Revision: stored.Revision},
		Target:  participantRuntimeAPI{ClientID: stored.Target.ClientID, Role: "target", State: participantStateForSpecRuntime(stored.Target.ClientID, runtimeState), Revision: stored.Revision},
	}
	spec.Transport = transportRuntimeAPI{
		Policy:   stored.TransportPolicy,
		Actual:   spec.ActualTransport,
		P2PState: spec.P2P.State,
	}
	spec.UpdatedAt = stored.UpdatedAt
	return spec
}

func specFromStoredTunnelConfig(config protocol.ProxyConfig, s *Server) tunnelSpecAPI {
	if s.store != nil && config.ID != "" && config.ClientID != "" {
		if stored, err := s.store.GetTunnelByIDE(config.ClientID, config.ID); err == nil {
			return specFromStoredTunnel(stored, s)
		}
	}
	return unifiedSpecFromProxyConfig(proxyConfigForClientView(config, s.isClientOnline(config.ClientID)))
}

func requiredTunnelClientsOnline(stored StoredTunnel, s *Server) bool {
	if stored.Target.ClientID != "" && !s.isClientOnline(stored.Target.ClientID) {
		return false
	}
	if stored.Ingress.Location == tunnelEndpointLocationClient && stored.Ingress.ClientID != "" && !s.isClientOnline(stored.Ingress.ClientID) {
		return false
	}
	return true
}

func participantStateForSpecRuntime(clientID, runtimeState string) string {
	if clientID == "" && runtimeState == protocol.ProxyRuntimeStateOffline {
		return "server"
	}
	switch runtimeState {
	case tunnelRuntimeStateActive, protocol.ProxyRuntimeStateExposed:
		return "ready"
	case protocol.ProxyRuntimeStatePending:
		return "provision_pending"
	case protocol.ProxyRuntimeStateOffline:
		return "offline"
	case protocol.ProxyRuntimeStateIdle:
		return "idle"
	case protocol.ProxyRuntimeStateError:
		return "error"
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

func (s *Server) allUnifiedTunnelSpecs() ([]tunnelSpecAPI, error) {
	byID := map[string]tunnelSpecAPI{}
	appendConfig := func(config protocol.ProxyConfig, online bool) {
		view := proxyConfigForClientView(config, online)
		if view.ID == "" {
			view.ID = view.Name
		}
		byID[view.ID] = unifiedSpecFromProxyConfig(view)
	}

	if s.store != nil {
		stored, err := s.store.GetAllTunnels()
		if err != nil {
			return nil, err
		}
		for _, tunnel := range stored {
			spec := specFromStoredTunnel(tunnel, s)
			if spec.ID == "" {
				spec.ID = spec.Name
			}
			byID[spec.ID] = spec
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

func (s *Server) findUnifiedTunnelSpecByID(id string) (tunnelSpecAPI, bool, error) {
	if strings.TrimSpace(id) == "" {
		return tunnelSpecAPI{}, false, nil
	}
	tunnels, err := s.allUnifiedTunnelSpecs()
	if err != nil {
		return tunnelSpecAPI{}, false, err
	}
	for _, tunnel := range tunnels {
		if tunnel.ID == id {
			return tunnel, true, nil
		}
	}
	return tunnelSpecAPI{}, false, nil
}

func (s *Server) isClientOnline(clientID string) bool {
	_, ok := s.loadLiveClient(clientID)
	return ok
}
