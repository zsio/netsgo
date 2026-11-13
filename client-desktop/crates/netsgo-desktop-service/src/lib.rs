mod controller;
pub mod host;
mod instance;
pub mod ipc;
pub mod manage;
pub mod platform;
#[cfg(test)]
mod tests;

pub use controller::DesktopSupervisor;
pub use instance::GuiInstance;
pub use platform::{StartupView, SERVICE_NAME};

pub const BUILD_VERSION: &str = match option_env!("NETSGO_DESKTOP_VERSION") {
    Some(version) => version,
    None => "dev",
};
