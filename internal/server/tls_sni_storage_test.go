package server

import (
	"path/filepath"
	"reflect"
	"testing"

	"golang.org/x/crypto/bcrypt"
	"netsgo/internal/storage"
	"netsgo/pkg/protocol"
)

func TestTLSSNIStorageMigrationPreservesOwnersAndClaims(t *testing.T) {
	path := filepath.Join(t.TempDir(), "netsgo.db")
	migrations, err := serverMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if migrations[len(migrations)-1].Name != "017_tls_sni_ingress" {
		t.Fatal("update pre-SNI migration fixture")
	}
	db, err := storage.OpenConfigured(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.ApplyMigrationPlan(db, serverMigrationPlan(migrations[:len(migrations)-1])); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	admin, err := newAdminStoreWithDB(path, db, false)
	if err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	admin.bcryptCost = bcrypt.MinCost
	if err := admin.Initialize("admin", "Password123", "https://panel.example.test", nil); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	owner, err := admin.GetSingleAdminUser()
	if err != nil {
		t.Fatal(err)
	}
	other, err := admin.CreateUser("other", "Password123")
	if err != nil {
		t.Fatal(err)
	}
	store, err := newTunnelStoreWithDB(path, db, false)
	if err != nil {
		t.Fatal(err)
	}
	s := New(0)
	s.auth.adminStore, s.store = admin, store
	for i, user := range []User{owner, other} {
		client := registerWebhookClient(t, admin, user.ID, "migration-"+user.ID, "migration-client")
		req := httpDomainAPIRequest("preserved", client.ID, []string{"app.example.test", "*.other.test"}[i])
		stored, err := s.storedTunnelFromUnifiedRequestForUser(user.ID, req, "")
		if err != nil {
			t.Fatal(err)
		}
		if err := store.AddTunnel(stored); err != nil {
			t.Fatal(err)
		}
	}
	before, err := store.GetAllTunnels()
	if err != nil {
		t.Fatal(err)
	}
	claims, err := queryStringColumn(db, `SELECT resource_key FROM tunnel_resource_locks ORDER BY resource_key`)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = openServerDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	store, err = newTunnelStoreWithDB(path, db, false)
	if err != nil {
		t.Fatal(err)
	}
	after, err := store.GetAllTunnels()
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("migration changed existing tunnels: before=%+v after=%+v err=%v", before, after, err)
	}
	newClaims, err := queryStringColumn(db, `SELECT resource_key FROM tunnel_resource_locks ORDER BY resource_key`)
	if err != nil || !reflect.DeepEqual(claims, newClaims) {
		t.Fatalf("migration changed claims: before=%v after=%v err=%v", claims, newClaims, err)
	}
	assertNoSQLiteForeignKeyViolations(t, db)
}

func TestTLSSNIStorageCrossProtocolOwnershipAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "netsgo.db")
	store := newTestTunnelStoreAt(t, path)
	base := httpDomainTestTunnel("tls-broad", "alice", "*.example.test")
	base.Type, base.Ingress.Type = protocol.ProxyTypeTLS, protocol.IngressTypeTLSSNI
	base.Ingress.Config = mustRawJSON(protocol.TLSSNIConfig{Domain: "*.example.test", AllowedSourceCIDRs: allowAllSourceCIDRs()})
	mustPrepareTestTunnelOwnership(t, store, base)
	if err := store.AddTunnel(base); err != nil {
		t.Fatal(err)
	}
	for _, domain := range []string{"app.example.test", "*.example.test"} {
		other := httpDomainTestTunnel("http-other", "bob", domain)
		mustPrepareTestTunnelOwnership(t, store, other)
		if err := store.AddTunnel(other); err == nil {
			t.Fatal("cross-user HTTP route overlapped a TLS claim")
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store = newTestTunnelStoreAt(t, path)
	assertHTTPDomainClaim(t, store, "app.example.test", base.ID)
	specific := httpDomainTestTunnel("http-specific", "alice", "app.example.test")
	mustPrepareTestTunnelOwnership(t, store, specific)
	if err := store.AddTunnel(specific); err != nil {
		t.Fatal(err)
	}
	assertHTTPDomainClaim(t, store, "app.example.test", specific.ID)
}

func TestTLSSNIRuntimeProjectionIgnoresLegacyFlatFields(t *testing.T) {
	stored := httpDomainTestTunnel("tls-spec", "alice", "app.example.test")
	stored.Ingress.Type = protocol.IngressTypeTLSSNI
	stored.Ingress.Config = mustRawJSON(protocol.TLSSNIConfig{Domain: "app.example.test", AllowedSourceCIDRs: allowAllSourceCIDRs()})
	stored.Target.Config = mustRawJSON(serviceConfigAPI{Host: "192.0.2.10", Port: 443})
	stored.Type, stored.Domain, stored.LocalIP, stored.LocalPort, stored.RemotePort = "udp", "stale.test", "stale", 1, 2
	config := storedTunnelToProxyConfig(stored)
	if config.Type != protocol.ProxyTypeTLS || config.Domain != "app.example.test" || config.LocalIP != "192.0.2.10" || config.LocalPort != 443 || config.RemotePort != 0 {
		t.Fatalf("TLS runtime read legacy projection: %+v", config)
	}
}
