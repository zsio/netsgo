use anyhow::{bail, Context, Result};
use serde::{Deserialize, Serialize};
use std::{
    fs,
    path::{Path, PathBuf},
    process::{Command, Output},
    thread,
    time::{Duration, Instant},
};

pub const SERVICE_NAME: &str = "NetsGoDesktop";
pub const DAEMON_LABEL: &str = "uy.zs.netsgo.desktop.service";
const LOGIN_LABEL: &str = "uy.zs.netsgo.desktop.login";

#[derive(Clone, Debug)]
pub struct StartupView {
    pub login_enabled: bool,
    pub boot_enabled: bool,
    pub busy: bool,
    pub service_version: Option<String>,
    pub service_error: Option<String>,
}

#[derive(Clone, Serialize, Deserialize)]
pub struct Owner {
    pub id: String,
    pub name: String,
    pub gid: u32,
}
#[derive(Clone, Serialize, Deserialize)]
pub struct Manifest {
    pub owner: Owner,
    pub user_root: PathBuf,
    pub enabled: bool,
    pub version: String,
}

pub fn command(program: impl AsRef<std::ffi::OsStr>) -> Command {
    #[allow(unused_mut)]
    let mut command = Command::new(program);
    #[cfg(windows)]
    {
        use std::os::windows::process::CommandExt;
        command.creation_flags(0x0800_0000);
    }
    command
}
pub fn output(command: &mut Command) -> Result<Output> {
    let result = command.output()?;
    if !result.status.success() {
        let detail = if result.stderr.is_empty() {
            &result.stdout
        } else {
            &result.stderr
        };
        bail!(
            "系统操作失败（{}）：{}",
            result.status,
            String::from_utf8_lossy(detail).trim()
        );
    }
    Ok(result)
}
pub fn owner() -> Result<Owner> {
    #[cfg(unix)]
    {
        let uid = unsafe { libc::geteuid() };
        if uid == 0 {
            bail!("请通过普通用户启动桌面程序，再按系统提示授权");
        }
        let name = String::from_utf8(output(command("/usr/bin/id").arg("-un"))?.stdout)?
            .trim()
            .to_owned();
        return Ok(Owner {
            id: uid.to_string(),
            name,
            gid: unsafe { libc::getegid() },
        });
    }
    #[cfg(windows)]
    {
        let text = String::from_utf8_lossy(
            &output(command("whoami.exe").args(["/user", "/fo", "csv", "/nh"]))?.stdout,
        )
        .to_string();
        let id = text
            .trim()
            .split(',')
            .next_back()
            .unwrap_or("")
            .trim_matches('"')
            .to_owned();
        if !id.starts_with("S-1-")
            || !id
                .bytes()
                .all(|b| b == b'S' || b == b'-' || b.is_ascii_digit())
        {
            bail!("无法识别当前 Windows 用户");
        }
        return Ok(Owner {
            id,
            name: std::env::var("USERNAME").unwrap_or_default(),
            gid: 0,
        });
    }
}
pub fn machine_root() -> Result<PathBuf> {
    #[cfg(target_os = "macos")]
    {
        return Ok(PathBuf::from("/Library/Application Support/NetsGoDesktop"));
    }
    #[cfg(windows)]
    {
        let result = output(command("powershell.exe").args([
            "-NoProfile",
            "-NonInteractive",
            "-Command",
            "[Environment]::GetFolderPath('CommonApplicationData')",
        ]))?;
        return Ok(PathBuf::from(String::from_utf8(result.stdout)?.trim()).join("NetsGoDesktop"));
    }
    #[cfg(not(any(target_os = "macos", windows)))]
    {
        bail!("当前系统暂不支持桌面启动管理");
    }
}
pub fn data_root() -> Result<PathBuf> {
    Ok(machine_root()?.join("data"))
}
pub fn read_manifest() -> Result<Option<Manifest>> {
    match fs::read(machine_root()?.join("service.json")) {
        Ok(bytes) => Ok(Some(
            serde_json::from_slice(&bytes).context("后台服务配置损坏")?,
        )),
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => Ok(None),
        Err(error) => Err(error.into()),
    }
}
pub fn owned_manifest(user_root: &Path) -> Result<Option<Manifest>> {
    let id = owner()?.id;
    let user_root = user_root.canonicalize()?;
    Ok(read_manifest()?
        .filter(|manifest| manifest.owner.id == id && manifest.user_root == user_root))
}
pub fn binary_name(name: &str) -> String {
    format!("{name}{}", if cfg!(windows) { ".exe" } else { "" })
}
pub fn source_bin() -> Result<PathBuf> {
    let exe = std::env::current_exe()?;
    let dir = exe.parent().context("无法确定安装目录")?;
    for name in ["netsgo", "netsgo-desktop-service"] {
        if !dir.join(binary_name(name)).is_file() {
            bail!("请从完整安装包运行 NetsGo 后再管理启动设置");
        }
    }
    Ok(dir.to_owned())
}

pub fn xml(value: &str) -> String {
    value
        .replace('&', "&amp;")
        .replace('<', "&lt;")
        .replace('>', "&gt;")
        .replace('"', "&quot;")
        .replace('\'', "&apos;")
}
#[cfg(target_os = "macos")]
fn app_bundle() -> Result<PathBuf> {
    std::env::current_exe()?
        .ancestors()
        .find(|path| path.extension().is_some_and(|ext| ext == "app"))
        .map(Path::to_owned)
        .context("请先将完整的 NetsGo.app 放入应用程序目录")
}

pub fn login_enabled() -> Result<bool> {
    #[cfg(target_os = "macos")]
    {
        let path = dirs::home_dir()
            .context("无法读取用户目录")?
            .join("Library/LaunchAgents")
            .join(format!("{LOGIN_LABEL}.plist"));
        return Ok(path.is_file());
    }
    #[cfg(windows)]
    {
        use winreg::{enums::HKEY_CURRENT_USER, RegKey};
        return Ok(RegKey::predef(HKEY_CURRENT_USER)
            .open_subkey("Software\\Microsoft\\Windows\\CurrentVersion\\Run")
            .and_then(|key| key.get_value::<String, _>("NetsGoDesktop"))
            .is_ok());
    }
    #[cfg(not(any(target_os = "macos", windows)))]
    {
        Ok(false)
    }
}
pub fn set_login_enabled(enabled: bool) -> Result<()> {
    #[cfg(target_os = "macos")]
    {
        let root = dirs::home_dir()
            .context("无法读取用户目录")?
            .join("Library/LaunchAgents");
        let path = root.join(format!("{LOGIN_LABEL}.plist"));
        if enabled {
            let app = app_bundle()?;
            let home_apps = dirs::home_dir()
                .context("无法读取用户目录")?
                .join("Applications");
            if !app.starts_with("/Applications") && !app.starts_with(home_apps) {
                bail!("请先将 NetsGo.app 移到应用程序目录，再开启登录自启动");
            }
            fs::create_dir_all(&root)?;
            fs::write(path, format!("<?xml version=\"1.0\"?><plist version=\"1.0\"><dict><key>Label</key><string>{LOGIN_LABEL}</string><key>RunAtLoad</key><true/><key>ProgramArguments</key><array><string>/usr/bin/open</string><string>-a</string><string>{}</string><string>--args</string><string>--netsgo-autostart</string></array></dict></plist>", xml(&app.to_string_lossy())))?;
            output(
                command("/bin/launchctl")
                    .args(["enable", &format!("gui/{}/{LOGIN_LABEL}", owner()?.id)]),
            )?;
        } else {
            let _ = command("/bin/launchctl")
                .args(["bootout", &format!("gui/{}/{LOGIN_LABEL}", owner()?.id)])
                .output();
            if path.exists() {
                fs::remove_file(path)?;
            }
        }
        return Ok(());
    }
    #[cfg(windows)]
    {
        use winreg::{enums::HKEY_CURRENT_USER, RegKey};
        let (key, _) = RegKey::predef(HKEY_CURRENT_USER)
            .create_subkey("Software\\Microsoft\\Windows\\CurrentVersion\\Run")?;
        if enabled {
            source_bin()?;
            key.set_value(
                "NetsGoDesktop",
                &format!(
                    "\"{}\" --netsgo-autostart",
                    std::env::current_exe()?.display()
                ),
            )?;
        } else {
            match key.delete_value("NetsGoDesktop") {
                Err(error) if error.kind() != std::io::ErrorKind::NotFound => {
                    return Err(error.into())
                }
                _ => {}
            }
        }
        return Ok(());
    }
    #[cfg(not(any(target_os = "macos", windows)))]
    {
        bail!("当前系统暂不支持登录自启动");
    }
}

pub fn daemon_plist(root: &Path, owner: &Owner) -> String {
    let host = xml(&root
        .join("bin")
        .join(binary_name("netsgo-desktop-service"))
        .to_string_lossy());
    let data = xml(&root.join("data").to_string_lossy());
    format!("<?xml version=\"1.0\"?><plist version=\"1.0\"><dict><key>Label</key><string>{DAEMON_LABEL}</string><key>UserName</key><string>{}</string><key>ProgramArguments</key><array><string>{host}</string><string>--run</string><string>--data-dir</string><string>{data}</string></array><key>RunAtLoad</key><true/><key>KeepAlive</key><true/><key>ThrottleInterval</key><integer>10</integer><key>ProcessType</key><string>Background</string><key>AssociatedBundleIdentifiers</key><array><string>uy.zs.netsgo.desktop</string></array></dict></plist>", xml(&owner.name))
}

pub fn stop_service() -> Result<()> {
    #[cfg(target_os = "macos")]
    {
        let _ = command("/bin/launchctl")
            .args(["bootout", &format!("system/{DAEMON_LABEL}")])
            .output();
        let start = Instant::now();
        while crate::ipc::endpoint_path(&data_root()?).exists() {
            if crate::ipc::call(&data_root()?, crate::ipc::Command::State).is_err() {
                // The owner process is already gone; a stale endpoint cannot lock SQLite.
                let endpoint: Option<crate::ipc::Endpoint> =
                    fs::read(crate::ipc::endpoint_path(&data_root()?))
                        .ok()
                        .and_then(|b| serde_json::from_slice(&b).ok());
                if endpoint.is_none_or(|e| unsafe { libc::kill(e.pid as i32, 0) } != 0) {
                    break;
                }
            }
            if start.elapsed() > Duration::from_secs(30) {
                bail!("后台服务尚未停止，请稍后重试");
            }
            thread::sleep(Duration::from_millis(200));
        }
        return Ok(());
    }
    #[cfg(windows)]
    {
        let _ = command("sc.exe").args(["stop", SERVICE_NAME]).output();
        let start = Instant::now();
        loop {
            let result = command("powershell.exe").args(["-NoProfile", "-NonInteractive", "-Command", "(Get-Service -Name NetsGoDesktop -ErrorAction SilentlyContinue).Status.ToString()"]).output()?;
            if !result.status.success()
                || String::from_utf8_lossy(&result.stdout).trim() == "Stopped"
            {
                break;
            }
            if start.elapsed() > Duration::from_secs(30) {
                bail!("后台服务尚未停止，请稍后重试");
            }
            thread::sleep(Duration::from_millis(500));
        }
        return Ok(());
    }
    #[cfg(not(any(target_os = "macos", windows)))]
    {
        bail!("当前系统暂不支持后台服务");
    }
}
pub fn register_service(root: &Path, owner: &Owner) -> Result<()> {
    #[cfg(target_os = "macos")]
    {
        let path = PathBuf::from(format!("/Library/LaunchDaemons/{DAEMON_LABEL}.plist"));
        fs::write(&path, daemon_plist(root, owner))?;
        use std::os::unix::fs::PermissionsExt;
        fs::set_permissions(&path, fs::Permissions::from_mode(0o644))?;
        output(command("/usr/sbin/chown").args(["root:wheel"]).arg(&path))?;
        output(command("/bin/launchctl").args(["enable", &format!("system/{DAEMON_LABEL}")]))?;
        output(
            command("/bin/launchctl")
                .args(["bootstrap", "system"])
                .arg(&path),
        )?;
        return Ok(());
    }
    #[cfg(windows)]
    {
        let binary = format!(
            "\"{}\" --service --data-dir \"{}\"",
            root.join("bin")
                .join(binary_name("netsgo-desktop-service"))
                .display(),
            root.join("data").display()
        );
        let exists = command("sc.exe")
            .args(["query", SERVICE_NAME])
            .output()?
            .status
            .success();
        let action = if exists { "config" } else { "create" };
        output(command("sc.exe").args([
            action,
            SERVICE_NAME,
            "binPath=",
            &binary,
            "start=",
            "auto",
            "obj=",
            "NT AUTHORITY\\LocalService",
            "DisplayName=",
            "NetsGo 后台连接",
        ]))?;
        output(command("sc.exe").args([
            "failure",
            SERVICE_NAME,
            "reset=",
            "86400",
            "actions=",
            "restart/5000/restart/15000/restart/30000",
        ]))?;
        output(command("sc.exe").args(["start", SERVICE_NAME]))?;
        return Ok(());
    }
    #[cfg(not(any(target_os = "macos", windows)))]
    {
        let _ = (root, owner);
        bail!("当前系统暂不支持后台服务");
    }
}
pub fn unregister_service() -> Result<()> {
    #[cfg(target_os = "macos")]
    {
        let path = PathBuf::from(format!("/Library/LaunchDaemons/{DAEMON_LABEL}.plist"));
        if path.exists() {
            fs::remove_file(path)?;
        }
        return Ok(());
    }
    #[cfg(windows)]
    {
        let result = command("sc.exe").args(["delete", SERVICE_NAME]).output()?;
        if !result.status.success() && result.status.code() != Some(1060) {
            bail!(
                "无法删除系统服务：{}",
                String::from_utf8_lossy(&result.stdout)
            );
        }
        return Ok(());
    }
    #[cfg(not(any(target_os = "macos", windows)))]
    {
        bail!("当前系统暂不支持后台服务");
    }
}

pub fn protect_root(root: &Path) -> Result<()> {
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        fs::set_permissions(root, fs::Permissions::from_mode(0o755))?;
    }
    #[cfg(windows)]
    {
        output(
            command("icacls.exe")
                .arg(root)
                .args(["/setowner", "*S-1-5-32-544"]),
        )?;
        output(command("icacls.exe").arg(root).args([
            "/inheritance:r",
            "/grant:r",
            "*S-1-5-18:(OI)(CI)F",
            "*S-1-5-32-544:(OI)(CI)F",
            "*S-1-5-32-545:(OI)(CI)RX",
            "*S-1-5-19:(OI)(CI)RX",
        ]))?;
    }
    Ok(())
}
pub fn private_staging(data: &Path) -> Result<()> {
    #[cfg(windows)]
    {
        output(command("icacls.exe").arg(data).args([
            "/inheritance:r",
            "/grant:r",
            "*S-1-5-18:(OI)(CI)F",
            "*S-1-5-32-544:(OI)(CI)F",
        ]))?;
    }
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        fs::set_permissions(data, fs::Permissions::from_mode(0o700))?;
    }
    Ok(())
}
// Only call this on a freshly copied tree, before making it writable by its owner.
pub fn protect_data(data: &Path, owner: &Owner, service_access: bool) -> Result<()> {
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        output(
            command("/usr/sbin/chown")
                .arg("-R")
                .arg(format!("{}:{}", owner.id, owner.gid))
                .arg(data),
        )?;
        fs::set_permissions(data, fs::Permissions::from_mode(0o700))?;
    }
    #[cfg(windows)]
    {
        output(command("icacls.exe").arg(data).args([
            "/inheritance:r",
            "/grant:r",
            "*S-1-5-18:(OI)(CI)F",
            "*S-1-5-32-544:(OI)(CI)F",
            "*S-1-5-19:(OI)(CI)M",
            &format!("*{}:(OI)(CI)M", owner.id),
            "/T",
        ]))?;
        if !service_access {
            output(
                command("icacls.exe")
                    .arg(data)
                    .args(["/remove:g", "*S-1-5-19", "/T"]),
            )?;
        }
    }
    #[cfg(unix)]
    {
        let _ = service_access;
    }
    Ok(())
}
