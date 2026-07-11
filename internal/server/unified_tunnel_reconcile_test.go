package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"netsgo/internal/socks5wire"
	"netsgo/pkg/protocol"
)

func testStoredC2CTunnelForReconcile(id, name, desired, runtime string, ingressPort int) StoredTunnel {
	now := time.Now().UTC()
	return StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{
			ID:         id,
			Name:       name,
			Type:       protocol.ProxyTypeTCP,
			LocalIP:    "127.0.0.1",
			LocalPort:  22,
			RemotePort: ingressPort,
		},
		ClientID:        "target-client",
		OwnerClientID:   "target-client",
		Binding:         TunnelBindingClientID,
		Revision:        1,
		Topology:        TunnelTopologyClientToClient,
		DesiredState:    desired,
		RuntimeState:    runtime,
		TransportPolicy: protocol.TransportPolicyServerRelayOnly,
		ActualTransport: protocol.ActualTransportUnknown,
		P2P:             P2PState{State: TunnelP2PStateIdle},
		Ingress: EndpointSpec{
			Location: protocol.EndpointLocationClient,
			ClientID: "ingress-client",
			Type:     protocol.IngressTypeTCPListen,
			Config:   mustRawJSON(tcpListenConfigAPI{BindIP: "127.0.0.1", Port: ingressPort}),
		},
		Target: EndpointSpec{
			Location: protocol.EndpointLocationClient,
			ClientID: "target-client",
			Type:     protocol.TargetTypeTCPService,
			Config:   mustRawJSON(serviceConfigAPI{IP: "127.0.0.1", Port: 22}),
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func TestReconcileRunningUnifiedTunnelsSkipsStoppedAndProjectsOffline(t *testing.T) {
	s := New(0)
	s.store = newTestTunnelStore(t)

	running := testStoredC2CTunnelForReconcile("running-c2c", "running-c2c", protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateError, 22022)
	stopped := testStoredC2CTunnelForReconcile("stopped-c2c", "stopped-c2c", protocol.ProxyDesiredStateStopped, protocol.ProxyRuntimeStateIdle, 22023)
	mustAddStableTunnel(t, s.store, running)
	mustAddStableTunnel(t, s.store, stopped)

	s.reconcileRunningUnifiedTunnels("test")

	gotRunning, err := s.store.GetTunnelByIDE(running.OwnerClientID, running.ID)
	if err != nil {
		t.Fatalf("load running tunnel: %v", err)
	}
	if gotRunning.DesiredState != protocol.ProxyDesiredStateRunning || gotRunning.RuntimeState != protocol.ProxyRuntimeStateOffline {
		t.Fatalf("running tunnel should be reconciled to running/offline without live clients, got %s/%s", gotRunning.DesiredState, gotRunning.RuntimeState)
	}

	gotStopped, err := s.store.GetTunnelByIDE(stopped.OwnerClientID, stopped.ID)
	if err != nil {
		t.Fatalf("load stopped tunnel: %v", err)
	}
	if gotStopped.DesiredState != protocol.ProxyDesiredStateStopped || gotStopped.RuntimeState != protocol.ProxyRuntimeStateIdle {
		t.Fatalf("stopped tunnel should be skipped by retry reconcile, got %s/%s", gotStopped.DesiredState, gotStopped.RuntimeState)
	}
}

func TestScheduleUnifiedTunnelReconcileAfterShutdownDoesNotMutateState(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "unified_tunnel_reconcile.go", nil, 0)
	if err != nil {
		t.Fatalf("parse unified_tunnel_reconcile.go: %v", err)
	}
	fn := findFuncDecl(file, "scheduleUnifiedTunnelReconcile")
	if fn == nil || fn.Body == nil {
		t.Fatal("scheduleUnifiedTunnelReconcile not found")
	}
	body := fn.Body

	sawDoneGuardBeforeGo := false
	for _, stmt := range body.List {
		if _, ok := stmt.(*ast.GoStmt); ok {
			if !sawDoneGuardBeforeGo {
				t.Fatal("scheduleUnifiedTunnelReconcile must check s.done before starting reconcile goroutine")
			}
			return
		}
		if stmtContainsDoneGuard(stmt) {
			sawDoneGuardBeforeGo = true
		}
	}
	t.Fatal("scheduleUnifiedTunnelReconcile does not start a reconcile goroutine")
}

func TestUnifiedMutationEventsPublishBeforeSchedulingReconcile(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "unified_tunnel_api.go", nil, 0)
	if err != nil {
		t.Fatalf("parse unified_tunnel_api.go: %v", err)
	}
	tests := []struct {
		function string
		emitCall string
	}{
		{function: "createUnifiedStoredTunnel", emitCall: "emitTunnelChangedIfStored"},
		{function: "updateUnifiedStoredTunnel", emitCall: "emitTunnelChangedIfStored"},
		{function: "migrateUnifiedStoredTunnel", emitCall: "emitMigratedTunnelOwnerEvents"},
	}
	for _, tc := range tests {
		t.Run(tc.function, func(t *testing.T) {
			fn := findFuncDecl(file, tc.function)
			if fn == nil || fn.Body == nil {
				t.Fatalf("function %s not found", tc.function)
			}
			emitPos := firstSelectorCallPosition(fn.Body, tc.emitCall)
			schedulePos := firstSelectorCallPosition(fn.Body, "scheduleUnifiedTunnelReconcile")
			if emitPos == token.NoPos || schedulePos == token.NoPos {
				t.Fatalf("missing event/schedule calls: emit=%v schedule=%v", emitPos, schedulePos)
			}
			if emitPos > schedulePos {
				t.Fatalf("%s must publish %s before scheduling reconcile", tc.function, tc.emitCall)
			}
		})
	}
}

func firstSelectorCallPosition(node ast.Node, name string) token.Pos {
	position := token.NoPos
	ast.Inspect(node, func(node ast.Node) bool {
		if position != token.NoPos {
			return false
		}
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if ok && selector.Sel.Name == name {
			position = call.Pos()
			return false
		}
		return true
	})
	return position
}

func findFuncDecl(file *ast.File, name string) *ast.FuncDecl {
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name.Name == name {
			return fn
		}
	}
	return nil
}

func stmtContainsDoneGuard(stmt ast.Stmt) bool {
	found := false
	ast.Inspect(stmt, func(node ast.Node) bool {
		if found {
			return false
		}
		selectStmt, ok := node.(*ast.SelectStmt)
		if !ok {
			return true
		}
		if selectHasDoneReturnCase(selectStmt) {
			found = true
			return false
		}
		return true
	})
	return found
}

func selectHasDoneReturnCase(selectStmt *ast.SelectStmt) bool {
	for _, stmt := range selectStmt.Body.List {
		comm, ok := stmt.(*ast.CommClause)
		if !ok || !isDoneReceive(comm.Comm) {
			continue
		}
		for _, bodyStmt := range comm.Body {
			if _, ok := bodyStmt.(*ast.ReturnStmt); ok {
				return true
			}
		}
	}
	return false
}

func isDoneReceive(stmt ast.Stmt) bool {
	exprStmt, ok := stmt.(*ast.ExprStmt)
	if !ok {
		return false
	}
	unary, ok := exprStmt.X.(*ast.UnaryExpr)
	if !ok || unary.Op != token.ARROW {
		return false
	}
	selector, ok := unary.X.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "done" {
		return false
	}
	ident, ok := selector.X.(*ast.Ident)
	return ok && ident.Name == "s"
}

func TestUnifiedReconcileRegistrySerializesSameTunnelAndRerunsDirty(t *testing.T) {
	registry := newUnifiedTunnelReconcileRegistry()
	firstEntered := make(chan struct{})
	secondAttemptDone := make(chan struct{})
	allowFirstReturn := make(chan struct{})
	var mu sync.Mutex
	started := 0
	maxConcurrent := 0
	running := 0

	reconcile := func() error {
		mu.Lock()
		started++
		running++
		if running > maxConcurrent {
			maxConcurrent = running
		}
		currentRun := started
		mu.Unlock()

		if currentRun == 1 {
			close(firstEntered)
			<-allowFirstReturn
		}

		mu.Lock()
		running--
		mu.Unlock()
		return nil
	}

	firstDone := make(chan error, 1)
	go func() {
		firstDone <- registry.run("same-tunnel", reconcile)
	}()
	<-firstEntered

	go func() {
		_ = registry.run("same-tunnel", reconcile)
		close(secondAttemptDone)
	}()

	select {
	case <-secondAttemptDone:
	case <-time.After(time.Second):
		t.Fatal("second same-tunnel reconcile call should return immediately without blocking")
	}

	mu.Lock()
	if started != 1 || maxConcurrent != 1 {
		t.Fatalf("same tunnel reconcile ran concurrently before dirty rerun: started=%d maxConcurrent=%d", started, maxConcurrent)
	}
	mu.Unlock()

	close(allowFirstReturn)
	select {
	case err := <-firstDone:
		if err != nil {
			t.Fatalf("first reconcile returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for dirty rerun to finish")
	}

	mu.Lock()
	defer mu.Unlock()
	if started != 2 {
		t.Fatalf("same tunnel dirty reconcile should rerun once after the active run completes, got %d run(s)", started)
	}
	if maxConcurrent != 1 {
		t.Fatalf("same tunnel reconcile must not run concurrently, maxConcurrent=%d", maxConcurrent)
	}
}

func TestUnifiedReconcileRegistryCoalescesMultipleDirtyCallsIntoSingleRerun(t *testing.T) {
	registry := newUnifiedTunnelReconcileRegistry()
	firstEntered := make(chan struct{})
	allowFirstReturn := make(chan struct{})
	var mu sync.Mutex
	started := 0
	maxConcurrent := 0
	running := 0

	reconcile := func() error {
		mu.Lock()
		started++
		running++
		if running > maxConcurrent {
			maxConcurrent = running
		}
		currentRun := started
		mu.Unlock()

		if currentRun == 1 {
			close(firstEntered)
			<-allowFirstReturn
		}

		mu.Lock()
		running--
		mu.Unlock()
		return nil
	}

	firstDone := make(chan error, 1)
	go func() {
		firstDone <- registry.run("same-tunnel", reconcile)
	}()
	<-firstEntered

	const dirtyCalls = 3
	dirtyDone := make(chan error, dirtyCalls)
	for i := 0; i < dirtyCalls; i++ {
		go func() {
			dirtyDone <- registry.run("same-tunnel", reconcile)
		}()
	}
	for i := 0; i < dirtyCalls; i++ {
		select {
		case err := <-dirtyDone:
			if err != nil {
				t.Fatalf("dirty same-tunnel call returned error: %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("dirty same-tunnel calls should return without blocking behind the active reconcile")
		}
	}

	mu.Lock()
	if started != 1 || maxConcurrent != 1 {
		t.Fatalf("same tunnel dirty calls ran concurrently before coalesced rerun: started=%d maxConcurrent=%d", started, maxConcurrent)
	}
	mu.Unlock()

	close(allowFirstReturn)
	select {
	case err := <-firstDone:
		if err != nil {
			t.Fatalf("first reconcile returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for coalesced dirty rerun to finish")
	}

	mu.Lock()
	defer mu.Unlock()
	if started != 2 {
		t.Fatalf("multiple dirty calls should coalesce into one rerun after the active run completes, got %d run(s)", started)
	}
	if maxConcurrent != 1 {
		t.Fatalf("same tunnel reconciles must stay serialized, maxConcurrent=%d", maxConcurrent)
	}
}

func TestUnifiedReconcileRegistryAllowsDifferentTunnelsInParallel(t *testing.T) {
	registry := newUnifiedTunnelReconcileRegistry()
	firstEntered := make(chan struct{})
	secondEntered := make(chan struct{})
	allowReturn := make(chan struct{})
	var mu sync.Mutex
	running := 0
	maxConcurrent := 0

	reconcile := func(entered chan<- struct{}) func() error {
		return func() error {
			mu.Lock()
			running++
			if running > maxConcurrent {
				maxConcurrent = running
			}
			mu.Unlock()
			close(entered)
			<-allowReturn
			mu.Lock()
			running--
			mu.Unlock()
			return nil
		}
	}

	done := make(chan error, 2)
	go func() { done <- registry.run("tunnel-a", reconcile(firstEntered)) }()
	go func() { done <- registry.run("tunnel-b", reconcile(secondEntered)) }()

	select {
	case <-firstEntered:
	case <-time.After(time.Second):
		t.Fatal("first tunnel reconcile did not start")
	}
	select {
	case <-secondEntered:
	case <-time.After(time.Second):
		t.Fatal("different tunnel reconcile should be allowed to run in parallel")
	}

	close(allowReturn)
	for i := 0; i < 2; i++ {
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("reconcile returned error: %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for parallel reconciles to finish")
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if maxConcurrent < 2 {
		t.Fatalf("different tunnel reconciles should be able to overlap, maxConcurrent=%d", maxConcurrent)
	}
}

func TestUnifiedReconcileRegistryReturnsReconcileErrorAndCleansEntry(t *testing.T) {
	registry := newUnifiedTunnelReconcileRegistry()
	wantErr := errors.New("reconcile failed")
	if err := registry.run("error-tunnel", func() error {
		return wantErr
	}); !errors.Is(err, wantErr) {
		t.Fatalf("registry should return reconcile error, got %v want %v", err, wantErr)
	}

	runs := 0
	if err := registry.run("error-tunnel", func() error {
		runs++
		return nil
	}); err != nil {
		t.Fatalf("registry should clean failed entry and allow a later reconcile, got %v", err)
	}
	if runs != 1 {
		t.Fatalf("later reconcile should run exactly once after failed entry cleanup, got %d", runs)
	}
}

func TestRestoreTunnelsReconcilesNonOwnerClientRelayParticipant(t *testing.T) {
	s := New(0)
	s.store = newTestTunnelStore(t)

	stored := testStoredC2CTunnelForReconcile(
		"related-c2c",
		"related-c2c",
		protocol.ProxyDesiredStateRunning,
		protocol.ProxyRuntimeStateOffline,
		22024,
	)
	mustAddStableTunnel(t, s.store, stored)

	caps := protocol.DefaultClientCapabilities()
	_, ingressSession := newTestClientRelayDataSession(t)
	_, targetSession := newTestClientRelayDataSession(t)
	ingressClient := &ClientConn{
		ID:          stored.Ingress.ClientID,
		Info:        protocol.ClientInfo{Capabilities: &caps},
		dataSession: ingressSession,
		generation:  1,
		state:       clientStateLive,
		proxies:     make(map[string]*ProxyTunnel),
	}
	targetClient := &ClientConn{
		ID:          stored.Target.ClientID,
		Info:        protocol.ClientInfo{Capabilities: &caps},
		dataSession: targetSession,
		generation:  1,
		state:       clientStateLive,
		proxies:     make(map[string]*ProxyTunnel),
	}
	s.clients.Store(ingressClient.ID, ingressClient)
	s.clients.Store(targetClient.ID, targetClient)

	s.restoreTunnels(ingressClient)

	got, err := s.store.GetTunnelByIDE(stored.OwnerClientID, stored.ID)
	if err != nil {
		t.Fatalf("load related tunnel: %v", err)
	}
	if got.RuntimeState != protocol.ProxyRuntimeStateError {
		t.Fatalf("non-owner participant restore should reconcile related tunnel, got runtime_state=%q", got.RuntimeState)
	}
	spec := specFromStoredTunnel(got, s)
	if len(spec.Issues) == 0 || spec.Issues[0].Code != protocol.TunnelIssueCodeProvisionAckRejected {
		t.Fatalf("related reconcile should record provisioning issue after control write failure, got %+v", spec.Issues)
	}
}

func TestRestoreTunnelsReconcilesRunningErrorTunnel(t *testing.T) {
	s := New(0)
	s.store = newTestTunnelStore(t)

	stored := testStoredC2CTunnelForReconcile(
		"error-c2c",
		"error-c2c",
		protocol.ProxyDesiredStateRunning,
		protocol.ProxyRuntimeStateError,
		22025,
	)
	stored.Error = "old persisted failure"
	mustAddStableTunnel(t, s.store, stored)

	caps := protocol.DefaultClientCapabilities()
	_, ingressSession := newTestClientRelayDataSession(t)
	_, targetSession := newTestClientRelayDataSession(t)
	targetClient := &ClientConn{
		ID:          stored.Target.ClientID,
		Info:        protocol.ClientInfo{Capabilities: &caps},
		dataSession: targetSession,
		generation:  1,
		state:       clientStateLive,
		proxies:     make(map[string]*ProxyTunnel),
	}
	ingressClient := &ClientConn{
		ID:          stored.Ingress.ClientID,
		Info:        protocol.ClientInfo{Capabilities: &caps},
		dataSession: ingressSession,
		generation:  1,
		state:       clientStateLive,
		proxies:     make(map[string]*ProxyTunnel),
	}
	s.clients.Store(targetClient.ID, targetClient)
	s.clients.Store(ingressClient.ID, ingressClient)

	s.restoreTunnels(targetClient)

	got, err := s.store.GetTunnelByIDE(stored.OwnerClientID, stored.ID)
	if err != nil {
		t.Fatalf("load restored tunnel: %v", err)
	}
	spec := specFromStoredTunnel(got, s)
	if len(spec.Issues) == 0 || spec.Issues[0].Code != protocol.TunnelIssueCodeProvisionAckRejected {
		t.Fatalf("running/error restore should attempt fresh reconcile and record current issue, state=%q issues=%+v", got.RuntimeState, spec.Issues)
	}
	if got.Error == "old persisted failure" {
		t.Fatal("running/error restore reused stale persisted runtime error")
	}
}

func TestUnifiedServerExposeProvisionAndDataHeaderUseStoredRevision(t *testing.T) {
	s := New(0)
	s.store = newTestTunnelStore(t)

	reservedListener, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("reserve remote port: %v", err)
	}
	remotePort := reservedListener.Addr().(*net.TCPAddr).Port
	t.Cleanup(func() {
		if reservedListener != nil {
			_ = reservedListener.Close()
		}
	})

	stored := StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{
			ID:         "server-expose-unified-id",
			Name:       "server-expose-unified",
			Type:       protocol.ProxyTypeTCP,
			LocalIP:    "192.0.2.50",
			LocalPort:  65022,
			RemotePort: remotePort,
		},
		ClientID:        "target-client",
		OwnerClientID:   "target-client",
		Binding:         TunnelBindingClientID,
		Revision:        9,
		Topology:        TunnelTopologyServerExpose,
		DesiredState:    protocol.ProxyDesiredStateRunning,
		RuntimeState:    protocol.ProxyRuntimeStateOffline,
		TransportPolicy: protocol.TransportPolicyServerRelayOnly,
		ActualTransport: protocol.ActualTransportUnknown,
		P2P:             P2PState{State: TunnelP2PStateIdle},
		Ingress: EndpointSpec{
			Location: protocol.EndpointLocationServer,
			Type:     protocol.IngressTypeTCPListen,
			Config:   mustRawJSON(tcpListenConfigAPI{BindIP: "0.0.0.0", Port: remotePort}),
		},
		Target: EndpointSpec{
			Location: protocol.EndpointLocationClient,
			ClientID: "target-client",
			Type:     protocol.TargetTypeTCPService,
			Config:   mustRawJSON(serviceConfigAPI{IP: "127.0.0.1", Port: 22}),
		},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := stored.normalize(); err != nil {
		t.Fatalf("normalize stored tunnel: %v", err)
	}
	mustAddStableTunnel(t, s.store, stored)

	targetWS, targetServerWS := newTestWebSocketPair(t)
	defer mustClose(t, targetWS)
	defer mustClose(t, targetServerWS)
	clientSession, serverSession := newTestClientRelayDataSession(t)
	caps := protocol.DefaultClientCapabilities()
	target := &ClientConn{
		ID:          stored.Target.ClientID,
		Info:        protocol.ClientInfo{Hostname: "target-client", Capabilities: &caps},
		conn:        targetServerWS,
		proxies:     make(map[string]*ProxyTunnel),
		dataSession: serverSession,
		generation:  1,
		state:       clientStateLive,
	}
	s.clients.Store(target.ID, target)
	go s.controlLoop(target)

	eventsCh := s.events.Subscribe()
	defer s.events.Unsubscribe(eventsCh)

	restoreDone := make(chan error, 1)
	go func() {
		restoreDone <- s.reconcileServerExposeTunnel(stored)
	}()
	pendingPayload := waitForTunnelChangedEvent(t, eventsCh, "pending", stored.Name)
	if got, _ := pendingPayload["runtime_state"].(string); got != protocol.ProxyRuntimeStatePending {
		t.Fatalf("pending event runtime_state: want %s, got %s", protocol.ProxyRuntimeStatePending, got)
	}
	msg := readControlMessageOfType(t, targetWS, protocol.MsgTypeTunnelProvision)
	var provision protocol.TunnelProvisionRequest
	if err := msg.ParsePayload(&provision); err != nil {
		t.Fatalf("parse provision payload: %v", err)
	}
	if provision.TunnelID == "" {
		t.Fatalf("expected unified tunnel provision payload, got empty tunnel_id: %+v", provision)
	}
	if provision.TunnelID != stored.ID || provision.Revision != stored.Revision || provision.Role != protocol.DataStreamRoleTarget {
		t.Fatalf("provision identity mismatch: %+v", provision)
	}
	if provision.Spec.Topology != TunnelTopologyServerExpose || provision.Spec.Target.ClientID != stored.Target.ClientID {
		t.Fatalf("provision spec mismatch: %+v", provision.Spec)
	}
	var targetCfg serviceConfigAPI
	if err := json.Unmarshal(provision.Spec.Target.Config, &targetCfg); err != nil {
		t.Fatalf("decode provision target config: %v", err)
	}
	if targetCfg.IP != "127.0.0.1" || targetCfg.Port != 22 {
		t.Fatalf("provision target config must come from endpoint config, not embedded flat fields: %+v", targetCfg)
	}
	if err := reservedListener.Close(); err != nil {
		t.Fatalf("release remote port: %v", err)
	}
	reservedListener = nil
	ack, err := protocol.NewMessage(protocol.MsgTypeTunnelProvisionAck, protocol.TunnelProvisionAck{
		TunnelID: provision.TunnelID,
		Revision: provision.Revision,
		Role:     provision.Role,
		Accepted: true,
		Message:  "ok",
	})
	if err != nil {
		t.Fatalf("build provision ack: %v", err)
	}
	if err := targetWS.WriteJSON(ack); err != nil {
		t.Fatalf("write provision ack: %v", err)
	}
	select {
	case err := <-restoreDone:
		if err != nil {
			t.Fatalf("restore unified server-expose: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for restore")
	}
	restoredPayload := waitForTunnelChangedEvent(t, eventsCh, "restored", stored.Name)
	if got, _ := restoredPayload["runtime_state"].(string); got != protocol.ProxyRuntimeStateExposed {
		t.Fatalf("restored event runtime_state: want %s, got %s", protocol.ProxyRuntimeStateExposed, got)
	}
	snapshot := s.collectSnapshot()
	if len(snapshot.Clients) != 1 || len(snapshot.Clients[0].Proxies) != 1 {
		t.Fatalf("snapshot should include one restored tunnel, got %+v", snapshot.Clients)
	}
	if got := snapshot.Clients[0].Proxies[0].RuntimeState; got != protocol.ProxyRuntimeStateExposed {
		t.Fatalf("snapshot runtime_state after restore: want %s, got %s", protocol.ProxyRuntimeStateExposed, got)
	}
	persisted, err := s.store.GetTunnelByIDE(stored.OwnerClientID, stored.ID)
	if err != nil {
		t.Fatalf("load restored stored tunnel: %v", err)
	}
	if persisted.RuntimeState != protocol.ProxyRuntimeStateExposed {
		t.Fatalf("stored runtime_state after restore: want %s, got %s", protocol.ProxyRuntimeStateExposed, persisted.RuntimeState)
	}
	t.Cleanup(func() {
		_ = s.CloseProxyRuntime(target, stored.Name)
	})

	type openResult struct {
		stream net.Conn
		err    error
	}
	openCh := make(chan openResult, 1)
	go func() {
		stream, err := s.openStreamToClient(target, stored.Name)
		openCh <- openResult{stream: stream, err: err}
	}()

	clientStream, err := clientSession.AcceptStream()
	if err != nil {
		t.Fatalf("accept client stream: %v", err)
	}
	defer mustClose(t, clientStream)
	header, err := protocol.DecodeDataStreamHeader(clientStream)
	if err != nil {
		t.Fatalf("decode data stream header: %v", err)
	}
	if header.TunnelID != stored.ID || header.Revision != stored.Revision {
		t.Fatalf("data stream header should use stored identity, got %+v", header)
	}
	if header.SourceRole != protocol.DataStreamRoleServer || header.TargetRole != protocol.DataStreamRoleTarget || header.Transport != protocol.ActualTransportServerRelay {
		t.Fatalf("data stream route mismatch: %+v", header)
	}
	select {
	case result := <-openCh:
		if result.err != nil {
			t.Fatalf("open stream: %v", result.err)
		}
		mustClose(t, result.stream)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for open stream")
	}
}

func TestServerExposeIngressIssueCodeUsesEndpointTypeNotLegacyFlatType(t *testing.T) {
	s := New(0)
	stored := StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{
			ID:   "http-endpoint-flat-tcp-id",
			Name: "http-endpoint-flat-tcp",
			Type: protocol.ProxyTypeTCP,
		},
		Revision:     1,
		DesiredState: protocol.ProxyDesiredStateRunning,
		Ingress: EndpointSpec{
			Type: protocol.IngressTypeHTTPHost,
		},
	}

	s.recordServerExposeReconcileIssue(stored, errors.New("route registration failed"))

	issues := s.unifiedRuntime.issuesForStoredTunnel(stored, true)
	if len(issues) != 1 {
		t.Fatalf("expected one ingress issue, got %+v", issues)
	}
	if issues[0].Code != protocol.TunnelIssueCodeIngressRouteFailed {
		t.Fatalf("HTTP endpoint ingress issue must be classified by endpoint type, got %+v", issues[0])
	}
}

func TestServerExposeIngressIssueCodeKeepsLegacyHTTPTypeCompatibility(t *testing.T) {
	for _, tc := range []struct {
		name      string
		typeValue string
		message   string
		wantCode  string
	}{
		{
			name:      "unified http endpoint type",
			typeValue: protocol.IngressTypeHTTPHost,
			message:   "bind: address already in use",
			wantCode:  protocol.TunnelIssueCodeIngressRouteFailed,
		},
		{
			name:      "legacy http proxy type",
			typeValue: protocol.ProxyTypeHTTP,
			message:   "bind: address already in use",
			wantCode:  protocol.TunnelIssueCodeIngressRouteFailed,
		},
		{
			name:      "tcp endpoint port in use",
			typeValue: protocol.IngressTypeTCPListen,
			message:   "bind: address already in use",
			wantCode:  protocol.TunnelIssueCodeIngressPortInUse,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := serverExposeIngressIssueCode(tc.typeValue, tc.message)
			if got != tc.wantCode {
				t.Fatalf("serverExposeIngressIssueCode(%q) = %q, want %q", tc.typeValue, got, tc.wantCode)
			}
		})
	}
}

func TestLegacyFlatHTTPRecordReconcileIssueUsesNormalizedEndpointType(t *testing.T) {
	s := New(0)
	stored := StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{
			ID:        "legacy-http-id",
			Name:      "legacy-http",
			Type:      protocol.ProxyTypeHTTP,
			LocalIP:   "127.0.0.1",
			LocalPort: 8080,
			Domain:    "legacy.example.com",
		},
		ClientID:     "target-client",
		Revision:     1,
		DesiredState: protocol.ProxyDesiredStateRunning,
		RuntimeState: protocol.ProxyRuntimeStateOffline,
	}
	if err := stored.normalize(); err != nil {
		t.Fatalf("normalize legacy HTTP tunnel: %v", err)
	}

	s.recordServerExposeReconcileIssue(stored, errors.New("address already in use"))

	issues := s.unifiedRuntime.issuesForStoredTunnel(stored, true)
	if len(issues) != 1 {
		t.Fatalf("expected one ingress issue, got %+v", issues)
	}
	if issues[0].Code != protocol.TunnelIssueCodeIngressRouteFailed {
		t.Fatalf("normalized legacy HTTP ingress issue must stay route-scoped, got %+v", issues[0])
	}
}

func TestUnifiedServerExposeReconcilePreservesNewRuntimeAfterLateRevisionAdvance(t *testing.T) {
	s := New(0)
	s.store = newTestTunnelStore(t)

	reservedListener, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("reserve remote port: %v", err)
	}
	remotePort := reservedListener.Addr().(*net.TCPAddr).Port
	t.Cleanup(func() {
		if reservedListener != nil {
			_ = reservedListener.Close()
		}
	})

	stored := StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{
			ID:         "server-expose-stale-revision-id",
			Name:       "server-expose-stale-revision",
			Type:       protocol.ProxyTypeTCP,
			LocalIP:    "127.0.0.1",
			LocalPort:  65022,
			RemotePort: remotePort,
		},
		ClientID:        "target-client",
		OwnerClientID:   "target-client",
		Binding:         TunnelBindingClientID,
		Revision:        7,
		Topology:        TunnelTopologyServerExpose,
		DesiredState:    protocol.ProxyDesiredStateRunning,
		RuntimeState:    protocol.ProxyRuntimeStateOffline,
		TransportPolicy: protocol.TransportPolicyServerRelayOnly,
		ActualTransport: protocol.ActualTransportUnknown,
		P2P:             P2PState{State: TunnelP2PStateIdle},
		Ingress: EndpointSpec{
			Location: protocol.EndpointLocationServer,
			Type:     protocol.IngressTypeTCPListen,
			Config:   mustRawJSON(tcpListenConfigAPI{BindIP: "127.0.0.1", Port: remotePort}),
		},
		Target: EndpointSpec{
			Location: protocol.EndpointLocationClient,
			ClientID: "target-client",
			Type:     protocol.TargetTypeTCPService,
			Config:   mustRawJSON(serviceConfigAPI{IP: "127.0.0.1", Port: 65022}),
		},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := stored.normalize(); err != nil {
		t.Fatalf("normalize stored tunnel: %v", err)
	}
	mustAddStableTunnel(t, s.store, stored)

	targetWS, targetServerWS := newTestWebSocketPair(t)
	defer mustClose(t, targetWS)
	defer mustClose(t, targetServerWS)
	_, serverSession := newTestClientRelayDataSession(t)
	caps := protocol.DefaultClientCapabilities()
	target := &ClientConn{
		ID:          stored.Target.ClientID,
		Info:        protocol.ClientInfo{Hostname: "target-client", Capabilities: &caps},
		conn:        targetServerWS,
		proxies:     make(map[string]*ProxyTunnel),
		dataSession: serverSession,
		generation:  1,
		state:       clientStateLive,
	}
	s.clients.Store(target.ID, target)
	go s.controlLoop(target)
	t.Cleanup(func() {
		_ = s.CloseProxyRuntime(target, stored.Name)
	})

	eventsCh := s.events.Subscribe()
	defer s.events.Unsubscribe(eventsCh)
	next := stored
	next.Revision = stored.Revision + 1
	next.RuntimeState = protocol.ProxyRuntimeStateOffline
	next.Target.Config = mustRawJSON(serviceConfigAPI{IP: "127.0.0.1", Port: 65023})
	next.LocalPort = 65023
	next.UpdatedAt = time.Now().UTC()
	replacement := &ProxyTunnel{
		Config: storedTunnelToProxyConfig(next),
		limits: newDirectionalBandwidthRuntime(next.BandwidthSettings, realBandwidthClock{}),
		done:   make(chan struct{}),
	}
	initializeTunnelRuntimeFromState(replacement, target.ID, time.Now())
	hookErrCh := make(chan error, 1)
	var hookOnce sync.Once
	s.serverExposeActivatedHook = func(_ StoredTunnel, _ *ProxyTunnel) {
		hookOnce.Do(func() {
			if err := s.store.ReplaceTunnelByID(stored.OwnerClientID, stored.ID, stored.Revision, next); err != nil {
				hookErrCh <- fmt.Errorf("advance stored tunnel revision after old activation: %w", err)
				return
			}
			target.proxyMu.Lock()
			target.proxies[stored.Name] = replacement
			target.proxyMu.Unlock()
		})
	}

	restoreDone := make(chan error, 1)
	go func() {
		restoreDone <- s.reconcileServerExposeTunnel(stored)
	}()
	_ = waitForTunnelChangedEvent(t, eventsCh, "pending", stored.Name)

	msg := readControlMessageOfType(t, targetWS, protocol.MsgTypeTunnelProvision)
	var provision protocol.TunnelProvisionRequest
	if err := msg.ParsePayload(&provision); err != nil {
		t.Fatalf("parse provision payload: %v", err)
	}
	if provision.TunnelID != stored.ID || provision.Revision != stored.Revision || provision.Role != protocol.DataStreamRoleTarget {
		t.Fatalf("provision identity mismatch: %+v", provision)
	}

	if err := reservedListener.Close(); err != nil {
		t.Fatalf("release remote port: %v", err)
	}
	reservedListener = nil
	ack, err := protocol.NewMessage(protocol.MsgTypeTunnelProvisionAck, protocol.TunnelProvisionAck{
		TunnelID: provision.TunnelID,
		Revision: provision.Revision,
		Role:     provision.Role,
		Accepted: true,
		Message:  "stale ack",
	})
	if err != nil {
		t.Fatalf("build stale provision ack: %v", err)
	}
	if err := targetWS.WriteJSON(ack); err != nil {
		t.Fatalf("write stale provision ack: %v", err)
	}

	select {
	case err := <-restoreDone:
		if !errors.Is(err, errTunnelProvisionAckCancelled) {
			t.Fatalf("late old restore should be cancelled, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for stale restore to finish")
	}
	select {
	case err := <-hookErrCh:
		t.Fatal(err)
	default:
	}

	target.proxyMu.RLock()
	currentRuntime := target.proxies[stored.Name]
	target.proxyMu.RUnlock()
	if currentRuntime != replacement {
		t.Fatal("late old restore must not delete or replace the new revision runtime")
	}
	probe, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(remotePort)))
	if err != nil {
		t.Fatalf("late old restore must release its listener: %v", err)
	}
	_ = probe.Close()
	got, err := s.store.GetTunnelByIDE(stored.OwnerClientID, stored.ID)
	if err != nil {
		t.Fatalf("load stored tunnel after stale ack: %v", err)
	}
	if got.Revision != next.Revision {
		t.Fatalf("stale ack must not roll back stored revision: got %d want %d", got.Revision, next.Revision)
	}
	if got.RuntimeState != protocol.ProxyRuntimeStateOffline {
		t.Fatalf("late old restore must preserve new revision runtime state, got %s", got.RuntimeState)
	}
	spec := specFromStoredTunnel(got, s)
	if len(spec.Issues) != 0 {
		t.Fatalf("late old restore must not project an issue onto the new revision: %+v", spec.Issues)
	}
}

func TestUnifiedServerExposeRejectedProvisionLeavesNoListenerOrAckWaiter(t *testing.T) {
	s := New(0)
	s.store = newTestTunnelStore(t)

	reservedListener, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("reserve remote port: %v", err)
	}
	remotePort := reservedListener.Addr().(*net.TCPAddr).Port
	t.Cleanup(func() {
		if reservedListener != nil {
			_ = reservedListener.Close()
		}
	})

	stored := StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{
			ID:         "server-expose-rejected-id",
			Name:       "server-expose-rejected",
			Type:       protocol.ProxyTypeTCP,
			LocalIP:    "127.0.0.1",
			LocalPort:  65022,
			RemotePort: remotePort,
		},
		ClientID:        "target-client",
		OwnerClientID:   "target-client",
		Binding:         TunnelBindingClientID,
		Revision:        5,
		Topology:        TunnelTopologyServerExpose,
		DesiredState:    protocol.ProxyDesiredStateRunning,
		RuntimeState:    protocol.ProxyRuntimeStateOffline,
		TransportPolicy: protocol.TransportPolicyServerRelayOnly,
		ActualTransport: protocol.ActualTransportUnknown,
		P2P:             P2PState{State: TunnelP2PStateIdle},
		Ingress: EndpointSpec{
			Location: protocol.EndpointLocationServer,
			Type:     protocol.IngressTypeTCPListen,
			Config:   mustRawJSON(tcpListenConfigAPI{BindIP: "127.0.0.1", Port: remotePort}),
		},
		Target: EndpointSpec{
			Location: protocol.EndpointLocationClient,
			ClientID: "target-client",
			Type:     protocol.TargetTypeTCPService,
			Config:   mustRawJSON(serviceConfigAPI{IP: "127.0.0.1", Port: 65022}),
		},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := stored.normalize(); err != nil {
		t.Fatalf("normalize stored tunnel: %v", err)
	}
	mustAddStableTunnel(t, s.store, stored)

	targetWS, targetServerWS := newTestWebSocketPair(t)
	defer mustClose(t, targetWS)
	defer mustClose(t, targetServerWS)
	_, serverSession := newTestClientRelayDataSession(t)
	caps := protocol.DefaultClientCapabilities()
	target := &ClientConn{
		ID:          stored.Target.ClientID,
		Info:        protocol.ClientInfo{Hostname: "target-client", Capabilities: &caps},
		conn:        targetServerWS,
		proxies:     make(map[string]*ProxyTunnel),
		dataSession: serverSession,
		generation:  1,
		state:       clientStateLive,
	}
	s.clients.Store(target.ID, target)
	go s.controlLoop(target)
	t.Cleanup(func() {
		_ = s.CloseProxyRuntime(target, stored.Name)
	})

	restoreDone := make(chan error, 1)
	go func() {
		restoreDone <- s.reconcileServerExposeTunnel(stored)
	}()

	msg := readControlMessageOfType(t, targetWS, protocol.MsgTypeTunnelProvision)
	var provision protocol.TunnelProvisionRequest
	if err := msg.ParsePayload(&provision); err != nil {
		t.Fatalf("parse provision payload: %v", err)
	}
	if provision.TunnelID != stored.ID || provision.Revision != stored.Revision || provision.Role != protocol.DataStreamRoleTarget {
		t.Fatalf("provision identity mismatch: %+v", provision)
	}
	ack, err := protocol.NewMessage(protocol.MsgTypeTunnelProvisionAck, protocol.TunnelProvisionAck{
		TunnelID: provision.TunnelID,
		Revision: provision.Revision,
		Role:     provision.Role,
		Accepted: false,
		Message:  "target rejected fixed service",
	})
	if err != nil {
		t.Fatalf("build rejected provision ack: %v", err)
	}
	if err := targetWS.WriteJSON(ack); err != nil {
		t.Fatalf("write rejected provision ack: %v", err)
	}

	select {
	case err := <-restoreDone:
		var rejected *tunnelProvisionRejectedError
		if !errors.As(err, &rejected) {
			t.Fatalf("restore should return rejected provision error, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for rejected restore")
	}

	s.tunnels.pendingProvisionAckMu.Lock()
	pendingCount := len(s.tunnels.pendingProvisionAcks)
	s.tunnels.pendingProvisionAckMu.Unlock()
	if pendingCount != 0 {
		t.Fatalf("rejected server-expose provision must release ack waiters, got %d", pendingCount)
	}
	if name, tunnel, exists := findTunnelBySelector(target, stored.ID); exists {
		config, runtimeHeld, stillExists := serverExposeTunnelSnapshot(target, name, tunnel)
		if stillExists && (runtimeHeld || config.RuntimeState == protocol.ProxyRuntimeStateExposed) {
			t.Fatalf("rejected provision left active runtime: name=%s runtime_state=%s", name, config.RuntimeState)
		}
	}
	if err := reservedListener.Close(); err != nil {
		t.Fatalf("release reserved port before no-listener check: %v", err)
	}
	reservedListener = nil
	probe, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(remotePort))
	if err != nil {
		t.Fatalf("rejected provision must not leave tcp listener on port %d: %v", remotePort, err)
	}
	_ = probe.Close()

	got, err := s.store.GetTunnelByIDE(stored.OwnerClientID, stored.ID)
	if err != nil {
		t.Fatalf("load stored tunnel after rejected provision: %v", err)
	}
	if got.RuntimeState != protocol.ProxyRuntimeStateError {
		t.Fatalf("rejected provision should persist runtime error, got %s", got.RuntimeState)
	}
	spec := specFromStoredTunnel(got, s)
	if len(spec.Issues) != 1 || spec.Issues[0].Code != protocol.TunnelIssueCodeProvisionAckRejected || spec.Issues[0].ClientID != stored.Target.ClientID {
		t.Fatalf("rejected provision issue mismatch: %+v", spec.Issues)
	}

	retryDone := make(chan error, 1)
	go func() {
		current, err := s.store.GetTunnelByIDE(stored.OwnerClientID, stored.ID)
		if err != nil {
			retryDone <- err
			return
		}
		retryDone <- s.reconcileServerExposeTunnel(current)
	}()

	retryMsg := readControlMessageOfType(t, targetWS, protocol.MsgTypeTunnelProvision)
	var retryProvision protocol.TunnelProvisionRequest
	if err := retryMsg.ParsePayload(&retryProvision); err != nil {
		t.Fatalf("parse retry provision payload: %v", err)
	}
	if retryProvision.TunnelID != stored.ID || retryProvision.Revision != stored.Revision {
		t.Fatalf("retry provision identity mismatch: %+v", retryProvision)
	}
	retryAck, err := protocol.NewMessage(protocol.MsgTypeTunnelProvisionAck, protocol.TunnelProvisionAck{
		TunnelID: retryProvision.TunnelID,
		Revision: retryProvision.Revision,
		Role:     retryProvision.Role,
		Accepted: true,
	})
	if err != nil {
		t.Fatalf("build retry provision ack: %v", err)
	}
	if err := targetWS.WriteJSON(retryAck); err != nil {
		t.Fatalf("write retry provision ack: %v", err)
	}
	select {
	case err := <-retryDone:
		if err != nil {
			t.Fatalf("error placeholder should be replaceable on retry: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for rejected provision retry")
	}

	retried, err := s.store.GetTunnelByIDE(stored.OwnerClientID, stored.ID)
	if err != nil {
		t.Fatalf("load stored tunnel after retry: %v", err)
	}
	if retried.RuntimeState != protocol.ProxyRuntimeStateExposed {
		t.Fatalf("retry should expose tunnel, got %s", retried.RuntimeState)
	}
	if issues := specFromStoredTunnel(retried, s).Issues; len(issues) != 0 {
		t.Fatalf("successful retry should clear rejected provision issue: %+v", issues)
	}
}

func TestUnifiedServerExposeRuntimeErrorWinsActivationTransition(t *testing.T) {
	s := New(0)
	s.store = newTestTunnelStore(t)
	stored := testStoredServerExposeTCPTunnel(
		"activation-error-id",
		"activation-error",
		"activation-error-client",
		65022,
		reserveTCPPort(t),
		time.Now().UTC(),
	)
	stored.Revision = 6
	stored.RuntimeState = protocol.ProxyRuntimeStateOffline
	stored.ActualTransport = protocol.ActualTransportUnknown
	mustAddStableTunnel(t, s.store, stored)

	clientWS, serverWS := newTestWebSocketPair(t)
	defer mustClose(t, clientWS)
	defer mustClose(t, serverWS)
	_, serverSession := newTestClientRelayDataSession(t)
	caps := protocol.DefaultClientCapabilities()
	client := &ClientConn{
		ID:          stored.OwnerClientID,
		Info:        protocol.ClientInfo{Hostname: stored.OwnerClientID, Capabilities: &caps},
		conn:        serverWS,
		proxies:     make(map[string]*ProxyTunnel),
		dataSession: serverSession,
		generation:  1,
		state:       clientStateLive,
	}
	s.clients.Store(client.ID, client)
	go s.controlLoop(client)

	runtimeErrorDone := make(chan struct{})
	s.serverExposeActivatedHook = func(_ StoredTunnel, tunnel *ProxyTunnel) {
		client.proxyMu.RLock()
		listener := tunnel.Listener
		proxyName := tunnel.Config.Name
		client.proxyMu.RUnlock()
		go func() {
			s.markTCPProxyRuntimeErrorIfCurrent(client, proxyName, tunnel, listener, "injected activation failure")
			close(runtimeErrorDone)
		}()
	}

	reconcileDone := make(chan error, 1)
	go func() {
		reconcileDone <- s.reconcileServerExposeTunnel(stored)
	}()
	msg := readControlMessageOfType(t, clientWS, protocol.MsgTypeTunnelProvision)
	var provision protocol.TunnelProvisionRequest
	if err := msg.ParsePayload(&provision); err != nil {
		t.Fatalf("parse activation provision: %v", err)
	}
	ack, err := protocol.NewMessage(protocol.MsgTypeTunnelProvisionAck, protocol.TunnelProvisionAck{
		TunnelID: provision.TunnelID,
		Revision: provision.Revision,
		Role:     provision.Role,
		Accepted: true,
	})
	if err != nil {
		t.Fatalf("build activation ack: %v", err)
	}
	if err := clientWS.WriteJSON(ack); err != nil {
		t.Fatalf("write activation ack: %v", err)
	}

	select {
	case err := <-reconcileDone:
		if err != nil {
			t.Fatalf("activation should finish before serialized runtime error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for activation")
	}
	select {
	case <-runtimeErrorDone:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for serialized runtime error")
	}

	got, err := s.store.GetTunnelByIDE(stored.OwnerClientID, stored.ID)
	if err != nil {
		t.Fatalf("load tunnel after activation error: %v", err)
	}
	if got.RuntimeState != protocol.ProxyRuntimeStateError || got.Error != "injected activation failure" {
		t.Fatalf("runtime error must remain final: state=%s error=%q", got.RuntimeState, got.Error)
	}
	name, tunnel, exists := findTunnelBySelector(client, stored.ID)
	if !exists {
		t.Fatal("runtime error should retain an exact retry placeholder")
	}
	config, runtimeHeld, stillExists := serverExposeTunnelSnapshot(client, name, tunnel)
	if !stillExists || runtimeHeld || config.RuntimeState != protocol.ProxyRuntimeStateError {
		t.Fatalf("runtime error placeholder mismatch: config=%+v held=%v exists=%v", config, runtimeHeld, stillExists)
	}
	issues := specFromStoredTunnel(got, s).Issues
	if len(issues) != 1 || issues[0].Message != "injected activation failure" {
		t.Fatalf("activation runtime issue should remain visible: %+v", issues)
	}
}

func TestUnifiedServerExposeRetryWaitsForRuntimeErrorCleanup(t *testing.T) {
	s := New(0)
	s.store = newTestTunnelStore(t)
	listener := newScriptedListener(t)
	stored := testStoredServerExposeTCPTunnel(
		"runtime-cleanup-order-id",
		"runtime-cleanup-order",
		"runtime-cleanup-client",
		65022,
		listener.addr.(*net.TCPAddr).Port,
		time.Now().UTC(),
	)
	stored.Revision = 7
	mustAddStableTunnel(t, s.store, stored)

	clientWS, serverWS := newTestWebSocketPair(t)
	defer mustClose(t, clientWS)
	defer mustClose(t, serverWS)
	_, serverSession := newTestClientRelayDataSession(t)
	caps := protocol.DefaultClientCapabilities()
	client := &ClientConn{
		ID:          stored.OwnerClientID,
		Info:        protocol.ClientInfo{Hostname: stored.OwnerClientID, Capabilities: &caps},
		conn:        serverWS,
		proxies:     make(map[string]*ProxyTunnel),
		dataSession: serverSession,
		generation:  1,
		state:       clientStateLive,
	}
	s.clients.Store(client.ID, client)
	go s.controlLoop(client)
	tunnel := &ProxyTunnel{
		Config:   storedTunnelToProxyConfig(stored),
		Listener: listener,
		limits:   newDirectionalBandwidthRuntime(stored.BandwidthSettings, realBandwidthClock{}),
		done:     make(chan struct{}),
	}
	tunnel.runtime.Revision = uint64(stored.Revision)
	initializeTunnelRuntimeFromState(tunnel, client.ID, time.Now())
	client.proxies[stored.Name] = tunnel
	t.Cleanup(func() {
		if name, _, exists := findTunnelBySelector(client, stored.ID); exists {
			_ = s.CloseProxyRuntime(client, name)
		}
	})

	cleanupEntered := make(chan struct{})
	releaseCleanup := make(chan struct{})
	s.runtimeErrorCleanupHook = func(config protocol.ProxyConfig) {
		if config.ID != stored.ID {
			return
		}
		close(cleanupEntered)
		<-releaseCleanup
	}
	markDone := make(chan struct{})
	go func() {
		s.markTCPProxyRuntimeErrorIfCurrent(client, stored.Name, tunnel, listener, "ordered runtime failure")
		close(markDone)
	}()
	select {
	case <-cleanupEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for runtime cleanup barrier")
	}

	current, err := s.store.GetTunnelByIDE(stored.OwnerClientID, stored.ID)
	if err != nil {
		t.Fatalf("load error state before retry: %v", err)
	}
	reconcileDone := make(chan error, 1)
	go func() {
		reconcileDone <- s.reconcileServerExposeTunnel(current)
	}()
	deadline := time.Now().Add(2 * time.Second)
	for {
		s.tunnelRuntimeOps.mu.Lock()
		entry := s.tunnelRuntimeOps.entries[tunnelRuntimeOperationKey(stored.ID, stored.OwnerClientID, stored.Name)]
		refs := 0
		if entry != nil {
			refs = entry.refs
		}
		s.tunnelRuntimeOps.mu.Unlock()
		if refs >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("retry did not wait on runtime cleanup operation")
		}
		time.Sleep(time.Millisecond)
	}
	client.proxyMu.RLock()
	stillOldRuntime := client.proxies[stored.Name] == tunnel
	client.proxyMu.RUnlock()
	if !stillOldRuntime {
		t.Fatal("retry replaced runtime before old revision cleanup completed")
	}

	close(releaseCleanup)
	select {
	case <-markDone:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for runtime cleanup")
	}
	unprovisionMsg := readControlMessageOfType(t, clientWS, protocol.MsgTypeTunnelUnprovision)
	var unprovision protocol.TunnelUnprovisionRequest
	if err := unprovisionMsg.ParsePayload(&unprovision); err != nil {
		t.Fatalf("parse ordered unprovision: %v", err)
	}
	if unprovision.TunnelID != stored.ID || unprovision.Revision != stored.Revision {
		t.Fatalf("ordered unprovision identity mismatch: %+v", unprovision)
	}

	provisionMsg := readControlMessageOfType(t, clientWS, protocol.MsgTypeTunnelProvision)
	var provision protocol.TunnelProvisionRequest
	if err := provisionMsg.ParsePayload(&provision); err != nil {
		t.Fatalf("parse ordered retry provision: %v", err)
	}
	ack, err := protocol.NewMessage(protocol.MsgTypeTunnelProvisionAck, protocol.TunnelProvisionAck{
		TunnelID: provision.TunnelID,
		Revision: provision.Revision,
		Role:     provision.Role,
		Accepted: true,
	})
	if err != nil {
		t.Fatalf("build ordered retry ack: %v", err)
	}
	if err := clientWS.WriteJSON(ack); err != nil {
		t.Fatalf("write ordered retry ack: %v", err)
	}
	select {
	case err := <-reconcileDone:
		if err != nil {
			t.Fatalf("retry after ordered cleanup: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for retry after runtime cleanup")
	}
}

func TestUnifiedServerExposeInFlightReconcileShutdownCleansRuntimeAndAckWaiter(t *testing.T) {
	s := New(0)
	s.store = newTestTunnelStore(t)

	reservedListener, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("reserve remote port: %v", err)
	}
	remotePort := reservedListener.Addr().(*net.TCPAddr).Port
	t.Cleanup(func() {
		if reservedListener != nil {
			_ = reservedListener.Close()
		}
	})

	stored := StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{
			ID:         "server-expose-shutdown-id",
			Name:       "server-expose-shutdown",
			Type:       protocol.ProxyTypeTCP,
			LocalIP:    "127.0.0.1",
			LocalPort:  65022,
			RemotePort: remotePort,
		},
		ClientID:        "target-client",
		OwnerClientID:   "target-client",
		Binding:         TunnelBindingClientID,
		Revision:        3,
		Topology:        TunnelTopologyServerExpose,
		DesiredState:    protocol.ProxyDesiredStateRunning,
		RuntimeState:    protocol.ProxyRuntimeStateOffline,
		TransportPolicy: protocol.TransportPolicyServerRelayOnly,
		ActualTransport: protocol.ActualTransportUnknown,
		P2P:             P2PState{State: TunnelP2PStateIdle},
		Ingress: EndpointSpec{
			Location: protocol.EndpointLocationServer,
			Type:     protocol.IngressTypeTCPListen,
			Config:   mustRawJSON(tcpListenConfigAPI{BindIP: "127.0.0.1", Port: remotePort}),
		},
		Target: EndpointSpec{
			Location: protocol.EndpointLocationClient,
			ClientID: "target-client",
			Type:     protocol.TargetTypeTCPService,
			Config:   mustRawJSON(serviceConfigAPI{IP: "127.0.0.1", Port: 65022}),
		},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := stored.normalize(); err != nil {
		t.Fatalf("normalize stored tunnel: %v", err)
	}
	mustAddStableTunnel(t, s.store, stored)

	targetWS, targetServerWS := newTestWebSocketPair(t)
	defer mustClose(t, targetWS)
	defer mustClose(t, targetServerWS)
	_, serverSession := newTestClientRelayDataSession(t)
	caps := protocol.DefaultClientCapabilities()
	target := &ClientConn{
		ID:          stored.Target.ClientID,
		Info:        protocol.ClientInfo{Hostname: "target-client", Capabilities: &caps},
		conn:        targetServerWS,
		proxies:     make(map[string]*ProxyTunnel),
		dataSession: serverSession,
		generation:  1,
		state:       clientStateLive,
	}
	s.clients.Store(target.ID, target)
	go s.controlLoop(target)
	t.Cleanup(func() {
		_ = s.CloseProxyRuntime(target, stored.Name)
	})

	restoreDone := make(chan error, 1)
	go func() {
		restoreDone <- s.reconcileUnifiedTunnel(stored.ID, "test_shutdown")
	}()

	msg := readControlMessageOfType(t, targetWS, protocol.MsgTypeTunnelProvision)
	var provision protocol.TunnelProvisionRequest
	if err := msg.ParsePayload(&provision); err != nil {
		t.Fatalf("parse provision payload: %v", err)
	}
	if provision.TunnelID != stored.ID || provision.Revision != stored.Revision || provision.Role != protocol.DataStreamRoleTarget {
		t.Fatalf("provision identity mismatch: %+v", provision)
	}

	s.tunnels.pendingProvisionAckMu.Lock()
	pendingBeforeShutdown := len(s.tunnels.pendingProvisionAcks)
	s.tunnels.pendingProvisionAckMu.Unlock()
	if pendingBeforeShutdown != 1 {
		t.Fatalf("expected one pending provision ack waiter before shutdown, got %d", pendingBeforeShutdown)
	}

	s.closeDone()

	select {
	case err := <-restoreDone:
		if !errors.Is(err, errTunnelProvisionAckCancelled) {
			t.Fatalf("shutdown should cancel in-flight reconcile provision wait, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for in-flight reconcile to exit after shutdown")
	}

	s.tunnels.pendingProvisionAckMu.Lock()
	pendingAfterShutdown := len(s.tunnels.pendingProvisionAcks)
	s.tunnels.pendingProvisionAckMu.Unlock()
	if pendingAfterShutdown != 0 {
		t.Fatalf("shutdown-cancelled reconcile must release ack waiters, got %d", pendingAfterShutdown)
	}
	s.unifiedReconcile.mu.Lock()
	registryEntries := len(s.unifiedReconcile.entries)
	s.unifiedReconcile.mu.Unlock()
	if registryEntries != 0 {
		t.Fatalf("shutdown-cancelled reconcile must release registry entry, got %d", registryEntries)
	}
	if name, tunnel, exists := findTunnelBySelector(target, stored.ID); exists {
		config, runtimeHeld, stillExists := serverExposeTunnelSnapshot(target, name, tunnel)
		if stillExists && (runtimeHeld || config.RuntimeState == protocol.ProxyRuntimeStateExposed || config.RuntimeState == protocol.ProxyRuntimeStatePending) {
			t.Fatalf("shutdown-cancelled reconcile left runtime: name=%s runtime_state=%s", name, config.RuntimeState)
		}
	}
}

func TestUnifiedServerExposeCapabilityLossCleansListenerAndProjectsIssue(t *testing.T) {
	s := New(0)
	s.store = newTestTunnelStore(t)

	reservedListener, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("reserve remote port: %v", err)
	}
	remotePort := reservedListener.Addr().(*net.TCPAddr).Port
	if err := reservedListener.Close(); err != nil {
		t.Fatalf("release reserved remote port: %v", err)
	}

	stored := StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{
			ID:         "server-expose-capability-loss-id",
			Name:       "server-expose-capability-loss",
			Type:       protocol.ProxyTypeTCP,
			LocalIP:    "127.0.0.1",
			LocalPort:  65022,
			RemotePort: remotePort,
		},
		ClientID:        "target-client",
		OwnerClientID:   "target-client",
		Binding:         TunnelBindingClientID,
		Revision:        4,
		Topology:        TunnelTopologyServerExpose,
		DesiredState:    protocol.ProxyDesiredStateRunning,
		RuntimeState:    protocol.ProxyRuntimeStateExposed,
		TransportPolicy: protocol.TransportPolicyServerRelayOnly,
		ActualTransport: protocol.ActualTransportServerRelay,
		P2P:             P2PState{State: TunnelP2PStateIdle},
		Ingress: EndpointSpec{
			Location: protocol.EndpointLocationServer,
			Type:     protocol.IngressTypeTCPListen,
			Config:   mustRawJSON(tcpListenConfigAPI{BindIP: "127.0.0.1", Port: remotePort}),
		},
		Target: EndpointSpec{
			Location: protocol.EndpointLocationClient,
			ClientID: "target-client",
			Type:     protocol.TargetTypeTCPService,
			Config:   mustRawJSON(serviceConfigAPI{IP: "127.0.0.1", Port: 65022}),
		},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := stored.normalize(); err != nil {
		t.Fatalf("normalize stored tunnel: %v", err)
	}
	mustAddStableTunnel(t, s.store, stored)

	caps := protocol.DefaultClientCapabilities()
	_, serverSession := newTestClientRelayDataSession(t)
	target := &ClientConn{
		ID:          stored.Target.ClientID,
		Info:        protocol.ClientInfo{Hostname: "target-client", Capabilities: &caps},
		proxies:     make(map[string]*ProxyTunnel),
		dataSession: serverSession,
		generation:  1,
		state:       clientStateLive,
	}
	s.clients.Store(target.ID, target)
	t.Cleanup(func() {
		_ = s.CloseProxyRuntime(target, stored.Name)
	})

	runtimeConfig, err := serverExposeRuntimeProxyRequest(stored)
	if err != nil {
		t.Fatalf("server expose runtime config: %v", err)
	}
	tunnel, err := s.prepareProxyTunnelWithExclusions(
		target,
		runtimeConfig,
		protocol.ProxyDesiredStateRunning,
		protocol.ProxyRuntimeStatePending,
		stored.Name,
		target.ID,
		stored.CreatedAt,
	)
	if err != nil {
		t.Fatalf("prepare runtime: %v", err)
	}
	_ = s.applyStoredServerExposeConfig(target, tunnel, stored, protocol.ProxyRuntimeStatePending, "")
	if err := s.activatePreparedTunnel(target, tunnel); err != nil {
		t.Fatalf("activate server-expose runtime: %v", err)
	}
	_ = s.applyStoredServerExposeConfig(target, tunnel, stored, protocol.ProxyRuntimeStateExposed, "")
	if _, runtimeHeld, exists := serverExposeTunnelSnapshot(target, stored.Name, tunnel); !exists || !runtimeHeld {
		t.Fatal("test setup should start with an active server-expose runtime")
	}

	noCaps := protocol.ClientCapabilities{}
	target.SetInfo(protocol.ClientInfo{Hostname: "target-client", Capabilities: &noCaps})

	if err := s.reconcileServerExposeTunnel(stored); err != nil {
		t.Fatalf("capability loss reconcile should project error without provisioning failure: %v", err)
	}
	if name, tunnel, exists := findTunnelBySelector(target, stored.ID); exists {
		config, runtimeHeld, stillExists := serverExposeTunnelSnapshot(target, name, tunnel)
		if stillExists && runtimeHeld {
			t.Fatalf("capability loss must close server listener/runtime: name=%s runtime_state=%s", name, config.RuntimeState)
		}
	}
	probe, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(remotePort))
	if err != nil {
		t.Fatalf("capability loss must release tcp listener on port %d: %v", remotePort, err)
	}
	_ = probe.Close()
	s.tunnels.pendingProvisionAckMu.Lock()
	pendingCount := len(s.tunnels.pendingProvisionAcks)
	s.tunnels.pendingProvisionAckMu.Unlock()
	if pendingCount != 0 {
		t.Fatalf("capability loss must not leave ack waiters, got %d", pendingCount)
	}
	reloaded, err := s.store.GetTunnelByIDE(stored.OwnerClientID, stored.ID)
	if err != nil {
		t.Fatalf("reload tunnel: %v", err)
	}
	spec := specFromStoredTunnel(reloaded, s)
	if spec.RuntimeState != protocol.ProxyRuntimeStateError {
		t.Fatalf("capability loss should project runtime error, got %q", spec.RuntimeState)
	}
	if len(spec.Issues) != 1 || spec.Issues[0].Code != protocol.TunnelIssueCodeCapabilityNotSupported || spec.Issues[0].ClientID != stored.Target.ClientID {
		t.Fatalf("capability issue mismatch: %+v", spec.Issues)
	}
}

func TestUnifiedServerExposeHTTPCapabilityRecoveryReactivatesClosedRuntime(t *testing.T) {
	s := New(0)
	s.store = newTestTunnelStore(t)
	stored := StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{
			ID:        "http-capability-recovery-id",
			Name:      "http-capability-recovery",
			Type:      protocol.ProxyTypeHTTP,
			LocalIP:   "127.0.0.1",
			LocalPort: 65022,
			Domain:    "http-capability-recovery.example.com",
		},
		ClientID:        "http-capability-client",
		OwnerClientID:   "http-capability-client",
		Binding:         TunnelBindingClientID,
		Revision:        5,
		Topology:        TunnelTopologyServerExpose,
		DesiredState:    protocol.ProxyDesiredStateRunning,
		RuntimeState:    protocol.ProxyRuntimeStateExposed,
		TransportPolicy: protocol.TransportPolicyServerRelayOnly,
		ActualTransport: protocol.ActualTransportServerRelay,
		P2P:             P2PState{State: TunnelP2PStateIdle},
		Ingress: EndpointSpec{
			Location: protocol.EndpointLocationServer,
			Type:     protocol.IngressTypeHTTPHost,
			Config: mustRawJSON(httpHostConfigAPI{
				Domain:             "http-capability-recovery.example.com",
				AllowedSourceCIDRs: allowAllSourceCIDRs(),
			}),
		},
		Target: EndpointSpec{
			Location: protocol.EndpointLocationClient,
			ClientID: "http-capability-client",
			Type:     protocol.TargetTypeTCPService,
			Config:   mustRawJSON(serviceConfigAPI{IP: "127.0.0.1", Port: 65022}),
		},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := stored.normalize(); err != nil {
		t.Fatalf("normalize HTTP capability tunnel: %v", err)
	}
	mustAddStableTunnel(t, s.store, stored)

	clientWS, serverWS := newTestWebSocketPair(t)
	defer mustClose(t, clientWS)
	defer mustClose(t, serverWS)
	clientSession, serverSession := newTestClientRelayDataSession(t)
	caps := protocol.DefaultClientCapabilities()
	target := &ClientConn{
		ID:          stored.OwnerClientID,
		Info:        protocol.ClientInfo{Hostname: stored.OwnerClientID, Capabilities: &caps},
		conn:        serverWS,
		proxies:     make(map[string]*ProxyTunnel),
		dataSession: serverSession,
		generation:  1,
		state:       clientStateLive,
	}
	s.clients.Store(target.ID, target)
	go s.controlLoop(target)
	tunnel := &ProxyTunnel{
		Config: storedTunnelToProxyConfig(stored),
		limits: newDirectionalBandwidthRuntime(stored.BandwidthSettings, realBandwidthClock{}),
		done:   make(chan struct{}),
	}
	tunnel.runtime.Revision = uint64(stored.Revision)
	initializeTunnelRuntimeFromState(tunnel, target.ID, time.Now())
	target.proxies[stored.Name] = tunnel
	t.Cleanup(func() {
		if name, _, exists := findTunnelBySelector(target, stored.ID); exists {
			_ = s.CloseProxyRuntime(target, name)
		}
	})

	noCaps := protocol.ClientCapabilities{}
	target.SetInfo(protocol.ClientInfo{Hostname: stored.OwnerClientID, Capabilities: &noCaps})
	if err := s.reconcileServerExposeTunnel(stored); err != nil {
		t.Fatalf("HTTP capability loss reconcile: %v", err)
	}
	if _, held, exists := serverExposeTunnelSnapshot(target, stored.Name, tunnel); !exists || held {
		t.Fatalf("closed HTTP activation should not be held: exists=%v held=%v", exists, held)
	}

	target.SetInfo(protocol.ClientInfo{Hostname: stored.OwnerClientID, Capabilities: &caps})
	current, err := s.store.GetTunnelByIDE(stored.OwnerClientID, stored.ID)
	if err != nil {
		t.Fatalf("load HTTP capability error state: %v", err)
	}
	reconcileDone := make(chan error, 1)
	go func() {
		reconcileDone <- s.reconcileServerExposeTunnel(current)
	}()
	provisionMsg := readControlMessageOfType(t, clientWS, protocol.MsgTypeTunnelProvision)
	var provision protocol.TunnelProvisionRequest
	if err := provisionMsg.ParsePayload(&provision); err != nil {
		t.Fatalf("parse HTTP recovery provision: %v", err)
	}
	ack, err := protocol.NewMessage(protocol.MsgTypeTunnelProvisionAck, protocol.TunnelProvisionAck{
		TunnelID: provision.TunnelID,
		Revision: provision.Revision,
		Role:     provision.Role,
		Accepted: true,
	})
	if err != nil {
		t.Fatalf("build HTTP recovery ack: %v", err)
	}
	if err := clientWS.WriteJSON(ack); err != nil {
		t.Fatalf("write HTTP recovery ack: %v", err)
	}
	select {
	case err := <-reconcileDone:
		if err != nil {
			t.Fatalf("HTTP capability recovery reconcile: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for HTTP capability recovery")
	}

	name, activeTunnel, exists := findTunnelBySelector(target, stored.ID)
	if !exists {
		t.Fatal("HTTP capability recovery did not create a runtime")
	}
	target.proxyMu.Lock()
	activation := proxyActivationSnapshotLocked(activeTunnel)
	target.proxyMu.Unlock()
	if _, held, stillExists := serverExposeTunnelSnapshot(target, name, activeTunnel); !stillExists || !held {
		t.Fatalf("HTTP capability recovery runtime mismatch: exists=%v held=%v", stillExists, held)
	}

	type openResult struct {
		stream net.Conn
		err    error
	}
	openDone := make(chan openResult, 1)
	go func() {
		stream, err := s.openStreamToClientForActivation(target, activeTunnel, activation)
		openDone <- openResult{stream: stream, err: err}
	}()
	clientStream, err := clientSession.Accept()
	if err != nil {
		t.Fatalf("accept HTTP recovery stream: %v", err)
	}
	defer mustClose(t, clientStream)
	header, err := protocol.DecodeDataStreamHeader(clientStream)
	if err != nil {
		t.Fatalf("decode HTTP recovery stream header: %v", err)
	}
	if header.TunnelID != stored.ID || header.Revision != stored.Revision {
		t.Fatalf("HTTP recovery stream identity mismatch: %+v", header)
	}
	select {
	case result := <-openDone:
		if result.err != nil || result.stream == nil {
			t.Fatalf("open HTTP recovery stream: stream=%v err=%v", result.stream, result.err)
		}
		_ = result.stream.Close()
	case <-time.After(2 * time.Second):
		t.Fatal("timed out opening HTTP recovery stream")
	}
}

func TestUnifiedServerExposeSOCKS5DataHeaderCarriesDynamicTarget(t *testing.T) {
	s := New(0)
	s.store = newTestTunnelStore(t)

	stored := StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{
			ID:   "server-expose-socks5-id",
			Name: "server-expose-socks5",
			Type: protocol.ProxyTypeTCP,
		},
		ClientID:        "target-client",
		OwnerClientID:   "target-client",
		Binding:         TunnelBindingClientID,
		Revision:        11,
		Topology:        TunnelTopologyServerExpose,
		DesiredState:    protocol.ProxyDesiredStateRunning,
		RuntimeState:    protocol.ProxyRuntimeStateOffline,
		TransportPolicy: protocol.TransportPolicyServerRelayOnly,
		ActualTransport: protocol.ActualTransportUnknown,
		P2P:             P2PState{State: TunnelP2PStateIdle},
		Ingress: EndpointSpec{
			Location: protocol.EndpointLocationServer,
			Type:     protocol.IngressTypeSOCKS5Listen,
			Config: mustRawJSON(protocol.SOCKS5ListenConfig{
				BindIP:             "127.0.0.1",
				Port:               reserveTCPPort(t),
				AllowedSourceCIDRs: []string{"127.0.0.0/8"},
				Auth:               protocol.SOCKS5AuthConfig{Type: protocol.SOCKS5AuthTypeNone},
			}),
		},
		Target: EndpointSpec{
			Location: protocol.EndpointLocationClient,
			ClientID: "target-client",
			Type:     protocol.TargetTypeSOCKS5ConnectHandler,
			Config: mustRawJSON(protocol.SOCKS5ConnectHandlerConfig{
				AllowedTargetCIDRs: []string{"0.0.0.0/0", "::/0"},
				DialTimeoutSeconds: 5,
			}),
		},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := stored.normalize(); err != nil {
		t.Fatalf("normalize stored tunnel: %v", err)
	}

	clientSession, serverSession := newTestClientRelayDataSession(t)
	target := &ClientConn{
		ID:          stored.Target.ClientID,
		proxies:     make(map[string]*ProxyTunnel),
		dataSession: serverSession,
		generation:  1,
		state:       clientStateLive,
	}
	s.clients.Store(target.ID, target)
	tunnel := &ProxyTunnel{
		Config: storedTunnelToProxyConfig(stored),
		limits: newDirectionalBandwidthRuntime(stored.BandwidthSettings, realBandwidthClock{}),
		done:   make(chan struct{}),
	}
	setProxyConfigStates(&tunnel.Config, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateExposed, "")
	tunnel.runtime.Revision = uint64(stored.Revision)
	initializeTunnelRuntimeFromState(tunnel, target.ID, time.Now())
	target.proxies[stored.Name] = tunnel
	activation := proxyActivationSnapshotLocked(tunnel)

	request := socks5wire.ConnectRequest{
		Host:         "example.com",
		Port:         443,
		AddrType:     protocol.SOCKS5AddrTypeDomain,
		OriginalHost: "example.com",
	}
	type openResult struct {
		stream net.Conn
		err    error
	}
	openCh := make(chan openResult, 1)
	go func() {
		stream, err := s.openSOCKS5StreamToClient(target, tunnel, activation, request)
		openCh <- openResult{stream: stream, err: err}
	}()

	clientStream, err := clientSession.AcceptStream()
	if err != nil {
		t.Fatalf("accept client stream: %v", err)
	}
	defer mustClose(t, clientStream)
	header, err := protocol.DecodeDataStreamHeader(clientStream)
	if err != nil {
		t.Fatalf("decode data stream header: %v", err)
	}
	if header.TunnelID != stored.ID || header.Revision != stored.Revision {
		t.Fatalf("data stream header should use stored identity, got %+v", header)
	}
	if header.TargetHost != request.Host || header.TargetPort != request.Port || header.TargetAddrType != request.AddrType || header.OriginalHost != request.OriginalHost {
		t.Fatalf("SOCKS5 dynamic target mismatch: got %+v request=%+v", header, request)
	}
	select {
	case result := <-openCh:
		if result.err != nil {
			t.Fatalf("open stream: %v", result.err)
		}
		mustClose(t, result.stream)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for open stream")
	}
}
