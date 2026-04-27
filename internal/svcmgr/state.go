package svcmgr

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

const serverDBFileName = "netsgo.db"

type InstallState int

const (
	StateNotInstalled InstallState = iota
	StateInstalled
	StateHistoricalDataOnly
	StateBroken
)

type InstallInspection struct {
	Role     Role
	State    InstallState
	Problems []string
}

func (s InstallState) String() string {
	switch s {
	case StateNotInstalled:
		return "not-installed"
	case StateInstalled:
		return "installed"
	case StateHistoricalDataOnly:
		return "historical-data-only"
	case StateBroken:
		return "broken"
	default:
		return fmt.Sprintf("InstallState(%d)", int(s))
	}
}

func Inspect(role Role) InstallInspection {
	return InspectWithLayout(NewLayout(role))
}

func Detect(role Role) InstallState {
	return Inspect(role).State
}

func DetectWithLayout(layout ServiceLayout) InstallState {
	return InspectWithLayout(layout).State
}

func InspectWithLayout(layout ServiceLayout) InstallInspection {
	inspection := InstallInspection{Role: layout.Role}
	hasUnit := pathExists(layout.UnitPath)
	hasEnv := pathExists(layout.EnvPath)
	hasRuntimeDir := dirExists(layout.RuntimeDir)

	if !hasUnit && !hasEnv {
		switch {
		case !hasRuntimeDir:
			inspection.State = StateNotInstalled
		case layout.Role == RoleServer:
			initialized, err := recoverableServerDataExists(layout.RuntimeDir)
			if err != nil {
				inspection.State = StateBroken
				inspection.Problems = []string{fmt.Sprintf("failed to inspect server data: %v", err)}
			} else if initialized {
				inspection.State = StateHistoricalDataOnly
				inspection.Problems = []string{"Recoverable server historical data was detected, but the managed service definition is missing"}
			} else {
				inspection.State = StateBroken
				inspection.Problems = []string{fmt.Sprintf("leftover runtime data directory still exists: %s", layout.RuntimeDir)}
			}
		default:
			inspection.State = StateBroken
			inspection.Problems = []string{fmt.Sprintf("leftover runtime data directory still exists: %s", layout.RuntimeDir)}
		}
		return inspection
	}

	problems := make([]string, 0, 8)
	if !hasUnit {
		problems = append(problems, fmt.Sprintf("missing unit file: %s", layout.UnitPath))
	}
	if !hasEnv {
		problems = append(problems, fmt.Sprintf("missing env file: %s", layout.EnvPath))
	}
	if !hasRuntimeDir {
		problems = append(problems, fmt.Sprintf("missing runtime data directory: %s", layout.RuntimeDir))
	}
	if len(problems) > 0 {
		inspection.State = StateBroken
		inspection.Problems = problems
		return inspection
	}

	if layout.BinaryPath == "" {
		problems = append(problems, "binary path is empty")
	} else if !isBinaryInstalledAt(layout.BinaryPath) {
		problems = append(problems, fmt.Sprintf("binary is missing or not executable: %s", layout.BinaryPath))
	}
	if layout.Role == RoleServer {
		initialized, err := recoverableServerDataExists(layout.RuntimeDir)
		if err != nil {
			problems = append(problems, fmt.Sprintf("failed to inspect server data: %v", err))
		} else if !initialized {
			problems = append(problems, fmt.Sprintf("missing server initialization data: %s", recoverableServerDataPath(layout.RuntimeDir)))
		}
	}

	if layout.Role == RoleServer {
		if _, err := ReadServerEnv(layout); err != nil {
			problems = append(problems, fmt.Sprintf("failed to read server env: %v", err))
		}
	} else {
		if _, err := ReadClientEnv(layout); err != nil {
			problems = append(problems, fmt.Sprintf("failed to read client env: %v", err))
		}
	}

	unitInfo, err := ReadUnitInfo(layout.UnitPath)
	if err != nil {
		problems = append(problems, fmt.Sprintf("failed to read unit file: %v", err))
	} else {
		if unitInfo.User != layout.RunAsUser {
			problems = append(problems, fmt.Sprintf("unit User mismatch: got %q want %q", unitInfo.User, layout.RunAsUser))
		}
		if unitInfo.Group != layout.RunAsGroup {
			problems = append(problems, fmt.Sprintf("unit Group mismatch: got %q want %q", unitInfo.Group, layout.RunAsGroup))
		}
		if unitInfo.EnvironmentFile != layout.EnvPath {
			problems = append(problems, fmt.Sprintf("unit EnvironmentFile mismatch: got %q want %q", unitInfo.EnvironmentFile, layout.EnvPath))
		}
		expectedExecStart := expectedExecStart(layout)
		if unitInfo.ExecStart != expectedExecStart {
			problems = append(problems, fmt.Sprintf("unit ExecStart mismatch: got %q want %q", unitInfo.ExecStart, expectedExecStart))
		}
	}

	if len(problems) > 0 {
		inspection.State = StateBroken
		inspection.Problems = problems
		return inspection
	}

	inspection.State = StateInstalled
	return inspection
}

func recoverableServerDataExists(dataDir string) (bool, error) {
	initialized, err := readServerDBInitialized(recoverableServerDataPath(dataDir))
	if err != nil {
		return false, err
	}
	return initialized, nil
}

func recoverableServerDataPath(dataDir string) string {
	return filepath.Join(dataDir, serverDBFileName)
}

func readServerDBInitialized(path string) (bool, error) {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.IsDir() {
		return false, fmt.Errorf("server sqlite path is a directory: %s", path)
	}

	db, err := sql.Open("sqlite", readOnlySQLiteDSN(path))
	if err != nil {
		return false, err
	}
	defer func() { _ = db.Close() }()

	hasConfig, err := sqliteFileTableExists(db, "server_config")
	if err != nil {
		return false, err
	}
	if !hasConfig {
		return false, nil
	}

	var initialized int
	err = db.QueryRow(`SELECT initialized FROM server_config WHERE id = 1`).Scan(&initialized)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return initialized != 0, nil
}

func sqliteFileTableExists(db *sql.DB, tableName string) (bool, error) {
	var name string
	err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, tableName).Scan(&name)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func readOnlySQLiteDSN(path string) string {
	u := url.URL{Scheme: "file", Path: path}
	q := u.Query()
	q.Set("mode", "ro")
	u.RawQuery = q.Encode()
	return u.String()
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
