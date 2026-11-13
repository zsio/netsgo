use std::{
    path::PathBuf,
    sync::{mpsc, Arc, Mutex},
    thread,
    time::{Duration, Instant},
};

use anyhow::{bail, Context, Result};
use velopack::{
    locator::{auto_locate_app_manifest, LocationContext},
    UpdateInfo, UpdateManager, UpdateOptions,
};

use crate::{
    feed::{public_key, release_version, Release},
    transport::{sources, verify_file, Transport, VerifiedSource},
    APP_ID, BUILD_VERSION,
};

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum UpdateStatus {
    Development,
    Idle,
    Checking,
    UpToDate,
    Available,
    Downloading,
    Ready,
    Installing,
    Restarting,
    Failed,
}

impl UpdateStatus {
    pub fn busy(self) -> bool {
        matches!(
            self,
            Self::Checking | Self::Downloading | Self::Installing | Self::Restarting
        )
    }
}

#[derive(Clone, Debug)]
pub struct UpdateView {
    pub current_version: String,
    pub status: UpdateStatus,
    pub available_version: Option<String>,
    pub notes: String,
    pub progress: u8,
    pub error: Option<String>,
    pub downloaded: bool,
    pub can_install: bool,
}

#[derive(Clone)]
struct PreparedUpdate {
    manager: UpdateManager,
    info: UpdateInfo,
    path: PathBuf,
}

struct State {
    view: UpdateView,
    candidate: Option<Release>,
    prepared: Option<PreparedUpdate>,
    next_check: Instant,
}

/// One updater per application, independent of the number of server connections or windows.
#[derive(Clone)]
pub struct DesktopUpdater(Arc<Mutex<State>>);

impl Default for DesktopUpdater {
    fn default() -> Self {
        Self::new()
    }
}

impl DesktopUpdater {
    pub fn new() -> Self {
        let published = release_version(BUILD_VERSION).is_ok();
        let installed =
            auto_locate_app_manifest(LocationContext::FromCurrentExe).is_ok_and(|locator| {
                locator.get_manifest_id() == APP_ID
                    && format!("v{}", locator.get_manifest_version()) == BUILD_VERSION
            });
        Self(Arc::new(Mutex::new(State {
            view: UpdateView {
                current_version: BUILD_VERSION.into(),
                status: if published {
                    UpdateStatus::Idle
                } else {
                    UpdateStatus::Development
                },
                available_version: None,
                notes: String::new(),
                progress: 0,
                error: None,
                downloaded: false,
                can_install: installed,
            },
            candidate: None,
            prepared: None,
            next_check: Instant::now() + Duration::from_secs(8),
        })))
    }

    pub fn view(&self) -> UpdateView {
        self.0.lock().unwrap().view.clone()
    }

    pub fn tick(&self) {
        let due = {
            let state = self.0.lock().unwrap();
            state.view.status != UpdateStatus::Development
                && !state.view.status.busy()
                && !state.view.downloaded
                && Instant::now() >= state.next_check
        };
        if due {
            self.check();
        }
    }

    pub fn check(&self) {
        let Ok(current) = release_version(BUILD_VERSION) else {
            return;
        };
        {
            let mut state = self.0.lock().unwrap();
            if state.view.status.busy() || state.view.downloaded {
                return;
            }
            state.view.status = UpdateStatus::Checking;
            state.view.error = None;
        }
        let this = self.clone();
        thread::spawn(move || {
            let target = crate::target();
            let result = public_key().and_then(|key| {
                Transport::default().check(&sources(&target), &target, &current, &key)
            });
            let mut state = this.0.lock().unwrap();
            match result {
                Ok(candidate) => {
                    state.view.available_version = candidate.as_ref().map(|r| r.tag.clone());
                    state.view.notes = candidate
                        .as_ref()
                        .map(|r| r.asset.NotesMarkdown.clone())
                        .unwrap_or_default();
                    state.view.status = if candidate.is_some() {
                        UpdateStatus::Available
                    } else {
                        UpdateStatus::UpToDate
                    };
                    state.candidate = candidate;
                    state.next_check = Instant::now() + Duration::from_secs(24 * 60 * 60);
                }
                Err(error) => {
                    state.view.status = UpdateStatus::Failed;
                    state.view.error = Some(format!("{error:#}"));
                    state.next_check = Instant::now() + Duration::from_secs(5 * 60);
                }
            }
        });
    }

    pub fn download(&self) {
        let release = {
            let mut state = self.0.lock().unwrap();
            if state.view.status.busy() || state.view.downloaded || !state.view.can_install {
                return;
            }
            let Some(release) = state.candidate.clone() else {
                return;
            };
            state.view.status = UpdateStatus::Downloading;
            state.view.error = None;
            state.view.progress = 0;
            release
        };
        let this = self.clone();
        thread::spawn(move || {
            let (tx, rx) = mpsc::channel::<i16>();
            let progress = this.clone();
            let progress_thread = thread::spawn(move || {
                for percent in rx {
                    progress.0.lock().unwrap().view.progress = percent.clamp(0i16, 100) as u8;
                }
            });
            let result = prepare(release, tx);
            let _ = progress_thread.join();
            let mut state = this.0.lock().unwrap();
            match result {
                Ok(prepared) => {
                    state.prepared = Some(prepared);
                    state.view.downloaded = true;
                    state.view.status = UpdateStatus::Ready;
                    state.view.progress = 100;
                }
                Err(error) => {
                    state.view.status = UpdateStatus::Failed;
                    state.view.error = Some(format!("{error:#}"));
                    state.next_check = Instant::now() + Duration::from_secs(5 * 60);
                }
            }
        });
    }

    /// Arm the external installer first. The GUI shuts down sidecars and quits only after Restarting.
    pub fn install(&self, resume_profiles: Vec<String>) {
        let prepared = {
            let mut state = self.0.lock().unwrap();
            if state.view.status.busy() {
                return;
            }
            let Some(prepared) = state.prepared.clone() else {
                return;
            };
            state.view.status = UpdateStatus::Installing;
            state.view.error = None;
            prepared
        };
        let this = self.clone();
        thread::spawn(move || {
            let result = (|| -> Result<()> {
                // Recheck even cached packages immediately before execution.
                verify_file(&prepared.path, &prepared.info.TargetFullRelease)?;
                let args = vec![
                    "--netsgo-update-resume".to_owned(),
                    serde_json::to_string(&resume_profiles)?,
                ];
                prepared
                    .manager
                    .wait_exit_then_apply_updates(&prepared.info, false, true, args)?;
                Ok(())
            })();
            let mut state = this.0.lock().unwrap();
            match result {
                Ok(()) => state.view.status = UpdateStatus::Restarting,
                Err(error) => {
                    state.view.status = UpdateStatus::Failed;
                    state.view.error = Some(format!("无法安装更新：{error:#}"));
                    // Allow a fresh download if a cached package was corrupted after preparation.
                    state.prepared = None;
                    state.view.downloaded = false;
                    state.view.progress = 0;
                }
            }
        });
    }
}

fn prepare(release: Release, progress: mpsc::Sender<i16>) -> Result<PreparedUpdate> {
    let locator = auto_locate_app_manifest(LocationContext::FromCurrentExe)
        .context("请从正式安装包启动 NetsGo 后再更新")?;
    let path = locator.get_packages_dir().join(&release.asset.FileName);
    let manager = UpdateManager::new(
        VerifiedSource {
            release: release.clone(),
            http: Transport::default(),
        },
        Some(UpdateOptions {
            MaximumDeltasBeforeFallback: -1,
            ..Default::default()
        }),
        None,
    )?;
    if manager.get_app_id() != APP_ID
        || format!("v{}", manager.get_current_version()) != BUILD_VERSION
    {
        bail!("已安装的版本与程序版本不一致，请重新安装");
    }
    if release_version(&release.tag)? <= manager.get_current_version() {
        bail!("更新版本不能低于当前版本");
    }
    // Velopack skips verification for an existing cached file; repair that case before using it.
    if path.exists() && verify_file(&path, &release.asset).is_err() {
        std::fs::remove_file(&path)?;
    }
    let info = UpdateInfo {
        TargetFullRelease: release.asset,
        ..Default::default()
    };
    manager.download_updates(&info, Some(progress))?;
    verify_file(&path, &info.TargetFullRelease)?;
    Ok(PreparedUpdate {
        manager,
        info,
        path,
    })
}
