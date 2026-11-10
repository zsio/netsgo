package server

import (
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"netsgo/pkg/protocol"
)

const unifiedTunnelRetryInterval = time.Minute

type unifiedTunnelReconcileRegistry struct {
	mu      sync.Mutex
	entries map[string]*unifiedTunnelReconcileEntry
}

type unifiedTunnelReconcileEntry struct {
	running bool
	pending []unifiedTunnelReconcileTask
}

// unifiedTunnelReconcileTask is the authorization snapshot for one reconcile
// source. Source-triggered work fixes the owner epoch, tunnel revision, and
// participant generations at enqueue time. A periodic task is explicitly
// marked fresh and captures that snapshot only when it is executed.
type unifiedTunnelReconcileTask struct {
	TunnelID     string
	OwnerUserID  string
	OwnerEpoch   uint64
	Revision     int64
	Participants []unifiedTunnelParticipant
	Reason       string
	Fresh        bool
}

type unifiedTunnelParticipant struct {
	ClientID   string
	Generation uint64
}

type tunnelRuntimeOperationRegistry struct {
	mu      sync.Mutex
	entries map[string]*tunnelRuntimeOperationEntry
}

type tunnelRuntimeOperationEntry struct {
	mu   sync.Mutex
	refs int
}

func newTunnelRuntimeOperationRegistry() *tunnelRuntimeOperationRegistry {
	return &tunnelRuntimeOperationRegistry{entries: make(map[string]*tunnelRuntimeOperationEntry)}
}

func (r *tunnelRuntimeOperationRegistry) lock(key string) func() {
	if r == nil || key == "" {
		return func() {}
	}
	r.mu.Lock()
	entry := r.entries[key]
	if entry == nil {
		entry = &tunnelRuntimeOperationEntry{}
		r.entries[key] = entry
	}
	entry.refs++
	r.mu.Unlock()

	entry.mu.Lock()
	return func() {
		entry.mu.Unlock()
		r.mu.Lock()
		entry.refs--
		if entry.refs == 0 {
			delete(r.entries, key)
		}
		r.mu.Unlock()
	}
}

func tunnelRuntimeOperationKey(tunnelID, clientID, name string) string {
	if tunnelID != "" {
		return "id:" + tunnelID
	}
	return "legacy:" + clientID + ":" + name
}

func newUnifiedTunnelReconcileRegistry() *unifiedTunnelReconcileRegistry {
	return &unifiedTunnelReconcileRegistry{entries: make(map[string]*unifiedTunnelReconcileEntry)}
}

func (r *unifiedTunnelReconcileRegistry) run(tunnelID string, reconcile func() error) error {
	return r.runTask(unifiedTunnelReconcileTask{TunnelID: tunnelID, Fresh: true}, func(unifiedTunnelReconcileTask) error {
		return reconcile()
	})
}

func (r *unifiedTunnelReconcileRegistry) runTask(task unifiedTunnelReconcileTask, reconcile func(unifiedTunnelReconcileTask) error) error {
	if r == nil {
		return reconcile(task)
	}
	r.mu.Lock()
	entry := r.entries[task.TunnelID]
	if entry == nil {
		entry = &unifiedTunnelReconcileEntry{}
		r.entries[task.TunnelID] = entry
	}
	if entry.running {
		entry.enqueue(task)
		r.mu.Unlock()
		return nil
	}
	entry.running = true
	r.mu.Unlock()

	var lastErr error
	current := task
	for {
		if err := reconcile(current); err != nil {
			lastErr = err
		} else {
			lastErr = nil
		}

		r.mu.Lock()
		if len(entry.pending) > 0 {
			current = entry.pending[0]
			entry.pending = entry.pending[1:]
			r.mu.Unlock()
			continue
		}
		entry.running = false
		delete(r.entries, task.TunnelID)
		r.mu.Unlock()
		return lastErr
	}
}

func (e *unifiedTunnelReconcileEntry) enqueue(task unifiedTunnelReconcileTask) {
	for _, pending := range e.pending {
		if sameUnifiedTunnelReconcileTask(pending, task) {
			return
		}
	}
	e.pending = append(e.pending, task)
}

func sameUnifiedTunnelReconcileTask(a, b unifiedTunnelReconcileTask) bool {
	if a.TunnelID != b.TunnelID || a.OwnerUserID != b.OwnerUserID || a.OwnerEpoch != b.OwnerEpoch ||
		a.Revision != b.Revision || a.Fresh != b.Fresh || len(a.Participants) != len(b.Participants) {
		return false
	}
	for i := range a.Participants {
		if a.Participants[i] != b.Participants[i] {
			return false
		}
	}
	return true
}

func (s *Server) unifiedReconcileRegistry() *unifiedTunnelReconcileRegistry {
	if s == nil {
		return nil
	}
	return s.unifiedReconcile
}

func (s *Server) reconcileUnifiedTunnel(tunnelID, reason string) error {
	tunnelID = strings.TrimSpace(tunnelID)
	if tunnelID == "" {
		return fmt.Errorf("tunnel id is required for unified reconcile")
	}
	stored, ok, err := s.findStoredTunnelByID(tunnelID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrTunnelNotFound
	}
	task, err := s.captureUnifiedTunnelReconcileTask(stored, reason)
	if err != nil {
		return err
	}
	if registry := s.unifiedReconcileRegistry(); registry != nil {
		return registry.runTask(task, s.executeUnifiedTunnelReconcileTask)
	}
	return s.executeUnifiedTunnelReconcileTask(task)
}

func (s *Server) reconcileStoredUnifiedTunnelForTask(stored StoredTunnel, reason string, ownerEpoch uint64, task *unifiedTunnelReconcileTask) error {
	_ = reason // reserved for runtime diagnostics and retry scheduling.
	// Desired running state is not authorization. A disabled or unresolved
	// owner must never regain a tunnel runtime merely because the periodic
	// reconciler, a client reconnect, or server restart reaches this code path.
	if stored.DesiredState == protocol.ProxyDesiredStateRunning {
		operational, err := s.isStoredTunnelOwnerOperational(stored)
		if err != nil {
			return err
		}
		if !operational {
			// The lifecycle write gate owns disabled-user cleanup. A reconciler
			// that observed disabled must not perform cleanup after a later enable.
			return nil
		}
	}
	switch stored.Topology {
	case TunnelTopologyClientToClient:
		return s.reconcileClientRelayTunnelForTask(stored, ownerEpoch, task)
	case TunnelTopologyServerExpose, "":
		return s.reconcileServerExposeTunnelForTask(stored, ownerEpoch, task)
	default:
		return fmt.Errorf("unsupported tunnel topology %q", stored.Topology)
	}
}

func (s *Server) captureUnifiedTunnelReconcileTask(stored StoredTunnel, reason string) (unifiedTunnelReconcileTask, error) {
	return s.captureUnifiedTunnelReconcileTaskAtEpoch(stored, reason, 0)
}

func (s *Server) captureUnifiedTunnelReconcileTaskAtEpoch(stored StoredTunnel, reason string, expectedOwnerEpoch uint64) (unifiedTunnelReconcileTask, error) {
	if strings.TrimSpace(stored.ID) == "" {
		return unifiedTunnelReconcileTask{}, fmt.Errorf("tunnel id is required for unified reconcile")
	}
	// Legacy/internal mutation helpers may rely on storage to assign the legacy
	// owner and therefore return a pre-insert value without OwnerUserID. Reload
	// only that incomplete identity; never infer an owner in memory.
	if strings.TrimSpace(stored.OwnerUserID) == "" {
		current, ok, err := s.findStoredTunnelByID(stored.ID)
		if err != nil {
			return unifiedTunnelReconcileTask{}, err
		}
		if !ok || current.Revision != stored.Revision {
			return unifiedTunnelReconcileTask{}, ErrTunnelNotFound
		}
		stored = current
	}
	ownerUserID := strings.TrimSpace(stored.OwnerUserID)
	if ownerUserID == "" {
		return unifiedTunnelReconcileTask{}, fmt.Errorf("tunnel %q has no user owner", stored.ID)
	}
	requireOperational := stored.DesiredState == protocol.ProxyDesiredStateRunning
	ownerEpoch, releaseOwnerGate, err := s.acquireUserLifecycleRead(ownerUserID, expectedOwnerEpoch, requireOperational)
	if err != nil {
		return unifiedTunnelReconcileTask{}, err
	}
	defer releaseOwnerGate()
	s.clientTunnelMutationMu.Lock()
	defer s.clientTunnelMutationMu.Unlock()
	return s.newUnifiedTunnelReconcileTaskAtEpoch(stored, reason, ownerEpoch)
}

// newUnifiedTunnelReconcileTaskAtEpoch builds work from an already-authorized
// epoch. Callers normally hold the lifecycle gate; retry queues may instead use
// an epoch captured earlier because execution revalidates every field.
func (s *Server) newUnifiedTunnelReconcileTaskAtEpoch(stored StoredTunnel, reason string, ownerEpoch uint64) (unifiedTunnelReconcileTask, error) {
	ownerUserID := strings.TrimSpace(stored.OwnerUserID)
	if strings.TrimSpace(stored.ID) == "" || ownerUserID == "" || ownerEpoch == 0 {
		return unifiedTunnelReconcileTask{}, fmt.Errorf("incomplete unified reconcile task identity")
	}
	participantIDs := make(map[string]struct{}, 3)
	for _, clientID := range []string{stored.OwnerClientID, stored.Ingress.ClientID, stored.Target.ClientID} {
		if clientID = strings.TrimSpace(clientID); clientID != "" {
			participantIDs[clientID] = struct{}{}
		}
	}
	participants := make([]unifiedTunnelParticipant, 0, len(participantIDs))
	for clientID := range participantIDs {
		participant := unifiedTunnelParticipant{ClientID: clientID}
		if value, ok := s.clients.Load(clientID); ok {
			participant.Generation = value.(*ClientConn).generation
		}
		participants = append(participants, participant)
	}
	sort.Slice(participants, func(i, j int) bool { return participants[i].ClientID < participants[j].ClientID })
	return unifiedTunnelReconcileTask{
		TunnelID:     stored.ID,
		OwnerUserID:  ownerUserID,
		OwnerEpoch:   ownerEpoch,
		Revision:     stored.Revision,
		Participants: participants,
		Reason:       reason,
	}, nil
}

// newUnifiedTunnelReconcileTaskForGenerationsAtEpoch is used by sources such
// as P2P whose own session snapshot is the authority for participant
// generations. It must not replace those archived generations with whatever
// happens to be in the live client map when delayed work is enqueued.
func (s *Server) newUnifiedTunnelReconcileTaskForGenerationsAtEpoch(stored StoredTunnel, reason string, ownerEpoch uint64, generations map[string]uint64) (unifiedTunnelReconcileTask, error) {
	task, err := s.newUnifiedTunnelReconcileTaskAtEpoch(stored, reason, ownerEpoch)
	if err != nil {
		return unifiedTunnelReconcileTask{}, err
	}
	for i := range task.Participants {
		generation, ok := generations[task.Participants[i].ClientID]
		if !ok || generation == 0 {
			return unifiedTunnelReconcileTask{}, fmt.Errorf("missing fixed generation for unified tunnel participant %q", task.Participants[i].ClientID)
		}
		task.Participants[i].Generation = generation
	}
	return task, nil
}

func (s *Server) executeUnifiedTunnelReconcileTask(task unifiedTunnelReconcileTask) error {
	if task.Fresh {
		stored, ok, err := s.findStoredTunnelByID(task.TunnelID)
		if err != nil {
			return err
		}
		if !ok {
			return ErrTunnelNotFound
		}
		captured, err := s.captureUnifiedTunnelReconcileTask(stored, task.Reason)
		if err != nil {
			return err
		}
		return s.executeFixedUnifiedTunnelReconcileTask(captured)
	}
	return s.executeFixedUnifiedTunnelReconcileTask(task)
}

func (s *Server) executeFixedUnifiedTunnelReconcileTask(task unifiedTunnelReconcileTask) error {
	s.runUserLifecycleHook("unified_reconcile_before_execute", task.OwnerUserID)
	defer s.runUserLifecycleHook("unified_reconcile_after_execute", task.OwnerUserID)
	stored, ok, err := s.findStoredTunnelByID(task.TunnelID)
	if err != nil {
		return err
	}
	if !ok || stored.OwnerUserID != task.OwnerUserID || stored.Revision != task.Revision {
		return nil
	}
	requireOperational := stored.DesiredState == protocol.ProxyDesiredStateRunning
	_, releaseOwnerGate, err := s.acquireUserLifecycleRead(task.OwnerUserID, task.OwnerEpoch, requireOperational)
	if err != nil {
		if errors.Is(err, ErrUserLifecycleEpochChanged) || errors.Is(err, ErrUserDisabled) || errors.Is(err, ErrUserNotFound) {
			return nil
		}
		return err
	}
	s.clientTunnelMutationMu.Lock()
	current, currentErr := s.unifiedTunnelReconcileTaskCurrentLocked(task)
	s.clientTunnelMutationMu.Unlock()
	releaseOwnerGate()
	if currentErr != nil {
		return currentErr
	}
	if !current {
		return nil
	}
	s.runUserLifecycleHook("unified_reconcile_after_initial_participant_check", task.OwnerUserID)
	err = s.reconcileStoredUnifiedTunnelForTask(stored, task.Reason, task.OwnerEpoch, &task)
	if errors.Is(err, ErrUserLifecycleEpochChanged) || errors.Is(err, ErrUserDisabled) ||
		errors.Is(err, ErrUserNotFound) {
		return nil
	}
	return err
}

func (s *Server) unifiedTunnelParticipantsCurrentLocked(task unifiedTunnelReconcileTask) bool {
	for _, expected := range task.Participants {
		value, ok := s.clients.Load(expected.ClientID)
		if expected.Generation == 0 {
			if ok {
				return false
			}
			continue
		}
		if !ok {
			return false
		}
		client := value.(*ClientConn)
		if client.generation != expected.Generation || client.OwnerUserID != task.OwnerUserID || client.OwnerEpoch != task.OwnerEpoch {
			return false
		}
	}
	return true
}

// unifiedTunnelReconcileTaskCurrentLocked verifies the complete fixed-work
// identity while clientTunnelMutationMu is held. Callers use it immediately
// before every publication or control send so a same-epoch client replacement
// cannot make old source work act on the replacement generation.
func (s *Server) unifiedTunnelReconcileTaskCurrentLocked(task unifiedTunnelReconcileTask) (bool, error) {
	stored, ok, err := s.findStoredTunnelByID(task.TunnelID)
	if err != nil {
		return false, err
	}
	if !ok || stored.OwnerUserID != task.OwnerUserID || stored.Revision != task.Revision {
		return false, nil
	}
	return s.unifiedTunnelParticipantsCurrentLocked(task), nil
}

func (s *Server) loadUnifiedTunnelTaskLiveClient(task *unifiedTunnelReconcileTask, clientID string) (*ClientConn, bool) {
	client, ok := s.loadLiveClient(clientID)
	if !ok || task == nil {
		return client, ok
	}
	for _, expected := range task.Participants {
		if expected.ClientID != clientID {
			continue
		}
		if expected.Generation == 0 || client.generation != expected.Generation ||
			client.OwnerUserID != task.OwnerUserID || client.OwnerEpoch != task.OwnerEpoch {
			return nil, false
		}
		return client, true
	}
	return nil, false
}

func (s *Server) withUnifiedTunnelReconcilePublication(stored StoredTunnel, ownerEpoch uint64, task *unifiedTunnelReconcileTask, publish func() error) error {
	if task == nil {
		return s.withStoredTunnelPublication(stored, ownerEpoch, publish)
	}
	_, releaseGate, err := s.acquireStoredTunnelLifecycle(stored, task.OwnerEpoch)
	if err != nil {
		return err
	}
	defer releaseGate()
	s.clientTunnelMutationMu.Lock()
	defer s.clientTunnelMutationMu.Unlock()
	releaseRuntimeOperation := s.tunnelRuntimeOps.lock(tunnelRuntimeOperationKey(stored.ID, stored.OwnerClientID, stored.Name))
	defer releaseRuntimeOperation()
	current, err := s.unifiedTunnelReconcileTaskCurrentLocked(*task)
	if err != nil {
		return err
	}
	if !current {
		return ErrUserLifecycleEpochChanged
	}
	return publish()
}

// isStoredTunnelOwnerOperational resolves the persisted user owner for every
// runtime reconcile. Missing storage, an empty owner, a missing user, and an
// unknown status all fail closed; only an explicit active user returns true.
func (s *Server) isStoredTunnelOwnerOperational(stored StoredTunnel) (bool, error) {
	if s == nil || s.auth == nil || s.auth.adminStore == nil {
		return false, fmt.Errorf("user store is unavailable while reconciling tunnel")
	}
	if strings.TrimSpace(stored.OwnerUserID) == "" {
		return false, fmt.Errorf("tunnel %q has no user owner", stored.ID)
	}
	operational, err := s.auth.adminStore.IsUserOperational(stored.OwnerUserID)
	if err != nil {
		return false, fmt.Errorf("resolve tunnel owner %q: %w", stored.OwnerUserID, err)
	}
	return operational, nil
}

func (s *Server) scheduleUnifiedTunnelReconcile(stored StoredTunnel, reason string) {
	task, err := s.captureUnifiedTunnelReconcileTask(stored, reason)
	if err != nil {
		s.logUnifiedTunnelReconcileCaptureError(stored, reason, err)
		return
	}
	s.scheduleCapturedUnifiedTunnelReconcile(stored, task)
}

func (s *Server) logUnifiedTunnelReconcileCaptureError(stored StoredTunnel, reason string, err error) {
	if errors.Is(err, ErrUserLifecycleEpochChanged) || errors.Is(err, ErrUserDisabled) || errors.Is(err, ErrUserNotFound) {
		return
	}
	log.Printf("⚠️ unified tunnel reconcile capture failed: id=%s name=%s topology=%s reason=%s err=%v", stored.ID, stored.Name, stored.Topology, reason, err)
}

func (s *Server) scheduleCapturedUnifiedTunnelReconcile(stored StoredTunnel, task unifiedTunnelReconcileTask) {
	if s == nil {
		return
	}
	tunnelID := strings.TrimSpace(task.TunnelID)
	if tunnelID == "" {
		return
	}
	if s.done != nil {
		select {
		case <-s.done:
			return
		default:
		}
	}
	go func() {
		if s.done != nil {
			select {
			case <-s.done:
				return
			default:
			}
		}
		if err := s.unifiedReconcileRegistry().runTask(task, s.executeUnifiedTunnelReconcileTask); err != nil {
			log.Printf("⚠️ unified tunnel reconcile failed: id=%s name=%s topology=%s reason=%s err=%v", stored.ID, stored.Name, stored.Topology, task.Reason, err)
		}
	}()
}

func (s *Server) unifiedTunnelReconcileLoop() {
	ticker := time.NewTicker(unifiedTunnelRetryInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.done:
			return
		case <-ticker.C:
			s.reconcileRunningUnifiedTunnels("retry")
		}
	}
}

func (s *Server) reconcileRunningUnifiedTunnels(reason string) {
	if s == nil || s.store == nil {
		return
	}
	tunnels, err := s.store.GetAllTunnels()
	if err != nil {
		return
	}
	for _, stored := range tunnels {
		if stored.DesiredState != protocol.ProxyDesiredStateRunning {
			continue
		}
		task := unifiedTunnelReconcileTask{TunnelID: stored.ID, Reason: reason, Fresh: true}
		if err := s.unifiedReconcileRegistry().runTask(task, s.executeUnifiedTunnelReconcileTask); err != nil {
			log.Printf("⚠️ unified tunnel retry failed: id=%s name=%s topology=%s reason=%s err=%v", stored.ID, stored.Name, stored.Topology, reason, err)
		}
	}
}

func (s *Server) findStoredTunnelByID(tunnelID string) (StoredTunnel, bool, error) {
	if s.store == nil {
		return StoredTunnel{}, false, fmt.Errorf("tunnel store not initialized")
	}
	stored, err := s.store.GetTunnelByID(tunnelID)
	if errors.Is(err, ErrTunnelNotFound) {
		return StoredTunnel{}, false, nil
	}
	if err != nil {
		return StoredTunnel{}, false, err
	}
	return stored, true, nil
}

func (s *Server) reconcileServerExposeTunnel(stored StoredTunnel) error {
	ownerEpoch, releaseOwnerGate, err := s.acquireStoredTunnelLifecycle(stored, 0)
	if err != nil {
		return err
	}
	releaseOwnerGate()
	return s.reconcileServerExposeTunnelAtEpoch(stored, ownerEpoch)
}

func (s *Server) reconcileServerExposeTunnelAtEpoch(stored StoredTunnel, ownerEpoch uint64) error {
	return s.reconcileServerExposeTunnelForTask(stored, ownerEpoch, nil)
}

func (s *Server) reconcileServerExposeTunnelForTask(stored StoredTunnel, ownerEpoch uint64, task *unifiedTunnelReconcileTask) error {
	publish := func(fn func() error) error {
		return s.withUnifiedTunnelReconcilePublication(stored, ownerEpoch, task, fn)
	}
	if stored.DesiredState == protocol.ProxyDesiredStateStopped {
		return publish(func() error {
			s.unifiedRuntime.clearTunnelIssues(stored.ID, stored.Revision)
			if err := s.unprovisionServerExposeTunnel(stored, "stopped", false); err != nil {
				return err
			}
			return s.updateStoredTunnelRuntime(stored, protocol.ProxyRuntimeStateIdle, "")
		})
	}

	client, ok := s.loadUnifiedTunnelTaskLiveClient(task, stored.OwnerClientID)
	if !ok || !clientHasDataSession(client) {
		return publish(func() error {
			s.unifiedRuntime.clearTunnelIssues(stored.ID, stored.Revision)
			if ok {
				if err := s.unprovisionServerExposeTunnel(stored, "participant_offline", false); err != nil {
					log.Printf("⚠️ failed to unprovision server-expose tunnel %s after participant offline: %v", stored.ID, err)
				}
			}
			return s.updateStoredTunnelRuntime(stored, protocol.ProxyRuntimeStateOffline, "")
		})
	}
	if issues := s.capabilityIssuesForStoredTunnel(stored); len(issues) > 0 {
		return publish(func() error {
			s.unifiedRuntime.clearTunnelIssues(stored.ID, stored.Revision)
			if err := s.unprovisionServerExposeTunnel(stored, "capability_not_supported", false); err != nil {
				log.Printf("⚠️ failed to unprovision server-expose tunnel %s after capability loss: %v", stored.ID, err)
			}
			return s.updateStoredTunnelRuntime(stored, protocol.ProxyRuntimeStateError, issues[0].Message)
		})
	}

	shouldRestore := true
	notifyRuntimeRetry := false
	err := publish(func() error {
		if name, tunnel, exists := findTunnelBySelector(client, stored.ID); exists {
			config, runtimeHeld, stillExists := serverExposeTunnelSnapshot(client, name, tunnel)
			if !stillExists {
				shouldRestore = false
				return nil
			}
			if config.ID != stored.ID || config.Revision != stored.Revision {
				return errTunnelProvisionAckCancelled
			}
			if config.DesiredState == protocol.ProxyDesiredStateRunning && runtimeHeld {
				if s.unifiedRuntime.hasIssuesForStoredTunnel(stored, true) {
					if !s.discardTunnelRuntimeIfCurrent(client, name, tunnel, stored.ID, stored.Revision) {
						return errTunnelProvisionAckCancelled
					}
					notifyRuntimeRetry = true
				} else {
					shouldRestore = false
					updated, err := s.transitionStoredTunnelRuntimeIfCurrent(stored, stored.RuntimeState, protocol.ProxyRuntimeStateExposed, "")
					if err != nil {
						return err
					}
					if !updated {
						return errTunnelProvisionAckCancelled
					}
					return nil
				}
			}
			if config.DesiredState == protocol.ProxyDesiredStateRunning && config.RuntimeState == protocol.ProxyRuntimeStatePending {
				shouldRestore = false
				updated, err := s.transitionStoredTunnelRuntimeIfCurrent(stored, stored.RuntimeState, protocol.ProxyRuntimeStatePending, "")
				if err != nil {
					return err
				}
				if !updated {
					return errTunnelProvisionAckCancelled
				}
				return nil
			}
			if config.DesiredState == protocol.ProxyDesiredStateStopped {
				shouldRestore = false
				s.unifiedRuntime.clearTunnelIssues(stored.ID, stored.Revision)
				if err := s.unprovisionServerExposeTunnel(stored, "stopped", false); err != nil {
					return err
				}
				return s.updateStoredTunnelRuntime(stored, protocol.ProxyRuntimeStateIdle, "")
			}
			if !runtimeHeld && !s.discardTunnelRuntimeIfCurrent(client, name, tunnel, stored.ID, stored.Revision) {
				return errTunnelProvisionAckCancelled
			}
		}

		s.unifiedRuntime.clearTunnelIssues(stored.ID, stored.Revision)
		return nil
	})
	if err != nil || !shouldRestore {
		return err
	}
	if notifyRuntimeRetry {
		if err := publish(func() error {
			return s.notifyClientTunnelUnprovision(client, stored.ID, stored.Revision, protocol.DataStreamRoleTarget, "retrying_after_runtime_issue")
		}); err != nil &&
			!errors.Is(err, ErrUserLifecycleEpochChanged) && !errors.Is(err, ErrUserDisabled) && !errors.Is(err, errTunnelProvisionAckCancelled) {
			log.Printf("⚠️ failed to unprovision server-expose target %s after runtime issue: %v", stored.ID, err)
		}
	}
	if err := s.restoreUnifiedServerExposeTunnel(client, stored, ownerEpoch, task); err != nil {
		if !errors.Is(err, errTunnelProvisionAckCancelled) {
			_ = publish(func() error {
				s.recordServerExposeReconcileIssue(stored, err)
				return nil
			})
		}
		return err
	}
	return nil
}

func serverExposeTunnelSnapshot(client *ClientConn, name string, tunnel *ProxyTunnel) (protocol.ProxyConfig, bool, bool) {
	client.proxyMu.RLock()
	defer client.proxyMu.RUnlock()
	current := client.proxies[name]
	if current == nil || current != tunnel {
		return protocol.ProxyConfig{}, false, false
	}
	config := current.Config
	return config, serverExposeRuntimeHeld(current, config), true
}

func serverExposeRuntimeHeld(tunnel *ProxyTunnel, config protocol.ProxyConfig) bool {
	if tunnel == nil || !isTunnelExposed(config) || !proxyActivationDoneOpen(tunnel.done) {
		return false
	}
	switch config.Type {
	case protocol.ProxyTypeHTTP, protocol.ProxyTypeTLS:
		return true
	case protocol.ProxyTypeUDP:
		return tunnel.UDPState != nil
	default:
		return tunnel.Listener != nil
	}
}

func (s *Server) recordServerExposeReconcileIssue(stored StoredTunnel, err error) {
	if err == nil {
		return
	}
	var rejected *tunnelProvisionRejectedError
	switch {
	case errors.Is(err, errTunnelProvisionAckTimeout):
		s.recordServerExposeProvisionIssue(stored, protocol.TunnelIssueCodeProvisionAckTimeout, err)
	case errors.Is(err, errTunnelProvisionAckCancelled):
		s.recordServerExposeProvisionIssue(stored, protocol.TunnelIssueCodeProvisionAckCancelled, err)
	case errors.As(err, &rejected):
		s.recordServerExposeProvisionIssue(stored, protocol.TunnelIssueCodeProvisionAckRejected, err)
	default:
		s.recordServerExposeIngressIssue(stored.ID, stored.Revision, stored.Ingress.Type, err.Error())
	}
}

func (s *Server) recordServerExposeProvisionIssue(stored StoredTunnel, code string, err error) {
	s.unifiedRuntime.recordServerIssue(stored.ID, stored.Revision, protocol.TunnelIssue{
		Code:       code,
		Scope:      "target_client",
		ClientID:   stored.Target.ClientID,
		Severity:   "error",
		Message:    tunnelProvisionErrorMessage(err),
		Retryable:  true,
		ObservedAt: time.Now().UTC(),
	})
}

func (s *Server) recordServerExposeIngressIssue(tunnelID string, revision int64, ingressType, message string) {
	message = strings.TrimSpace(message)
	if tunnelID == "" || revision <= 0 || message == "" {
		return
	}
	s.unifiedRuntime.recordServerIssue(tunnelID, revision, protocol.TunnelIssue{
		Code:       serverExposeIngressIssueCode(ingressType, message),
		Scope:      "server",
		Severity:   "error",
		Message:    message,
		Retryable:  true,
		ObservedAt: time.Now().UTC(),
	})
}

func serverExposeIngressIssueCode(ingressType, message string) string {
	if isDomainIngressType(ingressType) || ingressType == protocol.ProxyTypeHTTP || ingressType == protocol.ProxyTypeTLS {
		return protocol.TunnelIssueCodeIngressRouteFailed
	}
	lower := strings.ToLower(message)
	if strings.Contains(lower, "address already in use") || strings.Contains(lower, "only one usage of each socket address") {
		return protocol.TunnelIssueCodeIngressPortInUse
	}
	return protocol.TunnelIssueCodeIngressListenFailed
}

func (s *Server) reconcileTunnelsForClientGeneration(client *ClientConn, reason string) {
	if client == nil {
		return
	}
	clientID := client.ID
	if s == nil || s.store == nil || strings.TrimSpace(clientID) == "" {
		return
	}
	tunnels, err := s.store.GetAllTunnels()
	if err != nil {
		return
	}
	for _, stored := range tunnels {
		if stored.OwnerClientID == clientID || stored.Target.ClientID == clientID || stored.Ingress.ClientID == clientID {
			if err := s.reconcileUnifiedTunnelForClientGeneration(client, stored.ID, reason); err != nil {
				if errors.Is(err, ErrUserLifecycleEpochChanged) || errors.Is(err, ErrUserDisabled) || errors.Is(err, ErrUserNotFound) {
					return
				}
				log.Printf("⚠️ unified tunnel reconcile for client failed: client=%s id=%s name=%s reason=%s err=%v", clientID, stored.ID, stored.Name, reason, err)
			}
		}
	}
}

func (s *Server) reconcileNonOwnerTunnelsForClientGeneration(client *ClientConn, reason string) {
	if client == nil {
		return
	}
	clientID := client.ID
	if s == nil || s.store == nil || strings.TrimSpace(clientID) == "" {
		return
	}
	tunnels, err := s.store.GetAllTunnels()
	if err != nil {
		return
	}
	for _, stored := range tunnels {
		if stored.OwnerClientID == clientID || stored.ClientID == clientID {
			continue
		}
		if stored.Target.ClientID == clientID || stored.Ingress.ClientID == clientID {
			if err := s.reconcileUnifiedTunnelForClientGeneration(client, stored.ID, reason); err != nil {
				if errors.Is(err, ErrUserLifecycleEpochChanged) || errors.Is(err, ErrUserDisabled) || errors.Is(err, ErrUserNotFound) {
					return
				}
				log.Printf("⚠️ related unified tunnel reconcile for client failed: client=%s id=%s name=%s reason=%s err=%v", clientID, stored.ID, stored.Name, reason, err)
			}
		}
	}
}

// reconcileUnifiedTunnelForClientGeneration captures source-triggered work
// while the exact client generation is still current. The execution may run
// later, but it can no longer reinterpret an old attach/restore trigger as work
// for a same-epoch replacement connection.
func (s *Server) reconcileUnifiedTunnelForClientGeneration(client *ClientConn, tunnelID, reason string) error {
	var task unifiedTunnelReconcileTask
	var stored StoredTunnel
	var captureErr error
	if !s.withClientRuntimePublication(client, func() {
		var ok bool
		stored, ok, captureErr = s.findStoredTunnelByID(tunnelID)
		if captureErr != nil || !ok {
			if captureErr == nil {
				captureErr = ErrTunnelNotFound
			}
			return
		}
		if stored.OwnerUserID != client.OwnerUserID {
			captureErr = ErrUserLifecycleEpochChanged
			return
		}
		task, captureErr = s.newUnifiedTunnelReconcileTaskAtEpoch(stored, reason, client.OwnerEpoch)
	}) {
		return ErrUserLifecycleEpochChanged
	}
	if captureErr != nil {
		return captureErr
	}
	if registry := s.unifiedReconcileRegistry(); registry != nil {
		return registry.runTask(task, s.executeUnifiedTunnelReconcileTask)
	}
	return s.executeUnifiedTunnelReconcileTask(task)
}

func (s *Server) releaseUnifiedRuntimeForClient(clientID string) {
	if s == nil || s.store == nil || strings.TrimSpace(clientID) == "" {
		return
	}
	tunnels, err := s.store.GetAllTunnels()
	if err != nil {
		return
	}
	for _, stored := range tunnels {
		if stored.OwnerClientID == clientID || stored.Target.ClientID == clientID || stored.Ingress.ClientID == clientID {
			if err := s.unprovisionClientRelayTunnel(stored, "participant_session_released"); err != nil {
				log.Printf("⚠️ failed to release unified runtime for client %s tunnel %s: %v", clientID, stored.ID, err)
			}
		}
	}
}
