package manage

import (
	"fmt"

	"netsgo/internal/svcmgr"
	"netsgo/pkg/updater"
	"netsgo/pkg/version"
)

type updateChecker func(updater.DownloadChannel, string) (*updater.Result, bool, error)
type confirmedUpdateApplier func(updater.DownloadChannel, string, string) (*updater.Result, error)

func runUpdate(ui uiProvider, currentVersion string, hasInstalled func() bool) error {
	return runUpdateWithChecker(ui, currentVersion, hasInstalled, updater.CheckForUpdate, updater.ApplyConfirmedUpdate)
}

func runUpdateWithChecker(ui uiProvider, currentVersion string, hasInstalled func() bool, checkForUpdate updateChecker, applyConfirmedUpdate confirmedUpdateApplier) error {
	if hasInstalled == nil {
		hasInstalled = func() bool {
			return svcmgr.Detect(svcmgr.RoleServer) == svcmgr.StateInstalled ||
				svcmgr.Detect(svcmgr.RoleClient) == svcmgr.StateInstalled
		}
	}

	if !hasInstalled() {
		return fmt.Errorf("no installed services found")
	}

	if _, err := version.ParseSemver(currentVersion); err != nil {
		ui.PrintSummary("Update", [][2]string{
			{"Version", currentVersion},
			{"Status", "Development build — automatic update not supported"},
		})
		return nil
	}

	channelIdx, err := ui.Select("Select download channel", []string{"GitHub (default)", "ghproxy (mirror)"})
	if err != nil {
		return err
	}

	channel := updater.ChannelGitHub
	if channelIdx == 1 {
		channel = updater.ChannelGhproxy
	}

	ui.PrintSummary("Update", [][2]string{
		{"Current", currentVersion},
		{"Channel", string(channel)},
		{"Status", "Checking..."},
	})

	if checkForUpdate == nil {
		checkForUpdate = updater.CheckForUpdate
	}
	if applyConfirmedUpdate == nil {
		applyConfirmedUpdate = updater.ApplyConfirmedUpdate
	}

	result, needsUpdate, err := checkForUpdate(channel, currentVersion)
	if err != nil {
		ui.PrintSummary("Update failed", [][2]string{{"Error", err.Error()}})
		return nil
	}

	if !needsUpdate {
		currentNormalized, normalizeErr := version.NormalizeVersionString(currentVersion)
		if normalizeErr != nil {
			currentNormalized = currentVersion
		}
		ui.PrintSummary("No update", [][2]string{
			{"Current", currentNormalized},
			{"Status", "Already latest"},
		})
		return nil
	}

	confirmRows := [][2]string{
		{"Current", result.OldVersion},
		{"Latest", result.NewVersion},
		{"Channel", string(channel)},
		{"Action", "Download, replace binary, and restart managed services"},
	}
	ui.PrintSummary("Update available", confirmRows)

	confirmed, err := ui.Confirm("Download and apply this update?")
	if err != nil {
		return err
	}
	if !confirmed {
		ui.PrintSummary("Update cancelled", [][2]string{{"Status", "No changes were made"}})
		return nil
	}

	result, err = applyConfirmedUpdate(channel, currentVersion, result.NewVersion)
	if err != nil {
		ui.PrintSummary("Update failed", [][2]string{{"Error", err.Error()}})
		return nil
	}

	rows := [][2]string{
		{"From", result.OldVersion},
		{"To", result.NewVersion},
	}
	if len(result.Stopped) > 0 {
		rows = append(rows, [2]string{"Stopped", fmt.Sprintf("%v", result.Stopped)})
	}
	if len(result.Started) > 0 {
		rows = append(rows, [2]string{"Started", fmt.Sprintf("%v", result.Started)})
	}
	ui.PrintSummary("Update complete", rows)
	return nil
}
