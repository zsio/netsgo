package server

import "testing"

func TestActivityRetentionPatchPreservesMissingValues(t *testing.T) {
	current := DefaultActivityRetentionPolicy()
	days := 14
	zero := 0
	got := applyActivityRetentionPatch(current, &activityRetentionPatch{
		Info:  &activityRetentionRulePatch{Days: &days},
		Error: &activityRetentionRulePatch{MinCount: &zero},
	})
	if got.Info.Days != 14 || got.Info.MinCount != current.Info.MinCount || got.Error.Days != current.Error.Days || got.Error.MinCount != 0 || got.Debug != current.Debug || got.Warning != current.Warning {
		t.Fatalf("patched retention = %+v", got)
	}
}

func TestActivityRetentionValidationBounds(t *testing.T) {
	for _, mutate := range []func(*ActivityRetentionPolicy){
		func(p *ActivityRetentionPolicy) { p.Debug.Days = 0 },
		func(p *ActivityRetentionPolicy) { p.Info.Days = 3651 },
		func(p *ActivityRetentionPolicy) { p.Warning.MinCount = -1 },
		func(p *ActivityRetentionPolicy) { p.Error.MinCount = 100001 },
	} {
		policy := DefaultActivityRetentionPolicy()
		mutate(&policy)
		if err := policy.validate(); err == nil {
			t.Fatalf("invalid retention accepted: %+v", policy)
		}
	}
}

func TestAdminStorePersistsActivityRetentionPolicy(t *testing.T) {
	store := newInitializedAdminStore(t)
	config, err := store.GetServerConfigE()
	if err != nil {
		t.Fatal(err)
	}
	if config.ActivityRetention != DefaultActivityRetentionPolicy() {
		t.Fatalf("default retention = %+v", config.ActivityRetention)
	}
	config.ActivityRetention.Info = ActivityRetentionRule{Days: 14, MinCount: 321}
	if err := store.UpdateServerConfig(config); err != nil {
		t.Fatal(err)
	}
	reloaded, err := store.GetServerConfigE()
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.ActivityRetention != config.ActivityRetention {
		t.Fatalf("reloaded retention = %+v, want %+v", reloaded.ActivityRetention, config.ActivityRetention)
	}
}
