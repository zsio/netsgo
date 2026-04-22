package manage

import (
	"fmt"

	"netsgo/internal/svcmgr"
	"netsgo/pkg/updater"
	"netsgo/pkg/version"
)

func runUpdate(ui uiProvider, currentVersion string, hasInstalled func() bool, autoUpdate func(updater.DownloadChannel, string) (*updater.Result, error)) error {
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

	if autoUpdate == nil {
		autoUpdate = updater.AutoUpdate
	}

	result, err := autoUpdate(channel, currentVersion)
	if err != nil {
		ui.PrintSummary("Update failed", [][2]string{{"Error", err.Error()}})
		return nil
	}

	if result.NewVersion == currentVersion {
		ui.PrintSummary("No update", [][2]string{
			{"Current", currentVersion},
			{"Status", "Already latest"},
		})
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
