use anyhow::{Context, Result};
use serde::{Deserialize, Serialize};
use std::{
    fs,
    io::Write,
    path::{Path, PathBuf},
};

pub const BINDING_FILE: &str = "application.json";
pub const UNINSTALL_MARKER: &str = "uninstall-requested";

#[derive(Clone, Debug, Serialize, Deserialize)]
pub struct Installation {
    pub app_root: PathBuf,
    pub executable: PathBuf,
}

impl Installation {
    pub fn current() -> Result<Self> {
        let executable = std::env::current_exe()?;
        #[cfg(target_os = "macos")]
        let app_root = executable
            .ancestors()
            .find(|p| p.extension().is_some_and(|e| e == "app"))
            .context("请从完整的 NetsGo.app 运行")?
            .to_owned();
        #[cfg(not(target_os = "macos"))]
        let app_root = {
            let dir = executable.parent().context("无法确定应用目录")?;
            if dir.file_name().is_some_and(|n| n == "current") {
                dir.parent().unwrap_or(dir)
            } else {
                dir
            }
            .to_owned()
        };
        Ok(Self {
            app_root,
            executable,
        })
    }
    pub fn is_missing(&self) -> bool {
        // A denied/unmounted parent is not evidence of an uninstall.
        if !self.app_root.parent().is_some_and(Path::is_dir) {
            return false;
        }
        fs::metadata(&self.executable).is_err_and(|e| e.kind() == std::io::ErrorKind::NotFound)
    }
}

pub fn bind(data: &Path, installation: &Installation) -> Result<()> {
    if !installation.app_root.is_absolute()
        || !installation.executable.is_absolute()
        || !installation.executable.starts_with(&installation.app_root)
    {
        anyhow::bail!("应用安装位置无效");
    }
    let mut file = tempfile::NamedTempFile::new_in(data)?;
    file.write_all(&serde_json::to_vec(installation)?)?;
    file.as_file().sync_all()?;
    file.persist(data.join(BINDING_FILE)).map_err(|e| e.error)?;
    Ok(())
}

pub fn read(data: &Path) -> Result<Option<Installation>> {
    match fs::read(data.join(BINDING_FILE)) {
        Ok(bytes) => Ok(Some(serde_json::from_slice(&bytes)?)),
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => Ok(None),
        Err(e) => Err(e.into()),
    }
}

pub fn pause_reason(data: &Path) -> Option<String> {
    if data.join(UNINSTALL_MARKER).exists() {
        return Some("已请求卸载，后台连接已停止。请完成卸载，或修复后台服务以恢复。".into());
    }
    match read(data) {
        Ok(Some(installation)) if installation.is_missing() => Some(
            "应用已被移走或删除，后台连接已暂停。请恢复应用位置或使用卸载工具完成清理。".into(),
        ),
        Err(_) => Some("无法确认应用安装位置，后台连接已暂停。请修复后台服务。".into()),
        _ => None,
    }
}

pub fn request_stop(data: &Path) -> Result<()> {
    let mut file = tempfile::NamedTempFile::new_in(data)?;
    file.write_all(b"uninstall\n")?;
    file.persist(data.join(UNINSTALL_MARKER))
        .map_err(|e| e.error)?;
    Ok(())
}

pub fn clear_stop(data: &Path) -> Result<()> {
    match fs::remove_file(data.join(UNINSTALL_MARKER)) {
        Ok(()) => Ok(()),
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => Ok(()),
        Err(e) => Err(e.into()),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn removal_pauses_connections_and_atomic_replacement_can_resume() {
        let root = tempfile::tempdir().unwrap();
        let data = root.path().join("data");
        fs::create_dir(&data).unwrap();
        let app = root.path().join("NetsGo.app");
        fs::create_dir(&app).unwrap();
        let executable = app.join("netsgo-desktop");
        fs::write(&executable, b"app").unwrap();
        bind(
            &data,
            &Installation {
                app_root: app.clone(),
                executable,
            },
        )
        .unwrap();
        assert!(pause_reason(&data).is_none());
        fs::rename(&app, root.path().join("old.app")).unwrap();
        assert!(pause_reason(&data).is_some());
        fs::rename(root.path().join("old.app"), &app).unwrap();
        assert!(pause_reason(&data).is_none());
        request_stop(&data).unwrap();
        assert!(
            pause_reason(&data).is_some(),
            "uninstall intent survives a restored application"
        );
        clear_stop(&data).unwrap();
        assert!(pause_reason(&data).is_none());
    }
    #[test]
    fn legacy_services_and_unavailable_parent_are_not_mistaken_for_removal() {
        let root = tempfile::tempdir().unwrap();
        assert!(pause_reason(root.path()).is_none());
        let installation = Installation {
            app_root: root.path().join("unmounted/NetsGo.app"),
            executable: root.path().join("unmounted/NetsGo.app/ui"),
        };
        bind(root.path(), &installation).unwrap();
        assert!(pause_reason(root.path()).is_none());
    }
}
