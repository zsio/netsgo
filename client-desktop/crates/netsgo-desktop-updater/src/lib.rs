mod feed;
mod runtime;
#[cfg(test)]
mod tests;
mod transport;

pub use runtime::{DesktopUpdater, UpdateStatus, UpdateView};

pub const BUILD_VERSION: &str = match option_env!("NETSGO_DESKTOP_VERSION") {
    Some(version) => version,
    None => "dev",
};

pub const APP_ID: &str = "NetsGo.Desktop";

pub fn target() -> String {
    let os = match std::env::consts::OS {
        "macos" => "osx",
        "windows" => "win",
        other => other,
    };
    let arch = match std::env::consts::ARCH {
        "aarch64" => "arm64",
        "x86_64" => "x64",
        other => other,
    };
    format!("{os}-{arch}")
}
