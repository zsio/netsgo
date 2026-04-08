package install

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"

	"netsgo/internal/server"
	"netsgo/internal/svcmgr"
	"netsgo/internal/tui"
)

var errInstallBrokenState = errors.New("install: broken existing state")

type serverDeps struct {
	UI                uiProvider
	Inspect           func(svcmgr.Role) svcmgr.InstallInspection
	Detect            func(svcmgr.Role) svcmgr.InstallState
	SelectTLSMode     func(ui uiProvider) (string, error)
	LoadRecoverable   func() (server.InitParams, error)
	EnsureUser        func(string) error
	EnsureDirs        func() error
	ApplyInit         func(string, server.InitParams) error
	CurrentBinaryPath func() (string, error)
	InstallBinary     func(string) error
	WriteServerSpec   func(svcmgr.ServiceSpec) error
	WriteServerEnv    func(svcmgr.ServiceSpec, svcmgr.ServerEnv) error
	WriteServerUnit   func(svcmgr.ServiceSpec) error
	DaemonReload      func() error
	EnableAndStart    func(string) error
}

func InstallServer() error {
	return InstallServerWith(defaultServerDeps())
}

func InstallServerWith(deps serverDeps) error {
	inspection := resolveInspection(deps.Inspect, deps.Detect, svcmgr.RoleServer)
	state := inspection.State
	switch state {
	case svcmgr.StateInstalled:
		printInstalledSummary(deps.UI, "Server already installed")
		return nil
	case svcmgr.StateBroken:
		printBrokenSummary(deps.UI, "Server installation state is broken", inspection)
		return errInstallBrokenState
	}

	portRaw, err := deps.UI.Input("Listening port", tui.InputOptions{
		Placeholder: "e.g. 9527",
		Description: "TCP port for the server to listen on (1–65535)",
		Default:     "9527",
		Validate: func(s string) error {
			n, err := strconv.Atoi(s)
			if err != nil || n < 1 || n > 65535 {
				return fmt.Errorf("port must be a number between 1 and 65535")
			}
			return nil
		},
	})
	if err != nil {
		return err
	}
	port := 9527
	if portRaw != "" {
		port, err = strconv.Atoi(portRaw)
		if err != nil {
			return err
		}
	}
	tlsMode, err := deps.SelectTLSMode(deps.UI)
	if err != nil {
		return err
	}
	trustedProxies, err := deps.UI.Input("Trusted proxy CIDRs", tui.InputOptions{
		Placeholder: "e.g. 127.0.0.1/8,192.168.0.0/16",
		Description: "Comma-separated list of trusted proxy CIDRs (leave empty if not behind a proxy)",
	})
	if err != nil {
		return err
	}
	tlsCert := ""
	tlsKey := ""
	if tlsMode == "custom" {
		tlsCert, err = deps.UI.Input("TLS certificate path", tui.InputOptions{
			Placeholder: "e.g. /etc/ssl/certs/netsgo.pem",
			Validate: func(s string) error {
				if strings.TrimSpace(s) == "" {
					return fmt.Errorf("TLS certificate path cannot be empty")
				}
				return nil
			},
		})
		if err != nil {
			return err
		}
		tlsKey, err = deps.UI.Input("TLS private key path", tui.InputOptions{
			Placeholder: "e.g. /etc/ssl/private/netsgo.key",
			Validate: func(s string) error {
				if strings.TrimSpace(s) == "" {
					return fmt.Errorf("TLS private key path cannot be empty")
				}
				return nil
			},
		})
		if err != nil {
			return err
		}
	}

	serverAddr := ""
	initParams := server.InitParams{}
	if state == svcmgr.StateHistoricalDataOnly {
		deps.UI.PrintSummary("Detected existing server data", [][2]string{
			{"Status", "Recoverable"},
			{"Note", "If you continue, existing admin credentials, ServerAddr, AllowedPorts, and runtime data will be reused"},
		})
		ok, err := deps.UI.Confirm("Continue installation using existing data?")
		if err != nil {
			return err
		}
		if !ok {
			deps.UI.PrintSummary("Installation cancelled", [][2]string{
				{"Next step", "To reinitialize, clear the existing server data directory and run install again"},
			})
			return nil
		}
		if deps.LoadRecoverable == nil {
			return errors.New("install dependencies are incomplete")
		}
		initParams, err = deps.LoadRecoverable()
		if err != nil {
			return err
		}
		serverAddr = initParams.ServerAddr
	} else {
		serverAddr, err = deps.UI.Input("Server external address", tui.InputOptions{
			Placeholder: "e.g. https://netsgo.example.com",
			Description: "Public URL used by clients to reach this server (http:// or https://)",
			Validate:    server.ValidateServerAddr,
		})
		if err != nil {
			return err
		}
		initParams.ServerAddr = serverAddr
		initParams.AdminUsername, err = deps.UI.Input("Admin username", tui.InputOptions{
			Placeholder: "e.g. admin",
			Validate: func(s string) error {
				if strings.TrimSpace(s) == "" {
					return fmt.Errorf("admin username cannot be empty")
				}
				return nil
			},
		})
		if err != nil {
			return err
		}
		initParams.AdminPassword, err = deps.UI.Password("Admin password", tui.InputOptions{
			Validate: func(s string) error {
				if strings.TrimSpace(s) == "" {
					return fmt.Errorf("admin password cannot be empty")
				}
				return nil
			},
		})
		if err != nil {
			return err
		}
		initParams.AllowedPorts, err = deps.UI.Input("Allowed port ranges", tui.InputOptions{
			Placeholder: "e.g. 10000-11000",
			Description: "Comma-separated list of port ranges or single ports (e.g. 10000-11000,8080)",
			Default:     "10000-11000",
			Validate:    server.ValidateAllowedPorts,
		})
		if err != nil {
			return err
		}
	}

	deps.UI.PrintSummary("Installation summary", [][2]string{
		{"Role", "server"},
		{"Port", strconv.Itoa(port)},
		{"TLS mode", tlsMode},
		{"Server address", serverAddr},
		{"Trusted proxies", trustedProxies},
	})
	ok, err := deps.UI.Confirm("Proceed with installation?")
	if err != nil {
		return err
	}
	if !ok {
		printInstallCancelled(deps.UI)
		return nil
	}

	if state != svcmgr.StateHistoricalDataOnly {
		if err := deps.ApplyInit(svcmgr.ManagedDataDir, initParams); err != nil {
			return err
		}
	}
	if err := completeManagedInstall(svcmgr.RoleServer, managedInstallDeps{
		EnsureUser:        deps.EnsureUser,
		EnsureDirs:        deps.EnsureDirs,
		CurrentBinaryPath: deps.CurrentBinaryPath,
		InstallBinary:     deps.InstallBinary,
		DaemonReload:      deps.DaemonReload,
		EnableAndStart:    deps.EnableAndStart,
	}, func(spec svcmgr.ServiceSpec) error {
		spec.ListenPort = port
		spec.TLSMode = tlsMode
		spec.ServerURL = serverAddr
		if err := deps.WriteServerSpec(spec); err != nil {
			return err
		}
		if err := deps.WriteServerEnv(spec, svcmgr.ServerEnv{Port: port, TLSMode: tlsMode, TLSCert: tlsCert, TLSKey: tlsKey, TrustedProxies: trustedProxies, ServerAddr: serverAddr}); err != nil {
			return err
		}
		return deps.WriteServerUnit(spec)
	}); err != nil {
		return err
	}
	deps.UI.PrintSummary("Server installation complete", [][2]string{
		{"Status", "Running"},
		{"Panel URL", serverAddr},
		{"Next step", "Run netsgo manage to manage the service"},
	})
	return nil
}

func defaultServerDeps() serverDeps {
	return serverDeps{
		UI:      defaultUI{},
		Inspect: svcmgr.Inspect,
		Detect:  svcmgr.Detect,
		SelectTLSMode: func(ui uiProvider) (string, error) {
			index, err := ui.Select("TLS mode", []string{
				"off    — No TLS (recommended behind a reverse proxy)",
				"auto   — Auto-generate self-signed certificate (TOFU mode)",
				"custom — Use custom certificate files",
			})
			if err != nil {
				return "", err
			}
			return []string{"off", "auto", "custom"}[index], nil
		},
		LoadRecoverable: func() (server.InitParams, error) {
			return server.LoadRecoverableInitParams(svcmgr.ManagedDataDir)
		},
		EnsureUser:        svcmgr.EnsureUser,
		EnsureDirs:        ensureManagedServerDirs,
		ApplyInit:         server.ApplyInit,
		CurrentBinaryPath: svcmgr.CurrentBinaryPath,
		InstallBinary:     svcmgr.InstallBinary,
		WriteServerSpec:   svcmgr.WriteServerSpec,
		WriteServerEnv:    svcmgr.WriteServerEnv,
		WriteServerUnit:   svcmgr.WriteServerUnit,
		DaemonReload:      svcmgr.DaemonReload,
		EnableAndStart:    svcmgr.EnableAndStart,
	}
}

func ensureManagedServerDirs() error {
	if err := os.MkdirAll(svcmgr.ManagedDataDir+"/server", 0o750); err != nil {
		return err
	}
	if err := os.MkdirAll(svcmgr.ManagedDataDir+"/locks", 0o750); err != nil {
		return err
	}
	account, err := user.Lookup(svcmgr.SystemUser)
	if err != nil {
		return nil
	}
	uid, err := strconv.Atoi(account.Uid)
	if err != nil {
		return err
	}
	gid, err := strconv.Atoi(account.Gid)
	if err != nil {
		return err
	}
	serverDir := svcmgr.ManagedDataDir + "/server"
	if err := filepath.WalkDir(serverDir, func(path string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return os.Chown(path, uid, gid)
	}); err != nil {
		return err
	}
	return os.Chown(svcmgr.ManagedDataDir+"/locks", uid, gid)
}
