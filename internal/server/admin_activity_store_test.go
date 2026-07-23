package server

import (
	"errors"
	"testing"

	"netsgo/pkg/protocol"
)

func TestAdminActivityAPIKeyCreationIsAtomic(t *testing.T) {
	store := newInitializedAdminStore(t)
	actor := ActivityActor{Type: "admin", ID: "user", Name: "admin"}
	store.activityStore.failNextAppendsForTest(errors.New("injected activity failure"), 1)
	if _, _, err := store.AddAPIKeyWithActivity("failed-key", "sk-failed-key", []string{"connect"}, nil, 9, actor); err == nil {
		t.Fatal("AddAPIKeyWithActivity should fail")
	}
	var keys, events int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM api_keys WHERE name = 'failed-key'`).Scan(&keys); err != nil {
		t.Fatalf("count API keys: %v", err)
	}
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM activity_events`).Scan(&events); err != nil {
		t.Fatalf("count activity events: %v", err)
	}
	if keys != 0 || events != 0 {
		t.Fatalf("atomic rollback counts: keys=%d events=%d", keys, events)
	}

	key, activityID, err := store.AddAPIKeyWithActivity("created-key", "sk-created-key", []string{"connect"}, nil, 9, actor)
	if err != nil {
		t.Fatalf("AddAPIKeyWithActivity() error = %v", err)
	}
	if activityID <= 0 || key.MaxUses != 9 {
		t.Fatalf("created key/activity = %+v/%d", key, activityID)
	}
	var persistedMaxUses int
	if err := store.db.QueryRow(`SELECT max_uses FROM api_keys WHERE id = ?`, key.ID).Scan(&persistedMaxUses); err != nil {
		t.Fatalf("load max_uses: %v", err)
	}
	if persistedMaxUses != 9 {
		t.Fatalf("persisted max_uses = %d, want 9", persistedMaxUses)
	}
}

func TestAdminActivityDisplayNameNoopAndRollback(t *testing.T) {
	store := newInitializedAdminStore(t)
	client, err := store.GetOrCreateClient("install-activity", protocolClientInfoForActivity(), "192.0.2.1:1234")
	if err != nil {
		t.Fatalf("GetOrCreateClient() error = %v", err)
	}
	actor := ActivityActor{Type: "admin", ID: "user", Name: "admin"}
	activityID, err := store.UpdateClientDisplayNameWithActivity(client.ID, "display", actor)
	if err != nil || activityID <= 0 {
		t.Fatalf("UpdateClientDisplayNameWithActivity() = %d, %v", activityID, err)
	}
	noOpID, err := store.UpdateClientDisplayNameWithActivity(client.ID, "display", actor)
	if err != nil || noOpID != 0 {
		t.Fatalf("no-op display update = %d, %v", noOpID, err)
	}
	store.activityStore.failNextAppendsForTest(errors.New("injected activity failure"), 1)
	if _, err := store.UpdateClientDisplayNameWithActivity(client.ID, "rolled-back", actor); err == nil {
		t.Fatal("display update should fail with activity injection")
	}
	reloaded, ok := store.GetRegisteredClient(client.ID)
	if !ok || reloaded.DisplayName != "display" {
		t.Fatalf("client after rollback = %+v, ok=%v", reloaded, ok)
	}
}
func TestClientRegistrationActivityFirstInsertOnlyAndAtomic(t *testing.T) {
	store := newInitializedAdminStore(t)
	if _, err := store.AddAPIKey("registration-key", "sk-registration-key", []string{"connect"}, nil); err != nil {
		t.Fatalf("AddAPIKey() error = %v", err)
	}
	store.activityStore.failNextAppendsForTest(errors.New("injected activity failure"), 1)
	if _, err := store.RegisterClientAndExchangeToken("sk-registration-key", "install-registration", protocolClientInfoForActivity(), "192.0.2.10:4321"); err == nil {
		t.Fatal("registration should fail when activity append fails")
	}
	var clients, tokens, events int
	for query, destination := range map[string]*int{
		`SELECT COUNT(*) FROM registered_clients WHERE install_id = 'install-registration'`: &clients,
		`SELECT COUNT(*) FROM client_tokens WHERE install_id = 'install-registration'`:      &tokens,
		`SELECT COUNT(*) FROM activity_events`:                                              &events,
	} {
		if err := store.db.QueryRow(query).Scan(destination); err != nil {
			t.Fatalf("count registration rows: %v", err)
		}
	}
	if clients != 0 || tokens != 0 || events != 0 {
		t.Fatalf("registration rollback counts: clients=%d tokens=%d events=%d", clients, tokens, events)
	}

	first, err := store.RegisterClientAndExchangeToken("sk-registration-key", "install-registration", protocolClientInfoForActivity(), "192.0.2.10:4321")
	if err != nil || first.ActivityID <= 0 {
		t.Fatalf("first registration = %+v, %v", first, err)
	}
	second, err := store.RegisterClientAndExchangeToken("sk-registration-key", "install-registration", protocolClientInfoForActivity(), "192.0.2.10:4321")
	if err != nil || second.ActivityID != 0 || second.Client.ID != first.Client.ID {
		t.Fatalf("repeat exchange = %+v, %v", second, err)
	}
	page, err := store.activityStore.Query(ActivityQuery{Scope: ActivityScopeClient, ScopeID: first.Client.ID, Limit: 50})
	if err != nil || len(page.Items) != 1 || page.Items[0].Action != "registered" {
		t.Fatalf("registration activity = %+v, %v", page.Items, err)
	}
}

func protocolClientInfoForActivity() protocol.ClientInfo {
	return protocol.ClientInfo{Hostname: "activity-client"}
}
