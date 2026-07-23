package server

import (
	"errors"
	"testing"
)

func TestAdminSecurityActivityMutationRollsBackOnEventFailure(t *testing.T) {
	store := newInitializedAdminStore(t)
	user, err := store.ValidateAdminPassword("admin", "Admin1234")
	if err != nil || user == nil {
		t.Fatalf("load admin user: %+v, %v", user, err)
	}
	actor := ActivityActor{Type: "admin", ID: user.ID, Name: user.Username}
	store.activityStore.failNextAppendsForTest(errors.New("injected activity failure"), 1)
	if _, err := store.UpdateAdminUsernameWithActivity(user.ID, "rolled-back-admin", actor); err == nil {
		t.Fatal("username update should fail when activity append fails")
	}
	reloaded, err := store.GetAdminUserByID(user.ID)
	if err != nil {
		t.Fatalf("reload admin user: %v", err)
	}
	if reloaded.Username != "admin" {
		t.Fatalf("username was not rolled back: %q", reloaded.Username)
	}
	maxID, err := store.activityStore.MaxID()
	if err != nil || maxID != 0 {
		t.Fatalf("rolled-back security activity max id = %d, %v", maxID, err)
	}
}
