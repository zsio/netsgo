package manage

import (
	"path/filepath"
	"testing"

	"netsgo/internal/svcmgr"
)

func TestUninstallAllWithRemovesBothRolesAndOptionalBinary(t *testing.T) {
	ui := &fakeUI{
		selects:  []int{1},
		confirms: []bool{true, true, true},
	}

	serverSpec := svcmgr.NewSpec(svcmgr.RoleServer)
	serverSpec.DataDir = t.TempDir()
	serverSpec.UnitPath = filepath.Join(serverSpec.DataDir, "netsgo-server.service")
	serverSpec.EnvPath = filepath.Join(serverSpec.DataDir, "server.env")
	serverSpec.SpecPath = filepath.Join(serverSpec.DataDir, "server.json")

	clientSpec := svcmgr.NewSpec(svcmgr.RoleClient)
	clientSpec.DataDir = t.TempDir()
	clientSpec.UnitPath = filepath.Join(clientSpec.DataDir, "netsgo-client.service")
	clientSpec.EnvPath = filepath.Join(clientSpec.DataDir, "client.env")
	clientSpec.SpecPath = filepath.Join(clientSpec.DataDir, "client.json")

	serverRemoved := []string{}
	clientRemoved := []string{}
	binaryRemoved := false

	err := uninstallAllWith(uninstallAllDeps{
		UI: ui,
		Server: serverDeps{
			ReadServerSpec: func() (svcmgr.ServiceSpec, error) { return serverSpec, nil },
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
			ReadClientSpec: func() (svcmgr.ServiceSpec, error) { return clientSpec, nil },
			DisableAndStop: func() error { return nil },
			RemovePaths: func(paths ...string) error {
				clientRemoved = append(clientRemoved, paths...)
				return nil
			},
		},
	})
	assertSelectionExit(t, err)

	if !containsPath(serverRemoved, serverDataPath(serverSpec)) {
		t.Fatalf("bulk uninstall should remove server data when requested: %v", serverRemoved)
	}
	if !containsPath(clientRemoved, clientDataPath(clientSpec)) {
		t.Fatalf("bulk uninstall should remove client data: %v", clientRemoved)
	}
	if !binaryRemoved {
		t.Fatal("bulk uninstall should support removing the shared binary after both roles are removed")
	}
	if len(ui.summaries) != 3 || ui.summaries[2].title != "Managed services uninstalled" {
		t.Fatalf("bulk uninstall should end with a completion summary, got %#v", ui.summaries)
	}
}

func containsPath(paths []string, target string) bool {
	for _, path := range paths {
		if path == target {
			return true
		}
	}
	return false
}
