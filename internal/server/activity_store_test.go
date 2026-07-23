package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func newTestActivityStore(t *testing.T) *ActivityStore {
	t.Helper()
	store, err := NewActivityStore(filepath.Join(t.TempDir(), serverDBFileName))
	if err != nil {
		t.Fatalf("NewActivityStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func testActivitySpec(action string, occurredAt time.Time) ActivityEventSpec {
	return ActivityEventSpec{
		OccurredAt: occurredAt,
		Category:   ActivityCategoryTunnel,
		Action:     action,
		Source:     "test",
		Actor:      ActivityActor{Type: "system"},
		Payload: newActivityPayload(ActivityCategoryTunnel, action, ActivitySummaryArgs{
			TunnelName: "web",
		}),
		Clients: []ActivityClientSubject{
			{ClientID: "client-a", Relation: "owner", DisplayName: "Alpha"},
			{ClientID: "client-a", Relation: "owner", DisplayName: "duplicate"},
			{ClientID: "client-b", Relation: "target", Hostname: "beta.local"},
		},
		Tunnels: []ActivityTunnelSubject{
			{TunnelID: "tunnel-a", Relation: "subject", Name: "web", Type: "tcp", Topology: "server_relay"},
		},
	}
}

func TestActivityStoreAppendQueryAndCursor(t *testing.T) {
	store := newTestActivityStore(t)
	base := time.Date(2026, 7, 23, 1, 2, 3, 0, time.UTC)
	ids := make([]int64, 0, 4)
	for index, action := range []string{"created", "updated", "stopped", "resumed"} {
		spec := testActivitySpec(action, base.Add(time.Duration(index)*time.Nanosecond))
		id, err := store.Append(spec)
		if err != nil {
			t.Fatalf("Append(%s) error = %v", action, err)
		}
		ids = append(ids, id)
	}

	page, err := store.Query(ActivityQuery{Scope: ActivityScopeGlobal, Limit: 2})
	if err != nil {
		t.Fatalf("Query first page error = %v", err)
	}
	if got := activityIDs(page.Items); !reflect.DeepEqual(got, []int64{ids[3], ids[2]}) {
		t.Fatalf("first page IDs = %#v", got)
	}
	if !page.HasMore || page.NextCursor != ids[2] || page.Direction != ActivityDirectionBefore {
		t.Fatalf("first page cursor = %+v", page)
	}
	if len(page.Items[0].Clients) != 2 || len(page.Items[0].Tunnels) != 1 {
		t.Fatalf("subjects were not de-duplicated: %+v", page.Items[0])
	}

	older, err := store.Query(ActivityQuery{Scope: ActivityScopeGlobal, BeforeID: page.NextCursor, Limit: 2})
	if err != nil {
		t.Fatalf("Query older page error = %v", err)
	}
	if got := activityIDs(older.Items); !reflect.DeepEqual(got, []int64{ids[1], ids[0]}) {
		t.Fatalf("older page IDs = %#v", got)
	}

	after, err := store.Query(ActivityQuery{Scope: ActivityScopeGlobal, AfterID: ids[0], Limit: 2})
	if err != nil {
		t.Fatalf("Query after page error = %v", err)
	}
	if got := activityIDs(after.Items); !reflect.DeepEqual(got, []int64{ids[2], ids[1]}) {
		t.Fatalf("after page normalized IDs = %#v", got)
	}
	if after.NextCursor != ids[2] || !after.HasMore || after.Direction != ActivityDirectionAfter {
		t.Fatalf("after page cursor = %+v", after)
	}
}

func TestActivityStoreScopeFiltersDoNotDuplicateEvents(t *testing.T) {
	store := newTestActivityStore(t)
	spec := testActivitySpec("created", time.Now())
	spec.Clients = append(spec.Clients, ActivityClientSubject{ClientID: "client-a", Relation: "ingress"})
	id, err := store.Append(spec)
	if err != nil {
		t.Fatalf("Append() error = %v", err)
	}

	clientPage, err := store.Query(ActivityQuery{Scope: ActivityScopeClient, ScopeID: "client-a", Limit: 50})
	if err != nil {
		t.Fatalf("client Query() error = %v", err)
	}
	if got := activityIDs(clientPage.Items); !reflect.DeepEqual(got, []int64{id}) {
		t.Fatalf("client scoped IDs = %#v", got)
	}
	tunnelPage, err := store.Query(ActivityQuery{Scope: ActivityScopeTunnel, ScopeID: "tunnel-a", Limit: 50})
	if err != nil {
		t.Fatalf("tunnel Query() error = %v", err)
	}
	if got := activityIDs(tunnelPage.Items); !reflect.DeepEqual(got, []int64{id}) {
		t.Fatalf("tunnel scoped IDs = %#v", got)
	}
}

func TestActivityStoreDedupeAndAutoincrementDoNotReuseIDs(t *testing.T) {
	store := newTestActivityStore(t)
	spec := testActivitySpec("created", time.Now())
	spec.DedupeKey = "boot:client:generation:online"
	first, err := store.Append(spec)
	if err != nil {
		t.Fatalf("first Append() error = %v", err)
	}
	duplicate, err := store.Append(spec)
	if err != nil {
		t.Fatalf("duplicate Append() error = %v", err)
	}
	if duplicate != first {
		t.Fatalf("dedupe ID = %d, want %d", duplicate, first)
	}
	if _, err := store.db.Exec(`DELETE FROM activity_events WHERE id = ?`, first); err != nil {
		t.Fatalf("delete event: %v", err)
	}
	spec.DedupeKey = "boot:client:generation:offline"
	second, err := store.Append(spec)
	if err != nil {
		t.Fatalf("second Append() error = %v", err)
	}
	if second <= first {
		t.Fatalf("AUTOINCREMENT reused ID: first=%d second=%d", first, second)
	}
}

func TestActivityStoreRejectsUnsafePayloadAndRollsBackSubjects(t *testing.T) {
	store := newTestActivityStore(t)
	payload := newActivityPayload(ActivityCategoryTunnel, "created", ActivitySummaryArgs{TunnelName: strings.Repeat("x", activityPayloadMaxBytes)})
	spec := testActivitySpec("created", time.Now())
	spec.Payload = payload
	if _, err := store.Append(spec); err == nil || !strings.Contains(err.Error(), "payload exceeds") {
		t.Fatalf("oversized payload error = %v", err)
	}
	assertActivityCounts(t, store.db, 0, 0, 0)

	spec = testActivitySpec("created", time.Now())
	spec.Clients = []ActivityClientSubject{{ClientID: "client", Relation: "invalid"}}
	if _, err := store.Append(spec); err == nil || !strings.Contains(err.Error(), "relation") {
		t.Fatalf("invalid relation error = %v", err)
	}
	assertActivityCounts(t, store.db, 0, 0, 0)
}

func TestActivityStoreSubjectLimitsAndActorPrivacy(t *testing.T) {
	store := newTestActivityStore(t)
	actor := NewActivityActor("admin", "user", "admin", "203.0.113.42", "persistent-secret")
	if actor.IPPrefix != "203.0.113.0/24" || actor.IPHash == "" || strings.Contains(actor.IPHash, "203.0.113.42") {
		t.Fatalf("actor IP privacy snapshot = %+v", actor)
	}
	spec := testActivitySpec("created", time.Now())
	spec.Actor = actor
	spec.Clients = []ActivityClientSubject{{
		ClientID: "client", Relation: "subject", Hostname: strings.Repeat("界", 100),
	}}
	id, err := store.Append(spec)
	if err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	item, err := store.GetByID(id)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if !item.Clients[0].Truncated || len(item.Clients[0].Hostname) > activityNameMaxBytes || !json.Valid(item.Payload) {
		t.Fatalf("truncated subject = %+v", item.Clients[0])
	}
	if item.Actor.IPHash != actor.IPHash || item.Actor.IPPrefix != actor.IPPrefix {
		t.Fatalf("actor round trip = %+v", item.Actor)
	}
}

func TestActivityStoreBadPayloadFailsQuery(t *testing.T) {
	store := newTestActivityStore(t)
	if _, err := store.db.Exec(`INSERT INTO activity_events
		(occurred_at_ns, recorded_at_ns, severity, category, action, source, actor_type, payload_json)
		VALUES (?, ?, 'info', 'tunnel', 'created', 'test', 'system', '{')`, time.Now().UnixNano(), time.Now().UnixNano()); err != nil {
		t.Fatalf("seed malformed payload: %v", err)
	}
	if _, err := store.Query(ActivityQuery{Scope: ActivityScopeGlobal, Limit: 50}); err == nil || !strings.Contains(err.Error(), "invalid payload") {
		t.Fatalf("bad payload Query() error = %v", err)
	}
}

func TestActivityStoreAppendTxFailureRollsBackDomainMutation(t *testing.T) {
	store := newTestActivityStore(t)
	if _, err := store.db.Exec(`CREATE TABLE activity_domain_test (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatalf("create domain table: %v", err)
	}
	store.failNextAppendsForTest(errors.New("injected activity failure"), 1)
	tx, err := store.db.Begin()
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	if _, err := tx.Exec(`INSERT INTO activity_domain_test (id) VALUES (1)`); err != nil {
		t.Fatalf("insert domain row: %v", err)
	}
	if _, err := store.appendTx(tx, testActivitySpec("created", time.Now())); err == nil {
		t.Fatal("appendTx should fail")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM activity_domain_test`).Scan(&count); err != nil {
		t.Fatalf("count domain rows: %v", err)
	}
	if count != 0 {
		t.Fatalf("domain rows after activity failure = %d", count)
	}
}

func TestActivityRetentionUsesAgeAndMinimumCount(t *testing.T) {
	store := newTestActivityStore(t)
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	for index, age := range []time.Duration{10 * 24 * time.Hour, 9 * 24 * time.Hour, 8 * 24 * time.Hour, 1 * time.Hour} {
		spec := testActivitySpec("created", now.Add(-age))
		spec.DedupeKey = fmt.Sprintf("info-%d", index)
		if _, err := store.Append(spec); err != nil {
			t.Fatalf("Append(%d) error = %v", index, err)
		}
	}
	policy := DefaultActivityRetentionPolicy()
	policy.Info = ActivityRetentionRule{Days: 7, MinCount: 2}
	deleted, err := store.Prune(now, policy)
	if err != nil {
		t.Fatalf("Prune() error = %v", err)
	}
	if deleted != 2 {
		t.Fatalf("deleted = %d, want 2", deleted)
	}
	page, err := store.Query(ActivityQuery{Scope: ActivityScopeGlobal, Limit: 50})
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("remaining items = %d, want 2", len(page.Items))
	}
	var clientSubjects int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM activity_event_clients`).Scan(&clientSubjects); err != nil {
		t.Fatalf("count client subjects: %v", err)
	}
	if clientSubjects != 4 {
		t.Fatalf("client subjects after cascade = %d, want 4", clientSubjects)
	}
}

func TestActivityRetentionZeroMinimumDeletesAllExpired(t *testing.T) {
	store := newTestActivityStore(t)
	now := time.Now().UTC()
	for index := range 3 {
		spec := testActivitySpec("created", now.Add(-48*time.Hour))
		spec.DedupeKey = fmt.Sprintf("old-%d", index)
		if _, err := store.Append(spec); err != nil {
			t.Fatalf("Append() error = %v", err)
		}
	}
	policy := DefaultActivityRetentionPolicy()
	policy.Info = ActivityRetentionRule{Days: 1, MinCount: 0}
	deleted, err := store.Prune(now, policy)
	if err != nil {
		t.Fatalf("Prune() error = %v", err)
	}
	if deleted != 3 {
		t.Fatalf("deleted = %d, want 3", deleted)
	}
	assertActivityCounts(t, store.db, 0, 0, 0)
}

func TestActivityStoreRetentionQueryPlanUsesSeverityIndex(t *testing.T) {
	store := newTestActivityStore(t)
	rows, err := store.db.Query(`EXPLAIN QUERY PLAN SELECT id FROM activity_events
		WHERE severity = ? ORDER BY occurred_at_ns DESC, id DESC LIMIT ?`, ActivitySeverityInfo, 100)
	if err != nil {
		t.Fatalf("EXPLAIN QUERY PLAN error = %v", err)
	}
	defer rows.Close()
	var details []string
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			t.Fatalf("scan plan: %v", err)
		}
		details = append(details, detail)
	}
	joined := strings.Join(details, "\n")
	if !strings.Contains(joined, "idx_activity_events_severity_occurred") || strings.Contains(joined, "TEMP B-TREE") {
		t.Fatalf("unexpected retention plan:\n%s", joined)
	}
}

func activityIDs(items []ActivityItem) []int64 {
	ids := make([]int64, len(items))
	for index, item := range items {
		ids[index] = item.ID
	}
	return ids
}

func assertActivityCounts(t *testing.T, db *sql.DB, events, clients, tunnels int) {
	t.Helper()
	for table, want := range map[string]int{
		"activity_events":        events,
		"activity_event_clients": clients,
		"activity_event_tunnels": tunnels,
	} {
		var got int
		if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&got); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if got != want {
			t.Fatalf("%s count = %d, want %d", table, got, want)
		}
	}
}
