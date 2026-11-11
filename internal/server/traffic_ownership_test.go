package server

import (
	"net/http"
	"testing"
	"time"

	"netsgo/pkg/protocol"
)

type trafficOwnerFixture struct {
	server     *Server
	handler    http.Handler
	adminToken string

	ownerA  User
	ownerB  User
	clientA RegisteredClient
	clientB RegisteredClient
	tunnelA StoredTunnel
	tunnelB StoredTunnel
	store   *TrafficStore
}

func newTrafficOwnerFixture(t *testing.T) trafficOwnerFixture {
	t.Helper()

	s, handler, adminToken, cleanup := setupTestServerWithStores(t, true)
	t.Cleanup(cleanup)

	var ownerA User
	ownerA.Username = "admin"
	if err := s.auth.adminStore.db.QueryRow(`SELECT id FROM users WHERE username = ?`, ownerA.Username).Scan(&ownerA.ID); err != nil {
		t.Fatalf("load first traffic owner: %v", err)
	}
	ownerB, err := s.auth.adminStore.CreateUser("traffic-owner-b", "Password123")
	if err != nil {
		t.Fatalf("create second traffic owner: %v", err)
	}

	clientA, err := s.auth.adminStore.GetOrCreateClientForUser(ownerA.ID, "traffic-owner-a-install", protocol.ClientInfo{Hostname: "traffic-owner-a"}, "127.0.0.1")
	if err != nil {
		t.Fatalf("register first traffic client: %v", err)
	}
	clientB, err := s.auth.adminStore.GetOrCreateClientForUser(ownerB.ID, "traffic-owner-b-install", protocol.ClientInfo{Hostname: "traffic-owner-b"}, "127.0.0.2")
	if err != nil {
		t.Fatalf("register second traffic client: %v", err)
	}

	createdAt := time.Now().UTC()
	tunnelA := testStoredServerExposeTCPTunnel("traffic-owner-a-tunnel", "owner-a-tunnel", clientA.ID, 8080, 18080, createdAt)
	tunnelA.OwnerUserID = ownerA.ID
	if _, err := s.store.AddTunnelForUser(ownerA.ID, tunnelA, nil); err != nil {
		t.Fatalf("add first owner tunnel: %v", err)
	}
	tunnelB := testStoredServerExposeTCPTunnel("traffic-owner-b-tunnel", "owner-b-tunnel", clientB.ID, 8081, 18081, createdAt)
	tunnelB.OwnerUserID = ownerB.ID
	if _, err := s.store.AddTunnelForUser(ownerB.ID, tunnelB, nil); err != nil {
		t.Fatalf("add second owner tunnel: %v", err)
	}

	trafficStore := newTrafficStoreWithDB(s.auth.adminStore.path, s.auth.adminStore.db, false)
	s.store.attachTrafficStore(trafficStore, s.trafficAccumulator)
	s.trafficStore = trafficStore

	return trafficOwnerFixture{
		server:     s,
		handler:    handler,
		adminToken: adminToken,
		ownerA:     ownerA,
		ownerB:     ownerB,
		clientA:    *clientA,
		clientB:    *clientB,
		tunnelA:    tunnelA,
		tunnelB:    tunnelB,
		store:      trafficStore,
	}
}

func trafficOwnerDelta(tunnel StoredTunnel, at time.Time, ingressBytes, egressBytes uint64, includeSecond bool) TrafficDelta {
	delta := trafficDeltaFromStoredTunnel(tunnel, ingressBytes, egressBytes)
	delta.MinuteStart = minuteFloorUTC(at).Unix()
	if includeSecond {
		delta.SecondStart = secondFloorUTC(at).Unix()
	}
	return delta
}

func TestTrafficStoreQueryWithResolutionForUserSeparatesSecondAndHistoricalBuckets(t *testing.T) {
	fixture := newTrafficOwnerFixture(t)
	now := secondFloorUTC(time.Now().UTC())

	fixture.store.ApplyDeltas([]TrafficDelta{
		trafficOwnerDelta(fixture.tunnelA, now, 11, 7, true),
		trafficOwnerDelta(fixture.tunnelB, now, 101, 71, true),
	})

	resultA, err := fixture.store.QueryWithResolutionForUser(fixture.ownerA.ID, fixture.clientA.ID, "", now.Add(-time.Second), now.Add(time.Second), TrafficResolutionSecond)
	if err != nil {
		t.Fatalf("query first owner seconds: %v", err)
	}
	if got := mustSingleSeries(t, resultA, fixture.tunnelA.Name).Points[0]; got.IngressBytes != 11 || got.EgressBytes != 7 {
		t.Fatalf("first owner seconds = %+v", got)
	}

	crossOwner, err := fixture.store.QueryWithResolutionForUser(fixture.ownerB.ID, fixture.clientA.ID, "", now.Add(-time.Second), now.Add(time.Second), TrafficResolutionSecond)
	if err != nil {
		t.Fatalf("cross-owner seconds query: %v", err)
	}
	if len(crossOwner.Items) != 0 {
		t.Fatalf("second buckets leaked across owner scope: %+v", crossOwner.Items)
	}

	if err := fixture.store.Flush(); err != nil {
		t.Fatalf("flush minute buckets: %v", err)
	}
	minuteA, err := fixture.store.QueryWithResolutionForUser(fixture.ownerA.ID, fixture.clientA.ID, "", now.Add(-time.Minute), now.Add(time.Minute), TrafficResolutionMinute)
	if err != nil {
		t.Fatalf("query first owner minutes: %v", err)
	}
	if got := mustSingleSeries(t, minuteA, fixture.tunnelA.Name).Points[0]; got.IngressBytes != 11 || got.EgressBytes != 7 {
		t.Fatalf("first owner minute = %+v", got)
	}

	past := hourFloorUTC(now).Add(-2*time.Hour + 5*time.Minute)
	fixture.store.ApplyDeltas([]TrafficDelta{
		trafficOwnerDelta(fixture.tunnelA, past, 19, 3, false),
		trafficOwnerDelta(fixture.tunnelB, past, 191, 31, false),
	})
	if err := fixture.store.Flush(); err != nil {
		t.Fatalf("flush historical minute buckets: %v", err)
	}
	if err := fixture.store.Compact(now); err != nil {
		t.Fatalf("roll up historical minute buckets: %v", err)
	}
	hourA, err := fixture.store.QueryWithResolutionForUser(fixture.ownerA.ID, fixture.clientA.ID, "", past.Truncate(time.Hour), past.Truncate(time.Hour).Add(time.Hour), TrafficResolutionHour)
	if err != nil {
		t.Fatalf("query first owner hours: %v", err)
	}
	if got := mustSingleSeries(t, hourA, fixture.tunnelA.Name).Points[0]; got.IngressBytes != 19 || got.EgressBytes != 3 {
		t.Fatalf("first owner hour = %+v", got)
	}
	crossOwner, err = fixture.store.QueryWithResolutionForUser(fixture.ownerB.ID, fixture.clientA.ID, "", past.Truncate(time.Hour), past.Truncate(time.Hour).Add(time.Hour), TrafficResolutionHour)
	if err != nil {
		t.Fatalf("cross-owner hour query: %v", err)
	}
	if len(crossOwner.Items) != 0 {
		t.Fatalf("hour buckets leaked across owner scope: %+v", crossOwner.Items)
	}
}

func TestServerTrafficOwnerResolutionPersistsTunnelAndClientOwner(t *testing.T) {
	fixture := newTrafficOwnerFixture(t)
	now := secondFloorUTC(time.Now().UTC())

	fixture.server.recordTunnelTrafficAt(now, fixture.clientA.ID, protocol.ProxyConfig{
		ID:            fixture.tunnelA.ID,
		Name:          fixture.tunnelA.Name,
		Type:          fixture.tunnelA.Type,
		OwnerClientID: fixture.clientA.ID,
	}, 23, 5)
	fixture.server.flushTrafficObservations()
	if err := fixture.store.Flush(); err != nil {
		t.Fatalf("flush stored-tunnel traffic: %v", err)
	}

	var persistedOwner string
	if err := fixture.store.db.QueryRow(`SELECT owner_user_id FROM traffic_buckets WHERE tunnel_id = ?`, fixture.tunnelA.ID).Scan(&persistedOwner); err != nil {
		t.Fatalf("load stored-tunnel owner snapshot: %v", err)
	}
	if persistedOwner != fixture.ownerA.ID {
		t.Fatalf("stored-tunnel owner snapshot = %q, want %q", persistedOwner, fixture.ownerA.ID)
	}

	const runtimeOnlyTunnelID = "traffic-owner-runtime-only"
	fixture.server.recordTunnelTrafficAt(now, fixture.clientA.ID, protocol.ProxyConfig{
		ID:            runtimeOnlyTunnelID,
		Name:          "runtime-only",
		Type:          protocol.ProxyTypeTCP,
		OwnerClientID: fixture.clientA.ID,
	}, 29, 11)
	fixture.server.flushTrafficObservations()
	if err := fixture.store.Flush(); err != nil {
		t.Fatalf("flush client-owner traffic: %v", err)
	}
	if err := fixture.store.db.QueryRow(`SELECT owner_user_id FROM traffic_buckets WHERE tunnel_id = ?`, runtimeOnlyTunnelID).Scan(&persistedOwner); err != nil {
		t.Fatalf("load client-owner traffic snapshot: %v", err)
	}
	if persistedOwner != fixture.ownerA.ID {
		t.Fatalf("client-owner snapshot = %q, want %q", persistedOwner, fixture.ownerA.ID)
	}
}

func TestTrafficAPIUsesResourceScopeForOwnerTraffic(t *testing.T) {
	fixture := newTrafficOwnerFixture(t)
	now := secondFloorUTC(time.Now().UTC())
	fixture.store.ApplyDeltas([]TrafficDelta{
		trafficOwnerDelta(fixture.tunnelA, now, 31, 13, false),
		trafficOwnerDelta(fixture.tunnelB, now, 131, 113, false),
	})
	if err := fixture.store.Flush(); err != nil {
		t.Fatalf("flush API traffic: %v", err)
	}

	query := "?from=" + itoa(now.Add(-time.Minute).Unix()) + "&to=" + itoa(now.Add(time.Minute).Unix()) + "&resolution=minute"
	adminSelf := doMuxRequest(t, fixture.handler, http.MethodGet, "/api/clients/"+fixture.clientA.ID+"/traffic"+query, fixture.adminToken, nil)
	if adminSelf.Code != http.StatusOK {
		t.Fatalf("admin self traffic = %d: %s", adminSelf.Code, adminSelf.Body.String())
	}
	var selfResult TrafficQueryResult
	if err := mustDecodeJSON(t, adminSelf.Body, &selfResult); err != nil {
		t.Fatalf("decode admin self traffic: %v", err)
	}
	if len(selfResult.Items) != 1 || selfResult.Items[0].TunnelID != fixture.tunnelA.ID {
		t.Fatalf("admin self traffic leaked or missed scoped data: %+v", selfResult.Items)
	}

	ownerBToken := loginAdminTokenLocal(t, fixture.handler, fixture.ownerB.Username, "Password123")
	normalCrossOwner := doMuxRequest(t, fixture.handler, http.MethodGet, "/api/clients/"+fixture.clientA.ID+"/traffic"+query, ownerBToken, nil)
	if normalCrossOwner.Code != http.StatusNotFound {
		t.Fatalf("normal user cross-owner traffic = %d, want 404: %s", normalCrossOwner.Code, normalCrossOwner.Body.String())
	}

	adminTarget := doMuxRequest(t, fixture.handler, http.MethodGet, "/api/admin/users/"+fixture.ownerB.ID+"/clients/"+fixture.clientB.ID+"/traffic"+query, fixture.adminToken, nil)
	if adminTarget.Code != http.StatusOK {
		t.Fatalf("admin target traffic = %d: %s", adminTarget.Code, adminTarget.Body.String())
	}
	var targetResult TrafficQueryResult
	if err := mustDecodeJSON(t, adminTarget.Body, &targetResult); err != nil {
		t.Fatalf("decode admin target traffic: %v", err)
	}
	if len(targetResult.Items) != 1 || targetResult.Items[0].TunnelID != fixture.tunnelB.ID {
		t.Fatalf("admin target traffic leaked or missed scoped data: %+v", targetResult.Items)
	}
}

func TestCollectRealtimeTrafficEventsGroupsPayloadsByOwner(t *testing.T) {
	fixture := newTrafficOwnerFixture(t)
	now := secondFloorUTC(time.Now().UTC()).Add(2 * time.Second)
	sample := now.Add(-time.Second)
	fixture.store.ApplyDeltas([]TrafficDelta{
		trafficOwnerDelta(fixture.tunnelA, sample, 41, 17, true),
		trafficOwnerDelta(fixture.tunnelB, sample, 141, 117, true),
	})
	fixture.server.clients.Store(fixture.clientA.ID, &ClientConn{ID: fixture.clientA.ID, OwnerUserID: fixture.ownerA.ID, state: clientStateLive, proxies: make(map[string]*ProxyTunnel)})
	fixture.server.clients.Store(fixture.clientB.ID, &ClientConn{ID: fixture.clientB.ID, OwnerUserID: fixture.ownerB.ID, state: clientStateLive, proxies: make(map[string]*ProxyTunnel)})

	eventsByOwner := fixture.server.collectRealtimeTrafficEvents(now)
	if len(eventsByOwner) != 2 {
		t.Fatalf("realtime owner events = %+v", eventsByOwner)
	}
	first := eventsByOwner[fixture.ownerA.ID]
	if len(first.Clients) != 1 || first.Clients[0].ClientID != fixture.clientA.ID || len(first.Clients[0].Items) != 1 || first.Clients[0].Items[0].TunnelID != fixture.tunnelA.ID {
		t.Fatalf("first owner realtime payload leaked or missed data: %+v", first)
	}
	second := eventsByOwner[fixture.ownerB.ID]
	if len(second.Clients) != 1 || second.Clients[0].ClientID != fixture.clientB.ID || len(second.Clients[0].Items) != 1 || second.Clients[0].Items[0].TunnelID != fixture.tunnelB.ID {
		t.Fatalf("second owner realtime payload leaked or missed data: %+v", second)
	}
}

func TestRealtimeTrafficBatchAPIUsesResourceScopeAndAggregatesClients(t *testing.T) {
	fixture := newTrafficOwnerFixture(t)
	extraClient, err := fixture.server.auth.adminStore.GetOrCreateClientForUser(
		fixture.ownerA.ID,
		"traffic-owner-a-install-2",
		protocol.ClientInfo{Hostname: "owner-a-2"},
		"127.0.0.3",
	)
	if err != nil {
		t.Fatalf("register second client for ownerA: %v", err)
	}
	extraTunnel := testStoredServerExposeTCPTunnel(
		"traffic-owner-tunnel-a-2",
		"owner-a-http",
		extraClient.ID,
		8082,
		28080,
		time.Now().UTC(),
	)
	extraTunnel.OwnerUserID = fixture.ownerA.ID
	if _, err := fixture.server.store.AddTunnelForUser(fixture.ownerA.ID, extraTunnel, nil); err != nil {
		t.Fatalf("save extra tunnel: %v", err)
	}

	sample := secondFloorUTC(time.Now().UTC()).Add(-time.Second)
	fixture.store.ApplyDeltas([]TrafficDelta{
		trafficOwnerDelta(fixture.tunnelA, sample, 51, 19, true),
		trafficOwnerDelta(extraTunnel, sample, 61, 29, true),
		trafficOwnerDelta(fixture.tunnelB, sample, 151, 119, true),
	})
	fixture.server.clients.Store(fixture.clientA.ID, &ClientConn{ID: fixture.clientA.ID, OwnerUserID: fixture.ownerA.ID, state: clientStateLive, proxies: make(map[string]*ProxyTunnel)})
	fixture.server.clients.Store(extraClient.ID, &ClientConn{ID: extraClient.ID, OwnerUserID: fixture.ownerA.ID, state: clientStateLive, proxies: make(map[string]*ProxyTunnel)})
	fixture.server.clients.Store(fixture.clientB.ID, &ClientConn{ID: fixture.clientB.ID, OwnerUserID: fixture.ownerB.ID, state: clientStateLive, proxies: make(map[string]*ProxyTunnel)})

	adminSelf := doMuxRequest(t, fixture.handler, http.MethodGet, "/api/traffic/realtime", fixture.adminToken, nil)
	if adminSelf.Code != http.StatusOK {
		t.Fatalf("admin self realtime traffic = %d: %s", adminSelf.Code, adminSelf.Body.String())
	}
	var selfEvent realtimeTrafficEvent
	if err := mustDecodeJSON(t, adminSelf.Body, &selfEvent); err != nil {
		t.Fatalf("decode admin self realtime traffic: %v", err)
	}
	if len(selfEvent.Clients) != 2 {
		t.Fatalf("admin self realtime clients = %+v, want 2 clients", selfEvent.Clients)
	}
	gotIDs := []string{selfEvent.Clients[0].ClientID, selfEvent.Clients[1].ClientID}
	wantIDs := []string{fixture.clientA.ID, extraClient.ID}
	if wantIDs[0] > wantIDs[1] {
		wantIDs[0], wantIDs[1] = wantIDs[1], wantIDs[0]
	}
	if gotIDs[0] != wantIDs[0] || gotIDs[1] != wantIDs[1] {
		t.Fatalf("admin self realtime client ids = %v, want %v", gotIDs, wantIDs)
	}

	ownerBToken := loginAdminTokenLocal(t, fixture.handler, fixture.ownerB.Username, "Password123")
	ownerBSelf := doMuxRequest(t, fixture.handler, http.MethodGet, "/api/traffic/realtime", ownerBToken, nil)
	if ownerBSelf.Code != http.StatusOK {
		t.Fatalf("ownerB self realtime traffic = %d: %s", ownerBSelf.Code, ownerBSelf.Body.String())
	}
	var ownerBEvent realtimeTrafficEvent
	if err := mustDecodeJSON(t, ownerBSelf.Body, &ownerBEvent); err != nil {
		t.Fatalf("decode ownerB self realtime traffic: %v", err)
	}
	if len(ownerBEvent.Clients) != 1 || ownerBEvent.Clients[0].ClientID != fixture.clientB.ID {
		t.Fatalf("ownerB realtime traffic leaked or missed scoped data: %+v", ownerBEvent.Clients)
	}

	nonAdminCrossScope := doMuxRequest(t, fixture.handler, http.MethodGet, "/api/admin/users/"+fixture.ownerA.ID+"/traffic/realtime", ownerBToken, nil)
	if nonAdminCrossScope.Code != http.StatusForbidden {
		t.Fatalf("non-admin target-user realtime traffic = %d, want 403", nonAdminCrossScope.Code)
	}

	adminTarget := doMuxRequest(t, fixture.handler, http.MethodGet, "/api/admin/users/"+fixture.ownerB.ID+"/traffic/realtime", fixture.adminToken, nil)
	if adminTarget.Code != http.StatusOK {
		t.Fatalf("admin target realtime traffic = %d: %s", adminTarget.Code, adminTarget.Body.String())
	}
	var targetEvent realtimeTrafficEvent
	if err := mustDecodeJSON(t, adminTarget.Body, &targetEvent); err != nil {
		t.Fatalf("decode admin target realtime traffic: %v", err)
	}
	if len(targetEvent.Clients) != 1 || targetEvent.Clients[0].ClientID != fixture.clientB.ID {
		t.Fatalf("admin target realtime traffic = %+v, want client %s", targetEvent.Clients, fixture.clientB.ID)
	}
}
