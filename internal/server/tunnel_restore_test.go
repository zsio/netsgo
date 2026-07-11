package server

import (
	"errors"
	"testing"
	"time"

	"netsgo/pkg/protocol"
)

func configureRestoreBarrierAdminStore(t *testing.T, s *Server, action string) {
	t.Helper()
	store := newTestAdminStore(t)
	if err := store.Initialize(
		"admin",
		"Admin1234",
		"https://example.com",
		[]PortRange{{Start: 20000, End: 20010}},
	); err != nil {
		t.Fatalf("initialize admin store: %v", err)
	}
	s.auth.adminStore = store
	if action == restorePlaceholderActionStorageUnavailable {
		if err := store.Close(); err != nil {
			t.Fatalf("close admin store: %v", err)
		}
	}
}

func TestRestoreTunnelPlaceholderBarrierRejectsLateRevisionOrDelete(t *testing.T) {
	actions := []string{
		restorePlaceholderActionStorageUnavailable,
		restorePlaceholderActionPortNotAllowed,
		restorePlaceholderActionStopped,
	}
	mutations := []string{"revision", "delete"}

	for _, action := range actions {
		action := action
		for _, mutation := range mutations {
			mutation := mutation
			t.Run(action+"/"+mutation, func(t *testing.T) {
				s := New(0)
				s.store = newTestTunnelStore(t)
				configureRestoreBarrierAdminStore(t, s, action)

				stored := testStoredServerExposeTCPTunnel(
					"restore-barrier-"+action+"-"+mutation,
					"restore-barrier-"+action+"-"+mutation,
					"restore-barrier-client-"+action+"-"+mutation,
					8080,
					19090,
					time.Now().UTC(),
				)
				if action == restorePlaceholderActionStopped {
					stored.DesiredState = protocol.ProxyDesiredStateStopped
					stored.RuntimeState = protocol.ProxyRuntimeStateIdle
					stored.ActualTransport = protocol.ActualTransportUnknown
				}
				mustAddStableTunnel(t, s.store, stored)

				client := &ClientConn{
					ID:         stored.OwnerClientID,
					generation: 1,
					state:      clientStateLive,
					proxies:    make(map[string]*ProxyTunnel),
				}
				s.clients.Store(client.ID, client)

				eventsCh := s.events.Subscribe()
				defer s.events.Unsubscribe(eventsCh)

				hookCalls := 0
				var newRuntime *ProxyTunnel
				s.restorePlaceholderBeforeInstallHook = func(got StoredTunnel, gotAction string) {
					hookCalls++
					if gotAction != action {
						t.Fatalf("restore action: want %q, got %q", action, gotAction)
					}
					if got.ID != stored.ID || got.Revision != stored.Revision {
						t.Fatalf("restore hook identity mismatch: %+v", got)
					}

					switch mutation {
					case "revision":
						next := stored
						next.Revision = stored.Revision + 1
						next.UpdatedAt = time.Now().UTC()
						if err := s.store.ReplaceTunnelByID(stored.OwnerClientID, stored.ID, stored.Revision, next); err != nil {
							t.Fatalf("advance tunnel revision: %v", err)
						}
						reloaded, err := s.store.GetTunnelByIDE(stored.OwnerClientID, stored.ID)
						if err != nil {
							t.Fatalf("reload advanced tunnel: %v", err)
						}
						newRuntime = &ProxyTunnel{
							Config: storedTunnelToProxyConfig(reloaded),
							done:   make(chan struct{}),
						}
						initializeTunnelRuntimeFromState(newRuntime, client.ID, time.Now())
						client.proxyMu.Lock()
						client.proxies[stored.Name] = newRuntime
						client.proxyMu.Unlock()
					case "delete":
						if err := s.store.RemoveTunnelByID(stored.OwnerClientID, stored.ID); err != nil {
							t.Fatalf("delete tunnel at restore barrier: %v", err)
						}
					default:
						t.Fatalf("unsupported mutation %q", mutation)
					}
				}

				s.restoreTunnels(client)
				if hookCalls != 1 {
					t.Fatalf("restore barrier hook calls: want 1, got %d", hookCalls)
				}

				switch mutation {
				case "revision":
					got, err := s.store.GetTunnelByIDE(stored.OwnerClientID, stored.ID)
					if err != nil {
						t.Fatalf("load new tunnel revision: %v", err)
					}
					if got.Revision != stored.Revision+1 ||
						got.DesiredState != stored.DesiredState ||
						got.RuntimeState != stored.RuntimeState ||
						got.Error != stored.Error {
						t.Fatalf("new stored revision was overwritten: %+v", got)
					}
					client.proxyMu.RLock()
					current := client.proxies[stored.Name]
					client.proxyMu.RUnlock()
					if current != newRuntime || current.Config.Revision != stored.Revision+1 {
						t.Fatalf("new runtime was overwritten by stale placeholder: %+v", current)
					}
				case "delete":
					if _, err := s.store.GetTunnelByIDE(stored.OwnerClientID, stored.ID); !errors.Is(err, ErrTunnelNotFound) {
						t.Fatalf("deleted tunnel was recreated or changed: %v", err)
					}
					client.proxyMu.RLock()
					_, exists := client.proxies[stored.Name]
					client.proxyMu.RUnlock()
					if exists {
						t.Fatal("deleted tunnel left a ghost runtime placeholder")
					}
				}

				assertNoTunnelChangedEvent(t, eventsCh, 100*time.Millisecond, "")
			})
		}
	}
}
