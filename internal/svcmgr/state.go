package svcmgr

import (
	"fmt"
	"os"
	"path/filepath"
)

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
	return InspectWithPaths(
		role,
		SystemdDir+"/"+UnitName(role),
		SpecPath(role),
		ServicesDir+"/"+string(role)+".env",
		ManagedDataDir+"/"+string(role),
	)
}

func Detect(role Role) InstallState {
	return Inspect(role).State
}

func DetectWithPaths(unitPath, specPath, envPath, dataDir string, isServer bool) InstallState {
	role := RoleClient
	if isServer {
		role = RoleServer
	}
	return InspectWithPaths(role, unitPath, specPath, envPath, dataDir).State
}

func InspectWithPaths(role Role, unitPath, specPath, envPath, dataDir string) InstallInspection {
	inspection := InstallInspection{Role: role}
	hasUnit := pathExists(unitPath)
	hasSpec := pathExists(specPath)
	hasEnv := pathExists(envPath)
	hasDataDir := dirExists(dataDir)

	if !hasUnit && !hasSpec && !hasEnv {
		switch {
		case !hasDataDir:
			inspection.State = StateNotInstalled
		case role == RoleServer && recoverableServerDataExists(dataDir):
			inspection.State = StateHistoricalDataOnly
			inspection.Problems = []string{"Recoverable server historical data was detected, but the managed service definition is missing"}
		default:
			inspection.State = StateBroken
			inspection.Problems = []string{fmt.Sprintf("leftover runtime data directory still exists: %s", dataDir)}
		}
		return inspection
	}

	problems := make([]string, 0, 8)
	if !hasUnit {
		problems = append(problems, fmt.Sprintf("missing unit file: %s", unitPath))
	}
	if !hasSpec {
		problems = append(problems, fmt.Sprintf("missing spec file: %s", specPath))
	}
	if !hasEnv {
		problems = append(problems, fmt.Sprintf("missing env file: %s", envPath))
	}
	if !hasDataDir {
		problems = append(problems, fmt.Sprintf("missing runtime data directory: %s", dataDir))
	}
	if len(problems) > 0 {
		inspection.State = StateBroken
		inspection.Problems = problems
		return inspection
	}

	spec, err := readSpec(specPath)
	if err != nil {
		inspection.State = StateBroken
		inspection.Problems = []string{fmt.Sprintf("failed to read spec file: %v", err)}
		return inspection
	}

	parentDataDir := filepath.Dir(dataDir)
	if spec.Role != role {
		problems = append(problems, fmt.Sprintf("spec role mismatch: got %q want %q", spec.Role, role))
	}
	if spec.ServiceName != "netsgo-"+string(role) {
		problems = append(problems, fmt.Sprintf("spec service name mismatch: got %q", spec.ServiceName))
	}
	if spec.UnitPath != unitPath {
		problems = append(problems, fmt.Sprintf("spec unit path mismatch: got %q want %q", spec.UnitPath, unitPath))
	}
	if spec.SpecPath != specPath {
		problems = append(problems, fmt.Sprintf("spec spec path mismatch: got %q want %q", spec.SpecPath, specPath))
	}
	if spec.EnvPath != envPath {
		problems = append(problems, fmt.Sprintf("spec env path mismatch: got %q want %q", spec.EnvPath, envPath))
	}
	if spec.DataDir != parentDataDir {
		problems = append(problems, fmt.Sprintf("spec data dir mismatch: got %q want %q", spec.DataDir, parentDataDir))
	}
	if spec.RunAsUser != SystemUser {
		problems = append(problems, fmt.Sprintf("spec run user mismatch: got %q want %q", spec.RunAsUser, SystemUser))
	}
	if spec.BinaryPath == "" {
		problems = append(problems, "spec binary path is empty")
	} else if !isBinaryInstalledAt(spec.BinaryPath) {
		problems = append(problems, fmt.Sprintf("binary is missing or not executable: %s", spec.BinaryPath))
	}
	if role == RoleServer && !recoverableServerDataExists(dataDir) {
		problems = append(problems, fmt.Sprintf("missing server initialization data: %s", filepath.Join(dataDir, "admin.json")))
	}

	envSpec := spec
	envSpec.EnvPath = envPath
	if role == RoleServer {
		if _, err := ReadServerEnv(envSpec); err != nil {
			problems = append(problems, fmt.Sprintf("failed to read server env: %v", err))
		}
	} else {
		if _, err := ReadClientEnv(envSpec); err != nil {
			problems = append(problems, fmt.Sprintf("failed to read client env: %v", err))
		}
	}

	execStart, err := ReadUnitExecStart(unitPath)
	if err != nil {
		problems = append(problems, fmt.Sprintf("failed to read unit ExecStart: %v", err))
	} else {
		expectedExecStart := expectedExecStart(role, spec.BinaryPath, spec.DataDir)
		if execStart != expectedExecStart {
			problems = append(problems, fmt.Sprintf("unit ExecStart mismatch: got %q want %q", execStart, expectedExecStart))
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

func recoverableServerDataExists(dataDir string) bool {
	return pathExists(dataDir + "/admin.json")
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
