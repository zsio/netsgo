package server

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestWebhookNotificationIntervalSuppressesFlappingPerTargetAndEvent(t *testing.T) {
	admin, store, owner := newWebhookStoreFixture(t)
	clientA := registerWebhookClient(t, admin, owner.ID, "interval-a", "client-a")
	clientB := registerWebhookClient(t, admin, owner.ID, "interval-b", "client-b")
	input := testWebhookInput("wh_interval")
	input.MinIntervalSeconds = 300
	if _, err := store.Create(owner.ID, input); err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC()
	events := []struct {
		clientID string
		action   string
		seconds  int
		queued   int
	}{
		{clientA.ID, "offline", 0, 1},
		{clientA.ID, "online", 1, 2},
		{clientA.ID, "offline", 2, 2},
		{clientA.ID, "online", 3, 2},
		{clientB.ID, "offline", 4, 3},
		{clientA.ID, "offline", 299, 3},
		{clientA.ID, "offline", 300, 4},
		{clientA.ID, "online", 301, 5},
	}
	for i, event := range events {
		id := appendWebhookClientEvent(t, admin, owner, event.clientID, event.action,
			fmt.Sprintf("interval-%d", i), start.Add(time.Duration(event.seconds)*time.Second))
		var activities, deliveries int
		if err := admin.db.QueryRow(`SELECT COUNT(*) FROM activity_events WHERE id = ?`, id).Scan(&activities); err != nil {
			t.Fatal(err)
		}
		if err := admin.db.QueryRow(`SELECT COUNT(*) FROM activity_webhook_deliveries WHERE webhook_id = ?`, input.ID).Scan(&deliveries); err != nil {
			t.Fatal(err)
		}
		if activities != 1 || deliveries != event.queued {
			t.Fatalf("event %d: activities=%d deliveries=%d, want 1/%d", i, activities, deliveries, event.queued)
		}
	}
}

func TestWebhookNotificationIntervalConfigurationAndStateSurviveRestart(t *testing.T) {
	admin, store, owner := newWebhookStoreFixture(t)
	client := registerWebhookClient(t, admin, owner.ID, "interval-persist", "persist-client")
	input := testWebhookInput("wh_persist")
	input.MinIntervalSeconds = 300
	created, err := store.Create(owner.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	if created.MinIntervalSeconds != 300 || created.snapshot().MinIntervalSeconds != 300 {
		t.Fatalf("created interval = %d", created.MinIntervalSeconds)
	}
	for _, enabled := range []bool{false, true} {
		current, err := store.SetEnabled(owner.ID, input.ID, enabled)
		if err != nil || current.MinIntervalSeconds != 300 {
			t.Fatalf("toggle: interval=%d err=%v", current.MinIntervalSeconds, err)
		}
		created = current
	}
	input = created.toConfigInput(true)
	input.MinIntervalSeconds = 600
	if _, err := store.Update(owner.ID, input.ID, input); err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC()
	appendWebhookClientEvent(t, admin, owner, client.ID, "offline", "before-restart", start)
	if err := admin.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewAdminStore(admin.path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	store = newWebhookStoreWithDB(reopened.db)
	current, err := store.Get(owner.ID, input.ID)
	if err != nil || current.MinIntervalSeconds != 600 {
		t.Fatalf("reopened interval=%d err=%v", current.MinIntervalSeconds, err)
	}
	appendWebhookClientEvent(t, reopened, owner, client.ID, "offline", "after-restart", start.Add(300*time.Second))
	page, err := store.ListDeliveries(owner.ID, input.ID, "", 50, "")
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("reopened deliveries=%d err=%v", len(page.Items), err)
	}
}

func TestWebhookNotificationIntervalDoesNotLimitTestsReplaysOrOtherWebhooks(t *testing.T) {
	admin, store, owner := newWebhookStoreFixture(t)
	client := registerWebhookClient(t, admin, owner.ID, "interval-manual", "manual-client")
	input := testWebhookInput("wh_manual")
	input.MinIntervalSeconds = 300
	if _, err := store.Create(owner.ID, input); err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC()
	appendWebhookClientEvent(t, admin, owner, client.ID, "offline", "manual-event", start)
	page, err := store.ListDeliveries(owner.ID, input.ID, "", 50, "")
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("initial deliveries=%d err=%v", len(page.Items), err)
	}
	for range 2 {
		if _, err := store.EnqueueTest(owner.ID, input, "client.offline"); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Replay(owner.ID, page.Items[0].ID); err != nil {
			t.Fatal(err)
		}
	}
	other := input
	other.ID = "wh_other_interval"
	if _, err := store.Create(owner.ID, other); err != nil {
		t.Fatal(err)
	}
	appendWebhookClientEvent(t, admin, owner, client.ID, "offline", "manual-repeat", start.Add(time.Second))
	for id, count := range map[string]int{input.ID: 5, other.ID: 1} {
		page, err := store.ListDeliveries(owner.ID, id, "", 50, "")
		if err != nil || len(page.Items) != count {
			t.Fatalf("%s deliveries=%d want=%d err=%v", id, len(page.Items), count, err)
		}
	}
}

func TestWebhookNotificationIntervalZeroAndOwnerIsolation(t *testing.T) {
	admin, store, owner := newWebhookStoreFixture(t)
	client := registerWebhookClient(t, admin, owner.ID, "interval-zero", "zero-client")
	input := testWebhookInput("wh_shared_id")
	if _, err := store.Create(owner.ID, input); err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC()
	for i := range 3 {
		appendWebhookClientEvent(t, admin, owner, client.ID, "offline", fmt.Sprintf("zero-%d", i), start)
	}
	page, err := store.ListDeliveries(owner.ID, input.ID, "", 50, "")
	if err != nil || len(page.Items) != 3 {
		t.Fatalf("zero interval deliveries=%d err=%v", len(page.Items), err)
	}
	current, err := store.Get(owner.ID, input.ID)
	if err != nil {
		t.Fatal(err)
	}
	input = current.toConfigInput(true)
	input.MinIntervalSeconds = 300
	if _, err := store.Update(owner.ID, input.ID, input); err != nil {
		t.Fatal(err)
	}
	appendWebhookClientEvent(t, admin, owner, client.ID, "offline", "owner-interval", start)
	other, err := admin.CreateUser("interval-other", "Password123")
	if err != nil {
		t.Fatal(err)
	}
	otherClient := registerWebhookClient(t, admin, other.ID, "interval-other", "other-client")
	if _, err := store.Create(other.ID, input); err != nil {
		t.Fatal(err)
	}
	appendWebhookClientEvent(t, admin, other, otherClient.ID, "offline", "other-owner-interval", start)
	page, err = store.ListDeliveries(other.ID, input.ID, "", 50, "")
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("other owner deliveries=%d err=%v", len(page.Items), err)
	}
}

func TestWebhookNotificationIntervalValidation(t *testing.T) {
	catalog := activityWebhookCatalog()
	for _, seconds := range []int{-1, 0, 300, 86400, 86401} {
		input := testWebhookInput("wh_interval_validation")
		input.MinIntervalSeconds = seconds
		err := validateWebhookInput(input, catalog.Fixtures, catalog.Variables, true)
		if seconds < 0 || seconds > 86400 {
			assertWebhookValidationError(t, err, "min_interval_seconds", "invalid_interval")
		} else if err != nil {
			t.Fatalf("interval %d: %v", seconds, err)
		}
	}
}

func TestWebhookNotificationIntervalConcurrentEventsQueueOnce(t *testing.T) {
	admin, store, owner := newWebhookStoreFixture(t)
	client := registerWebhookClient(t, admin, owner.ID, "interval-concurrent", "concurrent-client")
	input := testWebhookInput("wh_concurrent_interval")
	input.MinIntervalSeconds = 300
	if _, err := store.Create(owner.ID, input); err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC()
	var workers sync.WaitGroup
	for i := range 12 {
		workers.Go(func() {
			appendWebhookClientEvent(t, admin, owner, client.ID, "offline", fmt.Sprintf("concurrent-interval-%d", i), start)
		})
	}
	workers.Wait()
	page, err := store.ListDeliveries(owner.ID, input.ID, "", 50, "")
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("concurrent deliveries=%d err=%v", len(page.Items), err)
	}
}

func TestWebhookNotificationIntervalCleanup(t *testing.T) {
	admin, store, owner := newWebhookStoreFixture(t)
	client := registerWebhookClient(t, admin, owner.ID, "interval-cleanup", "cleanup-client")
	input := testWebhookInput("wh_cleanup_interval")
	input.MinIntervalSeconds = 300
	if _, err := store.Create(owner.ID, input); err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC()
	appendWebhookClientEvent(t, admin, owner, client.ID, "offline", "cleanup-old", start.Add(-24*time.Hour))
	appendWebhookClientEvent(t, admin, owner, client.ID, "online", "cleanup-current", start)
	if _, err := store.Prune(start); err != nil {
		t.Fatal(err)
	}
	var states int
	if err := admin.db.QueryRow(`SELECT COUNT(*) FROM activity_webhook_notification_state`).Scan(&states); err != nil || states != 1 {
		t.Fatalf("pruned states=%d err=%v", states, err)
	}
	if err := store.Delete(owner.ID, input.ID); err != nil {
		t.Fatal(err)
	}
	if err := admin.db.QueryRow(`SELECT COUNT(*) FROM activity_webhook_notification_state`).Scan(&states); err != nil || states != 0 {
		t.Fatalf("deleted Webhook states=%d err=%v", states, err)
	}
}

func TestWebhookNotificationIntervalStateRollsBackWithDelivery(t *testing.T) {
	admin, store, owner := newWebhookStoreFixture(t)
	client := registerWebhookClient(t, admin, owner.ID, "interval-atomic", "atomic-client")
	input := testWebhookInput("wh_atomic_interval")
	input.MinIntervalSeconds = 300
	if _, err := store.Create(owner.ID, input); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.db.Exec(`CREATE TRIGGER reject_notification_state
		AFTER INSERT ON activity_webhook_notification_state
		BEGIN SELECT RAISE(ABORT, 'forced notification state failure'); END`); err != nil {
		t.Fatal(err)
	}
	activityStore := newActivityStoreWithDB("", admin.db, false)
	_, err := activityStore.Append(ActivityEventSpec{
		Category: ActivityCategoryClient, Action: "offline", Source: "test",
		ScopeUserID: owner.ID, SubjectUserID: owner.ID, DedupeKey: "interval-atomic-failed",
		Actor:   ActivityActor{Type: "client", ID: client.ID},
		Payload: newActivityClientLifecyclePayload("offline", "", 1, true, ActivitySummaryArgs{ClientName: client.ID}),
		Clients: []ActivityClientSubject{{ClientID: client.ID, Relation: "subject"}},
	})
	if err == nil || !strings.Contains(err.Error(), "forced notification state failure") {
		t.Fatalf("append error=%v, want state failure", err)
	}
	for _, table := range []string{"activity_webhook_deliveries", "activity_webhook_notification_state"} {
		var count int
		if err := admin.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s rows=%d err=%v", table, count, err)
		}
	}
	if _, err := admin.db.Exec(`DROP TRIGGER reject_notification_state`); err != nil {
		t.Fatal(err)
	}
	appendWebhookClientEvent(t, admin, owner, client.ID, "offline", "interval-atomic-retry", time.Now().UTC())
	page, err := store.ListDeliveries(owner.ID, input.ID, "", 50, "")
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("retry deliveries=%d err=%v", len(page.Items), err)
	}
}
