package server

import (
	"path/filepath"
	"testing"
	"time"
)

func TestServerPruneActivityEventsUsesPersistedPolicy(t *testing.T) {
	path := filepath.Join(t.TempDir(), serverDBFileName)
	db, err := openServerDB(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	adminStore, err := newAdminStoreWithDB(path, db, false)
	if err != nil {
		t.Fatal(err)
	}
	activityStore := newActivityStoreWithDB(path, db, false)
	s := New(0)
	s.serverDB = db
	s.activityStore = activityStore
	s.auth.adminStore = adminStore

	config, err := adminStore.GetServerConfigE()
	if err != nil {
		t.Fatal(err)
	}
	config.ActivityRetention.Info = ActivityRetentionRule{Days: 1, MinCount: 0}
	if err := adminStore.UpdateServerConfig(config); err != nil {
		t.Fatal(err)
	}
	spec := testActivitySpec("created", time.Now().Add(-48*time.Hour))
	if _, err := activityStore.Append(spec); err != nil {
		t.Fatal(err)
	}
	s.pruneActivityEvents()
	page, err := activityStore.Query(ActivityQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 0 {
		t.Fatalf("expired activity remained: %+v", page.Items)
	}
}
