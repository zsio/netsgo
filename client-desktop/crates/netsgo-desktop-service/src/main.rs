#![cfg_attr(all(windows, not(debug_assertions)), windows_subsystem = "windows")]

use anyhow::{Context, Result};
use netsgo_desktop_service::{host, manage, BUILD_VERSION};
use std::{
    path::PathBuf,
    sync::{
        atomic::{AtomicBool, Ordering},
        Arc,
    },
};

fn main() {
    if let Err(error) = run() {
        eprintln!("NetsGo 后台服务：{error:#}");
        std::process::exit(1);
    }
}
fn run() -> Result<()> {
    let args: Vec<String> = std::env::args().collect();
    if args.get(1).map(String::as_str) == Some("--version") {
        println!("{BUILD_VERSION}");
        return Ok(());
    }
    if let Some(index) = args.iter().position(|arg| arg == "--manage-request") {
        return manage::execute_request(std::path::Path::new(
            args.get(index + 1).context("缺少安装请求路径")?,
        ));
    }
    let index = args
        .iter()
        .position(|arg| arg == "--data-dir")
        .context("用法：netsgo-desktop-service --run --data-dir <目录>")?;
    let data = PathBuf::from(args.get(index + 1).context("缺少数据目录")?);
    #[cfg(windows)]
    if args.iter().any(|arg| arg == "--service") {
        return windows::dispatch(data);
    }
    if !args.iter().any(|arg| arg == "--run") {
        anyhow::bail!("请由系统服务管理器启动，或使用 --run 进行前台验证");
    }
    let stop = Arc::new(AtomicBool::new(false));
    let signal = stop.clone();
    ctrlc::set_handler(move || signal.store(true, Ordering::SeqCst))?;
    host::run(&data, stop)
}

#[cfg(windows)]
mod windows {
    use super::*;
    use std::{ffi::OsString, sync::OnceLock, time::Duration};
    use windows_service::{
        define_windows_service,
        service::{
            ServiceControl, ServiceControlAccept, ServiceExitCode, ServiceState, ServiceStatus,
            ServiceType,
        },
        service_control_handler::{self, ServiceControlHandlerResult},
        service_dispatcher,
    };
    static DATA: OnceLock<PathBuf> = OnceLock::new();
    define_windows_service!(ffi_main, service_main);
    pub fn dispatch(data: PathBuf) -> Result<()> {
        let _ = DATA.set(data);
        service_dispatcher::start(netsgo_desktop_service::SERVICE_NAME, ffi_main)?;
        Ok(())
    }
    fn service_main(_: Vec<OsString>) {
        let _ = run_service();
    }
    fn run_service() -> Result<()> {
        let stop = Arc::new(AtomicBool::new(false));
        let signal = stop.clone();
        let handle = service_control_handler::register(
            netsgo_desktop_service::SERVICE_NAME,
            move |event| match event {
                ServiceControl::Stop | ServiceControl::Shutdown => {
                    signal.store(true, Ordering::SeqCst);
                    ServiceControlHandlerResult::NoError
                }
                ServiceControl::Interrogate => ServiceControlHandlerResult::NoError,
                _ => ServiceControlHandlerResult::NotImplemented,
            },
        )?;
        let status = |current_state, code| ServiceStatus {
            service_type: ServiceType::OWN_PROCESS,
            current_state,
            controls_accepted: if current_state == ServiceState::Running {
                ServiceControlAccept::STOP | ServiceControlAccept::SHUTDOWN
            } else {
                ServiceControlAccept::empty()
            },
            exit_code: ServiceExitCode::Win32(code),
            checkpoint: 0,
            wait_hint: Duration::from_secs(0),
            process_id: None,
        };
        handle.set_service_status(status(ServiceState::Running, 0))?;
        let result = host::run(DATA.get().context("缺少后台服务数据目录")?, stop);
        handle.set_service_status(status(
            ServiceState::Stopped,
            if result.is_ok() { 0 } else { 1 },
        ))?;
        result
    }
}
