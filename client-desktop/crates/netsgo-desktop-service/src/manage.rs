use crate::{
    ipc,
    platform::{self, Manifest, Owner},
};
use anyhow::{bail, Context, Result};
use netsgo_client_core::{config_file_path, DesktopConfig};
use serde::{Deserialize, Serialize};
use std::{
    fs,
    io::{Read, Write},
    path::{Path, PathBuf},
    thread,
    time::{Duration, Instant, SystemTime, UNIX_EPOCH},
};

pub const RESUME_FILE: &str = "agent.resume-once.json";
#[derive(Clone, Copy, Serialize, Deserialize, PartialEq, Eq)]
pub enum Operation {
    Install,
    Uninstall,
    Upgrade,
    Remove,
}
#[derive(Serialize, Deserialize)]
struct Request {
    operation: Operation,
    owner: Owner,
    user_root: PathBuf,
    source_bin: PathBuf,
    version: String,
    resume: Vec<String>,
    #[serde(default)]
    installation: Option<crate::presence::Installation>,
}
#[derive(Serialize, Deserialize)]
struct Response {
    error: Option<String>,
}

pub fn request(
    operation: Operation,
    user_root: &Path,
    source: Option<&Path>,
    resume: Vec<String>,
) -> Result<()> {
    if operation == Operation::Remove {
        return remove_request(user_root);
    }
    let _configuration = crate::uninstall::configuration_guard(user_root, true)?;
    let host_dir = platform::source_bin()?;
    let source_bin = source.unwrap_or(&host_dir).canonicalize()?;
    let version = String::from_utf8(
        platform::output(
            platform::command(source_bin.join(platform::binary_name("netsgo-desktop-service")))
                .arg("--version"),
        )?
        .stdout,
    )?
    .trim()
    .to_owned();
    if !version.starts_with('v') {
        bail!("请安装正式发布包后再启用系统后台服务");
    }
    let request = Request {
        operation,
        owner: platform::owner()?,
        user_root: user_root.canonicalize()?,
        source_bin,
        version,
        resume,
        installation: crate::presence::Installation::current().ok(),
    };
    run_request(
        &host_dir.join(platform::binary_name("netsgo-desktop-service")),
        &request,
    )
}

fn run_request(host: &Path, request: &Request) -> Result<()> {
    let temp = tempfile::tempdir()?;
    let file = temp.path().join("request.json");
    private_json(&file, request)?;
    elevate(host, &file).context("后台服务配置未完成；如取消了管理员授权，原运行方式会保留")?;
    let response: Response = serde_json::from_slice(
        &fs::read(temp.path().join("result.json")).context("系统安装器未返回结果")?,
    )?;
    if let Some(error) = response.error {
        bail!("{error}");
    }
    Ok(())
}

fn remove_request(user_root: &Path) -> Result<()> {
    // Run outside both the app and machine directories, so Windows can remove their executables.
    let original = crate::uninstall::helper_path()?;
    let temp = tempfile::tempdir()?;
    let host = temp
        .path()
        .join(platform::binary_name("netsgo-desktop-service"));
    fs::copy(&original, &host)?;
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        fs::set_permissions(&host, fs::Permissions::from_mode(0o755))?;
    }
    run_request(
        &host,
        &Request {
            operation: Operation::Remove,
            owner: platform::owner()?,
            user_root: user_root.canonicalize()?,
            source_bin: temp.path().to_owned(),
            version: crate::BUILD_VERSION.into(),
            resume: Vec::new(),
            installation: None,
        },
    )
}

#[cfg(target_os = "macos")]
fn shell_quote(value: &str) -> String {
    format!("'{}'", value.replace('\'', "'\\''"))
}
#[cfg(target_os = "macos")]
fn apple_quote(value: &str) -> String {
    format!("\"{}\"", value.replace('\\', "\\\\").replace('"', "\\\""))
}
#[cfg(windows)]
fn ps_quote(value: &str) -> String {
    format!("'{}'", value.replace('\'', "''"))
}
fn elevate(host: &Path, file: &Path) -> Result<()> {
    if host
        .to_string_lossy()
        .chars()
        .chain(file.to_string_lossy().chars())
        .any(char::is_control)
    {
        bail!("安装路径不能包含控制字符");
    }
    #[cfg(target_os = "macos")]
    {
        let command = format!(
            "{} --manage-request {}",
            shell_quote(&host.to_string_lossy()),
            shell_quote(&file.to_string_lossy())
        );
        platform::output(
            platform::command("/usr/bin/osascript")
                .arg("-e")
                .arg(format!(
                    "do shell script {} with administrator privileges",
                    apple_quote(&command)
                )),
        )?;
        return Ok(());
    }
    #[cfg(windows)]
    {
        let args = format!("--manage-request \"{}\"", file.display());
        let script = format!("$ErrorActionPreference='Stop'; $p=Start-Process -FilePath {} -ArgumentList {} -Verb RunAs -Wait -PassThru; exit $p.ExitCode", ps_quote(&host.to_string_lossy()), ps_quote(&args));
        platform::output(platform::command("powershell.exe").args([
            "-NoProfile",
            "-NonInteractive",
            "-Command",
            &script,
        ]))?;
        return Ok(());
    }
    #[cfg(not(any(target_os = "macos", windows)))]
    {
        bail!("当前系统暂不支持后台服务安装");
    }
}

pub fn execute_request(file: &Path) -> Result<()> {
    let mut request: Request = serde_json::from_slice(&fs::read(file)?)?;
    let result = (|| -> Result<()> {
        request.user_root = request.user_root.canonicalize()?;
        execute(&request)
    })();
    let response = Response {
        error: result.err().map(|e| format!("{e:#}")),
    };
    let response_path = file
        .parent()
        .context("安装请求路径无效")?
        .join("result.json");
    private_json(&response_path, &response)?;
    own_file(&response_path, &request.owner)?;
    // The caller reads the structured error, including failures after privilege elevation.
    Ok(())
}

fn execute(request: &Request) -> Result<()> {
    validate_request(request)?;
    let _configuration = if request.operation == Operation::Remove {
        None
    } else {
        Some(crate::uninstall::configuration_guard(
            &request.user_root,
            false,
        )?)
    };
    let root = platform::machine_root()?;
    let data = root.join("data");
    let previous = platform::read_manifest()?;
    if request.operation == Operation::Remove {
        return remove_installed_service(request, &root, previous);
    }
    if let Some(previous) = &previous {
        if previous.owner.id != request.owner.id {
            bail!("这台电脑的后台服务属于其他用户，请先由原用户停用");
        }
        if previous.enabled && previous.user_root != request.user_root {
            bail!("后台服务使用其他应用数据目录，请从原安装位置管理它");
        }
        if request.operation == Operation::Install && previous.enabled {
            bail!("后台服务已启用，请重新打开客户端以接管");
        }
    } else if request.operation != Operation::Install {
        bail!("尚未安装后台服务");
    }
    fs::create_dir_all(&root)?;
    platform::protect_root(&root)?;
    platform::stop_service()?;
    let backup = root.join(format!("bin-backup-{}", stamp()));
    let mut replaced_binary = false;
    let result = (|| -> Result<()> {
        if request.operation == Operation::Uninstall {
            copy_state(&data, &request.user_root, &request.owner, &root)?;
            platform::unregister_service()?;
            private_json(
                &root.join("service.json"),
                &Manifest {
                    owner: request.owner.clone(),
                    user_root: request.user_root.clone(),
                    enabled: false,
                    version: request.version.clone(),
                },
            )?;
            public_manifest(&root.join("service.json"))?;
            return Ok(());
        }
        let staging = tempfile::tempdir_in(&root)?;
        for name in ["netsgo-desktop-service", "netsgo"] {
            let name = platform::binary_name(name);
            copy_regular(&request.source_bin.join(&name), &staging.path().join(name))?;
        }
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            for entry in fs::read_dir(staging.path())? {
                fs::set_permissions(entry?.path(), fs::Permissions::from_mode(0o755))?;
            }
            fs::set_permissions(staging.path(), fs::Permissions::from_mode(0o755))?;
        }
        if root.join("bin").exists() {
            fs::rename(root.join("bin"), &backup)?;
        }
        if let Err(error) = fs::rename(staging.path(), root.join("bin")) {
            if backup.exists() {
                let _ = fs::rename(&backup, root.join("bin"));
            }
            return Err(error.into());
        }
        replaced_binary = true;
        if request.operation == Operation::Install {
            copy_state(&request.user_root, &data, &request.owner, &root)?;
        }
        fs::create_dir_all(&data)?;
        if let Some(installation) = &request.installation {
            crate::presence::bind(&data, installation)?;
            own_file(&data.join(crate::presence::BINDING_FILE), &request.owner)?;
        }
        crate::presence::clear_stop(&data)?;
        private_json(&data.join(RESUME_FILE), &request.resume)?;
        own_file(&data.join(RESUME_FILE), &request.owner)?;
        private_json(
            &root.join("service.json"),
            &Manifest {
                owner: request.owner.clone(),
                user_root: request.user_root.clone(),
                enabled: true,
                version: request.version.clone(),
            },
        )?;
        public_manifest(&root.join("service.json"))?;
        platform::register_service(&root, &request.owner)?;
        wait_for_version(&data, &request.version)?;
        Ok(())
    })();
    if let Err(error) = result {
        let recovery = (|| -> Result<()> {
            platform::stop_service()?;
            if replaced_binary {
                fs::remove_dir_all(root.join("bin"))?;
                if backup.exists() {
                    fs::rename(&backup, root.join("bin"))?;
                }
            }
            if let Some(previous) = previous {
                private_json(&root.join("service.json"), &previous)?;
                public_manifest(&root.join("service.json"))?;
                if previous.enabled {
                    private_json(&data.join(RESUME_FILE), &request.resume)?;
                    own_file(&data.join(RESUME_FILE), &request.owner)?;
                    platform::register_service(&root, &previous.owner)?;
                    wait_for_version(&data, &previous.version)?;
                }
            } else {
                platform::unregister_service()?;
                if root.join("service.json").exists() {
                    fs::remove_file(root.join("service.json"))?;
                }
            }
            Ok(())
        })();
        if let Err(recovery_error) = recovery {
            return Err(error.context(format!("恢复原后台服务也未完成：{recovery_error:#}")));
        }
        return Err(error);
    }
    if backup.exists() {
        let _ = fs::remove_dir_all(backup);
    }
    Ok(())
}

fn wait_for_version(data: &Path, version: &str) -> Result<()> {
    let deadline = Instant::now() + Duration::from_secs(25);
    loop {
        if ipc::call(data, ipc::Command::State).is_ok_and(|reply| reply.version == version) {
            return Ok(());
        }
        if Instant::now() >= deadline {
            bail!("后台服务未能正常启动");
        }
        thread::sleep(Duration::from_millis(250));
    }
}

fn validate_request(request: &Request) -> Result<()> {
    if request
        .user_root
        .file_name()
        .is_none_or(|name| name != "netsgo-desktop")
        || !request.user_root.is_absolute()
    {
        bail!("系统服务只支持标准 netsgo-desktop 数据目录");
    }
    if fs::symlink_metadata(&request.user_root)?
        .file_type()
        .is_symlink()
    {
        bail!("应用数据目录不能是符号链接");
    }
    #[cfg(unix)]
    {
        use std::os::unix::fs::MetadataExt;
        if unsafe { libc::geteuid() } != 0 {
            bail!("安装系统服务需要管理员授权");
        }
        let uid: u32 = request.owner.id.parse()?;
        if uid == 0 || fs::metadata(&request.user_root)?.uid() != uid {
            bail!("配置所有者与应用数据目录不一致");
        }
        let actual = String::from_utf8(
            platform::output(platform::command("/usr/bin/id").args(["-u", &request.owner.name]))?
                .stdout,
        )?;
        if actual.trim() != request.owner.id {
            bail!("用户身份校验失败");
        }
    }
    if request.operation == Operation::Remove {
        return Ok(());
    }
    for name in ["netsgo-desktop-service", "netsgo"] {
        let metadata = fs::symlink_metadata(request.source_bin.join(platform::binary_name(name)))?;
        if !metadata.is_file() || metadata.file_type().is_symlink() {
            bail!("后台服务程序文件无效");
        }
    }
    Ok(())
}

fn remove_installed_service(
    request: &Request,
    root: &Path,
    previous: Option<Manifest>,
) -> Result<()> {
    let Some(mut manifest) = previous else {
        if root.exists() {
            bail!("后台目录缺少所有者记录，请保留应用并联系维护者清理");
        }
        return Ok(());
    };
    if manifest.owner.id != request.owner.id || manifest.user_root != request.user_root {
        bail!("后台服务属于其他用户或数据目录，未执行删除");
    }
    let metadata = fs::symlink_metadata(root)?;
    if !metadata.is_dir() || metadata.file_type().is_symlink() {
        bail!("后台程序目录无效");
    }
    #[cfg(windows)]
    {
        use std::os::windows::fs::MetadataExt;
        if metadata.file_attributes() & 0x400 != 0 {
            bail!("后台程序目录不能是重解析点");
        }
    }
    platform::stop_service()?;
    if manifest.enabled {
        copy_state(&root.join("data"), &request.user_root, &request.owner, root)
            .context("保留连接配置失败，已保留应用和后台文件，请重试卸载")?;
    }
    platform::unregister_service()?;
    manifest.enabled = false;
    private_json(&root.join("service.json"), &manifest)?;
    public_manifest(&root.join("service.json"))?;
    // Keep the ownership record until all other files are gone, so a failed cleanup is retryable.
    for entry in fs::read_dir(root)? {
        let entry = entry?;
        if entry.file_name() == "service.json" {
            continue;
        }
        let metadata = fs::symlink_metadata(entry.path())?;
        if metadata.is_dir() && !metadata.file_type().is_symlink() {
            fs::remove_dir_all(entry.path())?;
        } else {
            fs::remove_file(entry.path())?;
        }
    }
    fs::remove_file(root.join("service.json"))?;
    fs::remove_dir(root)?;
    Ok(())
}

fn copy_state(from: &Path, to: &Path, owner: &Owner, staging_root: &Path) -> Result<()> {
    let raw = match fs::read(config_file_path(from)) {
        Ok(raw) => {
            let _: DesktopConfig = serde_json::from_slice(&raw)?;
            raw
        }
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => {
            serde_json::to_vec_pretty(&DesktopConfig::new_default())?
        }
        Err(error) => return Err(error.into()),
    };
    let stage = tempfile::tempdir_in(staging_root)?;
    platform::private_staging(stage.path()).context("保护配置迁移暂存目录")?;
    let profiles = stage.path().join("profiles");
    if from.join("profiles").exists() {
        copy_tree(&from.join("profiles"), &profiles).context("复制连接凭证到暂存目录")?;
    } else {
        fs::create_dir(&profiles)?;
    }
    let service_access = to == platform::data_root()?;
    platform::protect_data(stage.path(), owner, service_access).context("设置暂存凭证目录权限")?;
    fs::create_dir_all(to)?;
    let old_config = fs::read(config_file_path(to)).ok();
    let old_profiles = to.join(format!("profiles-backup-{}", stamp()));
    let had_profiles = to.join("profiles").exists();
    if had_profiles {
        fs::rename(to.join("profiles"), &old_profiles)?;
    }
    if let Err(error) = fs::rename(&profiles, to.join("profiles")) {
        if had_profiles {
            let _ = fs::rename(&old_profiles, to.join("profiles"));
        }
        return Err(error.into());
    }
    let result = (|| -> Result<()> {
        private_json_bytes(&config_file_path(to), &raw)?;
        own_file(&config_file_path(to), owner)?;
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            own_file(to, owner)?;
            fs::set_permissions(to, fs::Permissions::from_mode(0o700))?;
        }
        #[cfg(windows)]
        {
            platform::protect_data(to, owner, service_access).context("设置迁移后凭证目录权限")?;
        }
        Ok(())
    })();
    if let Err(error) = result {
        let _ = fs::remove_dir_all(to.join("profiles"));
        if had_profiles {
            let _ = fs::rename(&old_profiles, to.join("profiles"));
        }
        if let Some(bytes) = old_config {
            let _ = private_json_bytes(&config_file_path(to), &bytes);
            let _ = own_file(&config_file_path(to), owner);
        } else {
            let _ = fs::remove_file(config_file_path(to));
        }
        return Err(error);
    }
    Ok(())
}

fn copy_tree(from: &Path, to: &Path) -> Result<()> {
    if !fs::symlink_metadata(from)?.is_dir() || fs::symlink_metadata(from)?.file_type().is_symlink()
    {
        bail!("配置目录包含不支持的符号链接");
    }
    fs::create_dir(to)?;
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        fs::set_permissions(to, fs::Permissions::from_mode(0o700))?;
    }
    for entry in fs::read_dir(from)? {
        let entry = entry?;
        let metadata = fs::symlink_metadata(entry.path())?;
        if metadata.file_type().is_symlink() {
            bail!("配置目录包含不支持的符号链接");
        }
        if metadata.is_dir() {
            copy_tree(&entry.path(), &to.join(entry.file_name()))?;
        } else if metadata.is_file() {
            copy_regular(&entry.path(), &to.join(entry.file_name()))?;
        } else {
            bail!("配置目录包含不支持的特殊文件");
        }
    }
    Ok(())
}
fn copy_regular(from: &Path, to: &Path) -> Result<()> {
    let metadata = fs::symlink_metadata(from)?;
    if !metadata.is_file() || metadata.file_type().is_symlink() {
        bail!("不能复制符号链接或特殊文件");
    }
    let mut options = fs::OpenOptions::new();
    options.read(true);
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt;
        options.custom_flags(libc::O_NOFOLLOW);
    }
    let mut input = options.open(from)?;
    let mut output = fs::OpenOptions::new()
        .create_new(true)
        .write(true)
        .open(to)?;
    std::io::copy(&mut input, &mut output)?;
    output.sync_all()?;
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        fs::set_permissions(to, fs::Permissions::from_mode(0o600))?;
    }
    Ok(())
}
fn stamp() -> u128 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap_or_default()
        .as_nanos()
}
fn private_json<T: Serialize>(path: &Path, value: &T) -> Result<()> {
    private_json_bytes(path, &serde_json::to_vec(value)?)
}
fn private_json_bytes(path: &Path, bytes: &[u8]) -> Result<()> {
    let mut temp = tempfile::NamedTempFile::new_in(path.parent().context("无效的目标路径")?)?;
    temp.write_all(bytes)?;
    temp.as_file().sync_all()?;
    temp.persist(path).map_err(|e| e.error)?;
    Ok(())
}
fn own_file(path: &Path, owner: &Owner) -> Result<()> {
    #[cfg(unix)]
    {
        use std::os::unix::ffi::OsStrExt;
        let uid: u32 = owner.id.parse()?;
        let name = std::ffi::CString::new(path.as_os_str().as_bytes())?;
        if unsafe { libc::lchown(name.as_ptr(), uid, owner.gid) } != 0 {
            return Err(std::io::Error::last_os_error().into());
        }
    }
    #[cfg(windows)]
    {
        let _ = (path, owner);
    }
    Ok(())
}
fn public_manifest(path: &Path) -> Result<()> {
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        fs::set_permissions(path, fs::Permissions::from_mode(0o644))?;
    }
    #[cfg(windows)]
    let _ = path;
    Ok(())
}

pub fn unpack_binaries(package: &Path) -> Result<tempfile::TempDir> {
    let mut archive = zip::ZipArchive::new(fs::File::open(package)?)?;
    let directory = tempfile::tempdir()?;
    for binary in ["netsgo-desktop-service", "netsgo"] {
        let expected = platform::binary_name(binary);
        let mut found = false;
        for index in 0..archive.len() {
            let mut file = archive.by_index(index)?;
            if file.name().rsplit('/').next() != Some(expected.as_str()) {
                continue;
            }
            if found
                || file.size() > 128 * 1024 * 1024
                || file
                    .unix_mode()
                    .is_some_and(|mode| mode & 0o170000 == 0o120000)
            {
                bail!("更新包中的后台程序文件无效");
            }
            let mut output = fs::OpenOptions::new()
                .create_new(true)
                .write(true)
                .open(directory.path().join(&expected))?;
            std::io::copy(&mut file.by_ref().take(128 * 1024 * 1024 + 1), &mut output)?;
            #[cfg(unix)]
            {
                use std::os::unix::fs::PermissionsExt;
                fs::set_permissions(
                    directory.path().join(&expected),
                    fs::Permissions::from_mode(0o755),
                )?;
            }
            found = true;
        }
        if !found {
            bail!("此更新包缺少后台服务程序，请下载支持后台运行的完整安装包");
        }
    }
    Ok(directory)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn migration_preserves_sqlite_bytes_and_keeps_previous_local_credentials() {
        let root = tempfile::tempdir().unwrap();
        let source = root.path().join("source");
        let target = root.path().join("netsgo-desktop");
        fs::create_dir_all(source.join("profiles/server/client")).unwrap();
        fs::create_dir_all(target.join("profiles/old/client")).unwrap();
        let database = b"opaque sqlite identity and token bytes";
        fs::write(source.join("profiles/server/client/netsgo.db"), database).unwrap();
        fs::write(
            target.join("profiles/old/client/netsgo.db"),
            b"previous credentials",
        )
        .unwrap();
        private_json(&config_file_path(&source), &DesktopConfig::new_default()).unwrap();
        copy_state(&source, &target, &platform::owner().unwrap(), root.path()).unwrap();
        let migrated = fs::read(target.join("profiles/server/client/netsgo.db"));
        #[cfg(windows)]
        if migrated.is_err() {
            eprintln!("owner: {}", platform::owner().unwrap().id);
            eprintln!(
                "{}",
                String::from_utf8_lossy(
                    &platform::command("icacls.exe")
                        .arg(&target)
                        .arg("/T")
                        .output()
                        .unwrap()
                        .stdout
                )
            );
        }
        assert_eq!(migrated.unwrap(), database);
        let backup = fs::read_dir(&target)
            .unwrap()
            .map(|entry| entry.unwrap().path())
            .find(|path| {
                path.file_name()
                    .unwrap()
                    .to_string_lossy()
                    .starts_with("profiles-backup-")
            })
            .unwrap();
        assert_eq!(
            fs::read(backup.join("old/client/netsgo.db")).unwrap(),
            b"previous credentials"
        );
    }

    #[cfg(unix)]
    #[test]
    fn migration_rejects_symlinked_credentials() {
        let root = tempfile::tempdir().unwrap();
        let source = root.path().join("source");
        let target = root.path().join("target");
        fs::create_dir(&source).unwrap();
        fs::write(root.path().join("secret"), b"must not be copied").unwrap();
        std::os::unix::fs::symlink(root.path().join("secret"), source.join("netsgo.db")).unwrap();
        assert!(copy_tree(&source, &target).is_err());
        assert!(!target.join("netsgo.db").exists());
    }

    #[test]
    fn update_extracts_only_one_complete_pair_of_service_binaries() {
        use zip::{write::SimpleFileOptions, ZipWriter};
        let root = tempfile::tempdir().unwrap();
        let package = root.path().join("full.nupkg");
        let mut writer = ZipWriter::new(fs::File::create(&package).unwrap());
        for binary in ["netsgo-desktop-service", "netsgo"] {
            writer
                .start_file(
                    format!("lib/native/{}", platform::binary_name(binary)),
                    SimpleFileOptions::default(),
                )
                .unwrap();
            writer.write_all(binary.as_bytes()).unwrap();
        }
        writer
            .start_file("../../outside", SimpleFileOptions::default())
            .unwrap();
        writer.write_all(b"ignored").unwrap();
        writer.finish().unwrap();
        let extracted = unpack_binaries(&package).unwrap();
        assert_eq!(fs::read_dir(extracted.path()).unwrap().count(), 2);
        assert_eq!(
            fs::read(extracted.path().join(platform::binary_name("netsgo"))).unwrap(),
            b"netsgo"
        );
        assert!(!root.path().join("outside").exists());
        let invalid = root.path().join("invalid.nupkg");
        let mut writer = ZipWriter::new(fs::File::create(&invalid).unwrap());
        writer
            .start_file("anything", SimpleFileOptions::default())
            .unwrap();
        writer.finish().unwrap();
        assert!(unpack_binaries(&invalid).is_err());
    }
}
