package server

func applyActivityRetentionPatch(current ActivityRetentionPolicy, patch *activityRetentionPatch) ActivityRetentionPolicy {
	if current == (ActivityRetentionPolicy{}) {
		current = DefaultActivityRetentionPolicy()
	}
	if patch == nil {
		return current
	}
	current.Debug = applyActivityRetentionRulePatch(current.Debug, patch.Debug)
	current.Info = applyActivityRetentionRulePatch(current.Info, patch.Info)
	current.Warning = applyActivityRetentionRulePatch(current.Warning, patch.Warning)
	current.Error = applyActivityRetentionRulePatch(current.Error, patch.Error)
	return current
}

func applyActivityRetentionRulePatch(current ActivityRetentionRule, patch *activityRetentionRulePatch) ActivityRetentionRule {
	if patch == nil {
		return current
	}
	if patch.Days != nil {
		current.Days = *patch.Days
	}
	if patch.MinCount != nil {
		current.MinCount = *patch.MinCount
	}
	return current
}
