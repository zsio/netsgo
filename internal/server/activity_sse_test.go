package server

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

func TestPublishActivityIDBroadcastsCommittedItem(t *testing.T) {
	path := filepath.Join(t.TempDir(), serverDBFileName)
	db, err := openServerDB(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s := New(0)
	s.serverDB = db
	s.activityStore = newActivityStoreWithDB(path, db, false)
	ch := s.events.Subscribe()
	defer s.events.Unsubscribe(ch)
	id, err := s.activityStore.Append(testActivitySpec("created", time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	s.publishActivityID(id)
	select {
	case event := <-ch:
		if event.Type != "activity_event" {
			t.Fatalf("event type = %q", event.Type)
		}
		var item ActivityItem
		if err := json.Unmarshal([]byte(event.Data), &item); err != nil {
			t.Fatal(err)
		}
		if item.ID != id || item.Action != "created" {
			t.Fatalf("activity hint = %+v", item)
		}
	case <-time.After(time.Second):
		t.Fatal("activity hint was not published")
	}
}

func TestPublishActivityIDIgnoresRolledBackID(t *testing.T) {
	s := New(0)
	path := filepath.Join(t.TempDir(), serverDBFileName)
	db, err := openServerDB(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s.serverDB = db
	s.activityStore = newActivityStoreWithDB(path, db, false)
	ch := s.events.Subscribe()
	defer s.events.Unsubscribe(ch)
	s.publishActivityID(999)
	select {
	case event := <-ch:
		t.Fatalf("unexpected hint for absent event: %+v", event)
	case <-time.After(25 * time.Millisecond):
	}
}
