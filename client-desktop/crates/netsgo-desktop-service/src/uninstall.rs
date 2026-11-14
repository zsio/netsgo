//! Uninstall owns service cleanup. It never relies on the user disabling startup switches.
use crate::{manage, platform, presence};
use anyhow::{bail, Context, Result};
use fs2::FileExt;
use netsgo_client_core::default_desktop_data_dir;
use std::{
    fs,
    path::{Path, PathBuf},
    process::Stdio,
    thread,
    time::{Duration, Instant},
};

pub const QUIT_REQUEST: &str = "desktop-uninstall.request";
#[cfg(windows)]
const PACK_ID: &str = "NetsGo.Desktop";

pub fn is_running(root: &Path) -> bool {
    fs::OpenOptions::new()
        .create(true)
        .truncate(false)
        .read(true)
        .write(true)
        .open(root.join("uninstall.lock"))
        .is_ok_and(|file| file.try_lock_exclusive().is_err())
}

pub fn helper_path() -> Result<PathBuf> {
    let exe = std::env::current_exe()?;
    if exe
        .file_stem()
        .is_some_and(|n| n == "netsgo-desktop-service" || n == "netsgo-uninstall")
    {
        return Ok(exe);
    }
    let adjacent = exe
        .parent()
        .context("无法确定卸载工具位置")?
        .join(platform::binary_name("netsgo-desktop-service"));
    if adjacent.is_file() {
        return Ok(adjacent);
    }
    let machine = platform::machine_root()?
        .join("bin")
        .join(platform::binary_name("netsgo-desktop-service"));
    if machine.is_file() {
        return Ok(machine);
    }
    bail!("找不到卸载工具，请重新下载完整安装包后再卸载")
}

pub fn is_uninstaller_bundle() -> bool {
    std::env::current_exe()
        .ok()
        .and_then(|p| p.file_stem().map(|n| n == "netsgo-uninstall"))
        .unwrap_or(false)
}

/// Copy before starting: the worker must survive deletion of the application directory.
pub fn launch(user_root: &Path, app_root: Option<&Path>, quiet: bool) -> Result<()> {
    let source = helper_path()?;
    let temp = tempfile::Builder::new()
        .prefix("netsgo-uninstall-")
        .tempdir()?;
    let executable = temp.path().join(platform::binary_name("netsgo-uninstall"));
    fs::copy(source, &executable)?;
    fs::write(
        temp.path().join(".netsgo-uninstall-worker"),
        b"netsgo-owned-temp\n",
    )?;
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        fs::set_permissions(&executable, fs::Permissions::from_mode(0o755))?;
    }
    let mut command = platform::command(&executable);
    command
        .arg("--uninstall-app")
        .arg("--worker")
        .arg("--data-dir")
        .arg(user_root);
    if let Some(path) = app_root {
        command.arg("--app-path").arg(path);
    }
    if quiet {
        command.arg("--quiet");
    }
    command
        .stdin(Stdio::null())
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .spawn()?;
    let _ = temp.keep();
    Ok(())
}

pub fn cli(args: &[String]) -> Result<()> {
    let value = |key: &str| {
        args.windows(2)
            .find(|pair| pair[0] == key)
            .map(|pair| PathBuf::from(&pair[1]))
    };
    let user_root = value("--data-dir").unwrap_or_else(|| {
        platform::read_manifest()
            .ok()
            .flatten()
            .filter(|manifest| platform::owner().is_ok_and(|owner| owner.id == manifest.owner.id))
            .map(|manifest| manifest.user_root)
            .unwrap_or_else(default_desktop_data_dir)
    });
    fs::create_dir_all(&user_root)?;
    let quiet = args.iter().any(|arg| arg == "--quiet");
    let app_root = value("--app-path").or_else(|| discover_app_root(&user_root).ok());
    if !args.iter().any(|arg| arg == "--worker") {
        return launch(&user_root, app_root.as_deref(), quiet);
    }
    let result = run(&user_root, app_root.as_deref(), quiet);
    if let Err(error) = &result {
        if !quiet {
            show_message(&format!(
                "卸载未完成，应用已保留。\n\n{error:#}\n\n请重新打开应用或重新运行卸载工具。"
            ));
        }
    }
    schedule_worker_cleanup();
    result.map(|_| ())
}

fn discover_app_root(user_root: &Path) -> Result<PathBuf> {
    let managed = platform::owned_manifest(user_root)?.is_some();
    if managed {
        if let Some(installation) = presence::read(&platform::data_root()?)? {
            return Ok(installation.app_root);
        }
    }
    let installation = presence::Installation::current()?;
    #[cfg(target_os = "macos")]
    {
        if installation
            .app_root
            .file_name()
            .is_some_and(|n| n == "NetsGo.app")
        {
            return Ok(installation.app_root);
        }
        for root in [
            PathBuf::from("/Applications/NetsGo.app"),
            dirs::home_dir()
                .context("无法读取用户目录")?
                .join("Applications/NetsGo.app"),
        ] {
            if root.is_dir() {
                return Ok(root);
            }
        }
        if managed {
            return Ok(PathBuf::from("/Applications/NetsGo.app"));
        }
        bail!("无法确定 NetsGo.app 的安装位置，请使用应用设置里的卸载入口")
    }
    #[cfg(not(target_os = "macos"))]
    {
        Ok(installation.app_root)
    }
}

fn validate_app(root: &Path) -> Result<()> {
    if !root.is_absolute() || root.parent().is_none() {
        bail!("应用安装位置无效");
    }
    if !root.exists() {
        return Ok(());
    }
    let metadata = fs::symlink_metadata(root)?;
    if !metadata.is_dir() || metadata.file_type().is_symlink() {
        bail!("应用目录不能是符号链接");
    }
    #[cfg(windows)]
    {
        use std::os::windows::fs::MetadataExt;
        if metadata.file_attributes() & 0x400 != 0 {
            bail!("应用目录不能是重解析点");
        }
        let manifest = fs::read_to_string(root.join("current/sq.version")).context(
            "请从 Windows 设置中卸载已安装的 NetsGo；便携版请先用卸载工具清理后台，再删除程序文件",
        )?;
        if !manifest.contains(&format!("<id>{PACK_ID}</id>")) || !root.join("Update.exe").is_file()
        {
            bail!("不是 NetsGo 的安装目录");
        }
    }
    #[cfg(target_os = "macos")]
    {
        let plist = platform::output(
            platform::command("/usr/bin/plutil")
                .args(["-extract", "CFBundleIdentifier", "raw", "-o", "-"])
                .arg(root.join("Contents/Info.plist")),
        )?;
        if String::from_utf8_lossy(&plist.stdout).trim() != "uy.zs.netsgo.desktop" {
            bail!("不是 NetsGo 的应用包，未执行删除");
        }
    }
    Ok(())
}

fn run(user_root: &Path, app_root: Option<&Path>, quiet: bool) -> Result<bool> {
    let app_root = app_root.context("无法确定应用安装位置，未执行卸载")?;
    validate_app(app_root)?;
    fs::create_dir_all(user_root)?;
    let lock = fs::OpenOptions::new()
        .create(true)
        .truncate(false)
        .read(true)
        .write(true)
        .open(user_root.join("uninstall.lock"))?;
    lock.try_lock_exclusive()
        .context("另一个卸载任务正在执行，请稍候")?;
    if !quiet && !confirm_uninstall()? {
        return Ok(false);
    }
    let result = prepare(user_root).and_then(|_| remove_application(app_root));
    if result.is_err() {
        // An authorization cancellation keeps the app; a live service can resume safely.
        if platform::owned_manifest(user_root).ok().flatten().is_some() {
            let _ = presence::clear_stop(&platform::data_root()?);
        }
    }
    result?;
    Ok(true)
}

pub fn prepare(user_root: &Path) -> Result<()> {
    // Check ownership before touching another user's machine service.
    let manifest = platform::read_manifest()?;
    if let Some(manifest) = &manifest {
        let owner = platform::owner()?;
        if manifest.owner.id != owner.id || manifest.user_root != user_root.canonicalize()? {
            bail!("后台服务属于其他用户或数据目录，请由原用户完成卸载");
        }
        if platform::data_root()?.is_dir() {
            presence::request_stop(&platform::data_root()?)?;
        }
    }
    request_gui_exit(user_root)?;
    if manifest.is_some() {
        manage::request(manage::Operation::Remove, user_root, None, Vec::new())?;
    }
    platform::set_login_enabled(false)?;
    Ok(())
}

fn request_gui_exit(root: &Path) -> Result<()> {
    let file = fs::OpenOptions::new()
        .create(true)
        .truncate(false)
        .read(true)
        .write(true)
        .open(root.join("desktop-ui.lock"))?;
    if file.try_lock_exclusive().is_ok() {
        return Ok(());
    }
    fs::write(root.join(QUIT_REQUEST), b"uninstall")?;
    let deadline = Instant::now() + Duration::from_secs(20);
    while file.try_lock_exclusive().is_err() {
        if Instant::now() >= deadline {
            bail!("客户端尚未退出，请退出 NetsGo 后重试卸载；无需提前关闭自启动开关");
        }
        thread::sleep(Duration::from_millis(100));
    }
    Ok(())
}

#[cfg(target_os = "macos")]
fn remove_application(root: &Path) -> Result<()> {
    if !root.exists() {
        return Ok(());
    }
    use objc2_foundation::{NSFileManager, NSString, NSURL};
    let url =
        NSURL::fileURLWithPath_isDirectory(&NSString::from_str(&root.to_string_lossy()), true);
    NSFileManager::defaultManager()
        .trashItemAtURL_resultingItemURL_error(&url, None)
        .map_err(|error| anyhow::anyhow!("后台已清理，但应用无法移入废纸篓：{error}"))?;
    Ok(())
}

#[cfg(windows)]
fn remove_application(root: &Path) -> Result<()> {
    if !root.exists() {
        return Ok(());
    }
    let status = platform::command(root.join("Update.exe"))
        .args(["--uninstall", "--silent"])
        .status()?;
    if !status.success() {
        bail!("后台已清理，但应用卸载器返回 {status}，请重新运行卸载");
    }
    Ok(())
}
#[cfg(not(any(windows, target_os = "macos")))]
fn remove_application(_: &Path) -> Result<()> {
    bail!("当前平台不支持桌面卸载");
}

pub fn register_system_uninstaller() -> Result<()> {
    #[cfg(windows)]
    {
        use winreg::{
            enums::{HKEY_CURRENT_USER, KEY_WRITE},
            RegKey,
        };
        let exe = std::env::current_exe()?;
        let dir = exe.parent().context("无法确定安装位置")?;
        let Some(root) = dir
            .parent()
            .filter(|_| dir.file_name().is_some_and(|name| name == "current"))
        else {
            return Ok(());
        };
        let helper = dir.join("netsgo-uninstall.exe");
        if !helper.is_file() || !root.join("Update.exe").is_file() {
            return Ok(());
        }
        let path = format!("Software\\Microsoft\\Windows\\CurrentVersion\\Uninstall\\{PACK_ID}");
        let key = match RegKey::predef(HKEY_CURRENT_USER).open_subkey_with_flags(path, KEY_WRITE) {
            Ok(key) => key,
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => return Ok(()),
            Err(error) => return Err(error.into()),
        };
        let command = format!(
            "\"{}\" --uninstall-app --app-path \"{}\" --data-dir \"{}\"",
            helper.display(),
            root.display(),
            default_desktop_data_dir().display()
        );
        key.set_value("UninstallString", &command)?;
        key.set_value("QuietUninstallString", &format!("{command} --quiet"))?;
    }
    Ok(())
}

/// A direct Velopack invocation can bypass the registered wrapper. This callback must never show UI.
pub fn fast_uninstall_hook() {
    let root = default_desktop_data_dir();
    if platform::owned_manifest(&root)
        .ok()
        .flatten()
        .is_some_and(|m| m.enabled)
    {
        if let Ok(data) = platform::data_root() {
            let _ = presence::request_stop(&data);
            // beta.4 services do not understand the marker. Also disable their next-boot
            // reconnect and request a graceful stop through their existing IPC contract.
            let config = netsgo_client_core::config_file_path(&data);
            if let Ok(bytes) = fs::read(&config) {
                if let Ok(mut value) = serde_json::from_slice::<serde_json::Value>(&bytes) {
                    if let Some(profiles) = value.get_mut("profiles").and_then(|v| v.as_array_mut())
                    {
                        for profile in profiles {
                            if let Some(profile) = profile.as_object_mut() {
                                profile.insert("auto_connect".into(), false.into());
                            }
                        }
                        if let Ok(mut temp) = tempfile::NamedTempFile::new_in(&data) {
                            use std::io::Write;
                            if serde_json::to_writer(&mut temp, &value).is_ok() {
                                let _ = temp.flush();
                                let _ = temp.persist(&config);
                            }
                        }
                    }
                }
            }
            let _ = crate::ipc::call(&data, crate::ipc::Command::Disconnect(None));
        }
    }
    let _ = platform::set_login_enabled(false);
}

fn confirm_uninstall() -> Result<bool> {
    let message = "卸载 NetsGo？\n\n将停止隧道连接，移除自启动项和后台程序。连接配置与凭证会保留。";
    #[cfg(windows)]
    {
        return Ok(windows_message(message, true) == 1);
    }
    #[cfg(target_os = "macos")]
    {
        let script = format!("display dialog {} with title \"卸载 NetsGo\" buttons {{\"取消\", \"卸载\"}} default button \"取消\" cancel button \"取消\" with icon caution", apple_string(message));
        let result = platform::command("/usr/bin/osascript")
            .args(["-e", &script])
            .output()?;
        return Ok(result.status.success());
    }
    #[cfg(not(any(windows, target_os = "macos")))]
    {
        bail!("当前平台不支持桌面卸载");
    }
}

pub fn show_message(message: &str) {
    #[cfg(windows)]
    {
        windows_message(message, false);
    }
    #[cfg(target_os = "macos")]
    {
        let _ = platform::command("/usr/bin/osascript").args(["-e", &format!("display dialog {} with title \"NetsGo\" buttons {{\"好\"}} default button \"好\"", apple_string(message))]).output();
    }
}
#[cfg(target_os = "macos")]
fn apple_string(value: &str) -> String {
    format!("\"{}\"", value.replace('\\', "\\\\").replace('"', "\\\""))
}

#[cfg(windows)]
fn windows_message(message: &str, confirm: bool) -> i32 {
    #[link(name = "user32")]
    extern "system" {
        fn MessageBoxW(
            window: *mut std::ffi::c_void,
            text: *const u16,
            title: *const u16,
            flags: u32,
        ) -> i32;
    }
    let text: Vec<u16> = message.encode_utf16().chain(Some(0)).collect();
    let title: Vec<u16> = "NetsGo".encode_utf16().chain(Some(0)).collect();
    unsafe {
        MessageBoxW(
            std::ptr::null_mut(),
            text.as_ptr(),
            title.as_ptr(),
            if confirm { 0x31 } else { 0x10 },
        )
    }
}

fn schedule_worker_cleanup() {
    let Ok(exe) = std::env::current_exe() else {
        return;
    };
    let Some(directory) = exe.parent() else {
        return;
    };
    if !directory
        .file_name()
        .is_some_and(|name| name.to_string_lossy().starts_with("netsgo-uninstall-"))
    {
        return;
    }
    if fs::read(directory.join(".netsgo-uninstall-worker"))
        .ok()
        .as_deref()
        != Some(b"netsgo-owned-temp\n")
    {
        return;
    }
    #[cfg(unix)]
    {
        let _ = fs::remove_dir_all(directory);
    }
    #[cfg(windows)]
    {
        let path = directory.to_string_lossy().replace('\'', "''");
        let script = format!("Start-Sleep -Seconds 2; Remove-Item -LiteralPath '{path}' -Recurse -Force -ErrorAction SilentlyContinue");
        let _ = platform::command("powershell.exe")
            .args(["-NoProfile", "-NonInteractive", "-Command", &script])
            .stdin(Stdio::null())
            .stdout(Stdio::null())
            .stderr(Stdio::null())
            .spawn();
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn unrelated_directory_cannot_be_uninstalled() {
        let root = tempfile::tempdir().unwrap();
        fs::write(root.path().join("personal.txt"), b"keep").unwrap();
        assert!(validate_app(root.path()).is_err());
        assert_eq!(fs::read(root.path().join("personal.txt")).unwrap(), b"keep");
    }
    #[test]
    fn a_running_ui_must_release_its_data_before_cleanup() {
        let root = tempfile::tempdir().unwrap();
        let instance = crate::GuiInstance::claim(root.path(), false)
            .unwrap()
            .unwrap();
        let path = root.path().to_owned();
        let task = thread::spawn(move || request_gui_exit(&path));
        for _ in 0..100 {
            if root.path().join(QUIT_REQUEST).exists() {
                break;
            }
            thread::sleep(Duration::from_millis(10));
        }
        assert!(root.path().join(QUIT_REQUEST).exists());
        drop(instance);
        task.join().unwrap().unwrap();
    }
}
