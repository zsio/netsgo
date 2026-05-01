package manage

import (
	"testing"

	"netsgo/internal/svcmgr"
)

func TestUninstallAllWithRemovesBothRolesAndOptionalBinary(t *testing.T) {
	ui := &fakeUI{
		selects:  []int{1},
		confirms: []bool{true, true, true},
	}

	serverLayout := svcmgr.NewLayout(svcmgr.RoleServer)
	clientLayout := svcmgr.NewLayout(svcmgr.RoleClient)

	serverRemoved := []string{}
	clientRemoved := []string{}
	binaryRemoved := false

	err := uninstallAllWith(uninstallAllDeps{
		UI: ui,
		Server: serverDeps{
			DisableAndStop: func() error { return nil },
			RemovePaths: func(paths ...string) error {
				serverRemoved = append(serverRemoved, paths...)
				return nil
			},
			DaemonReload: func() error { return nil },
			RemoveBinary: func() error {
				binaryRemoved = true
				return nil
			},
		},
		Client: clientDeps{
			DisableAndStop: func() error { return nil },
			RemovePaths: func(paths ...string) error {
				clientRemoved = append(clientRemoved, paths...)
				return nil
			},
		},
	})
	assertSelectionExit(t, err)

	if !containsPath(serverRemoved, serverDataPath(serverLayout)) {
		t.Fatalf("bulk uninstall should remove server data when requested: %v", serverRemoved)
	}
	if !containsPath(clientRemoved, clientDataPath(clientLayout)) {
		t.Fatalf("bulk uninstall should remove client data: %v", clientRemoved)
	}
	if !binaryRemoved {
		t.Fatal("bulk uninstall should support removing the shared binary after both roles are removed")
	}
	if len(ui.summaries) != 3 || ui.summaries[2].title != "托管服务已卸载" {
		t.Fatalf("bulk uninstall should end with a completion summary, got %#v", ui.summaries)
	}
	assertSummaryCallDoesNotContain(t, ui.summaries[1], "身份")
	assertSummaryCallRow(t, ui.summaries[2], "server 角色", "已移除")
	assertSummaryCallRow(t, ui.summaries[2], "client 角色", "已移除")
	assertConfirmPhrase(t, ui.confirmCalls, "在批量移除中包含 server 卸载？", "remove server data")
	assertConfirmPhrase(t, ui.confirmCalls, "在批量移除中包含 client 卸载？", "uninstall client")
	assertConfirmPhrase(t, ui.confirmCalls, "未检测到其他托管角色。是否同时移除共享二进制 /usr/local/bin/netsgo？", "remove binary")
}

func containsPath(paths []string, target string) bool {
	for _, path := range paths {
		if path == target {
			return true
		}
	}
	return false
}
