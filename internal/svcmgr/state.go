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
			inspection.Problems = []string{"检测到可恢复的 server 历史数据，但缺少受管服务定义"}
		default:
			inspection.State = StateBroken
			inspection.Problems = []string{fmt.Sprintf("残留运行数据目录仍存在: %s", dataDir)}
		}
		return inspection
	}

	problems := make([]string, 0, 8)
	if !hasUnit {
		problems = append(problems, fmt.Sprintf("缺少 unit 文件: %s", unitPath))
	}
	if !hasSpec {
		problems = append(problems, fmt.Sprintf("缺少 spec 文件: %s", specPath))
	}
	if !hasEnv {
		problems = append(problems, fmt.Sprintf("缺少 env 文件: %s", envPath))
	}
	if !hasDataDir {
		problems = append(problems, fmt.Sprintf("缺少运行数据目录: %s", dataDir))
	}
	if len(problems) > 0 {
		inspection.State = StateBroken
		inspection.Problems = problems
		return inspection
	}

	spec, err := readSpec(specPath)
	if err != nil {
		inspection.State = StateBroken
		inspection.Problems = []string{fmt.Sprintf("无法读取 spec 文件: %v", err)}
		return inspection
	}

	expectedDataDir := filepath.Dir(dataDir)
	if spec.Role != role {
		problems = append(problems, fmt.Sprintf("spec 角色不匹配: got %q want %q", spec.Role, role))
	}
	if spec.ServiceName != "netsgo-"+string(role) {
		problems = append(problems, fmt.Sprintf("spec 服务名不匹配: got %q", spec.ServiceName))
	}
	if spec.UnitPath != unitPath {
		problems = append(problems, fmt.Sprintf("spec unit 路径不匹配: got %q want %q", spec.UnitPath, unitPath))
	}
	if spec.SpecPath != specPath {
		problems = append(problems, fmt.Sprintf("spec spec 路径不匹配: got %q want %q", spec.SpecPath, specPath))
	}
	if spec.EnvPath != envPath {
		problems = append(problems, fmt.Sprintf("spec env 路径不匹配: got %q want %q", spec.EnvPath, envPath))
	}
	if spec.DataDir != expectedDataDir {
		problems = append(problems, fmt.Sprintf("spec data dir 不匹配: got %q want %q", spec.DataDir, expectedDataDir))
	}
	if spec.RunAsUser != SystemUser {
		problems = append(problems, fmt.Sprintf("spec 运行用户不匹配: got %q want %q", spec.RunAsUser, SystemUser))
	}
	if spec.BinaryPath == "" {
		problems = append(problems, "spec binary path 为空")
	} else if !isBinaryInstalledAt(spec.BinaryPath) {
		problems = append(problems, fmt.Sprintf("二进制不存在或不可执行: %s", spec.BinaryPath))
	}
	if role == RoleServer && !recoverableServerDataExists(dataDir) {
		problems = append(problems, fmt.Sprintf("缺少 server 初始化数据: %s", filepath.Join(dataDir, "admin.json")))
	}

	envSpec := spec
	envSpec.EnvPath = envPath
	if role == RoleServer {
		if _, err := ReadServerEnv(envSpec); err != nil {
			problems = append(problems, fmt.Sprintf("无法读取 server env: %v", err))
		}
	} else {
		if _, err := ReadClientEnv(envSpec); err != nil {
			problems = append(problems, fmt.Sprintf("无法读取 client env: %v", err))
		}
	}

	execStart, err := ReadUnitExecStart(unitPath)
	if err != nil {
		problems = append(problems, fmt.Sprintf("无法读取 unit ExecStart: %v", err))
	} else {
		expectedExecStart := expectedExecStart(role, spec.BinaryPath, spec.DataDir)
		if execStart != expectedExecStart {
			problems = append(problems, fmt.Sprintf("unit ExecStart 不匹配: got %q want %q", execStart, expectedExecStart))
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
