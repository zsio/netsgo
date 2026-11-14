use anyhow::Result;
use fs2::FileExt;
use std::{
    fs::{self, File, OpenOptions},
    path::{Path, PathBuf},
};

pub struct GuiInstance {
    _lock: File,
    activation: PathBuf,
}
impl GuiInstance {
    pub fn claim(root: &Path, activate_existing: bool) -> Result<Option<Self>> {
        fs::create_dir_all(root)?;
        let lock = OpenOptions::new()
            .create(true)
            .truncate(false)
            .read(true)
            .write(true)
            .open(root.join("desktop-ui.lock"))?;
        let activation = root.join("desktop-activate.request");
        if lock.try_lock_exclusive().is_err() {
            if activate_existing {
                fs::write(activation, b"activate")?;
            }
            return Ok(None);
        }
        let _ = fs::remove_file(&activation);
        let _ = fs::remove_file(root.join(crate::uninstall::QUIT_REQUEST));
        Ok(Some(Self {
            _lock: lock,
            activation,
        }))
    }
    pub fn take_activation(&self) -> bool {
        fs::remove_file(&self.activation).is_ok()
    }
    pub fn take_uninstall_request(&self) -> bool {
        fs::remove_file(
            self.activation
                .parent()
                .unwrap()
                .join(crate::uninstall::QUIT_REQUEST),
        )
        .is_ok()
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn duplicate_launch_activates_the_owner_instead_of_creating_a_second_supervisor() {
        let root = tempfile::tempdir().unwrap();
        let first = GuiInstance::claim(root.path(), false).unwrap().unwrap();
        assert!(GuiInstance::claim(root.path(), false).unwrap().is_none());
        assert!(!first.take_activation());
        assert!(GuiInstance::claim(root.path(), true).unwrap().is_none());
        assert!(first.take_activation());
        drop(first);
        // Other parallel tests may briefly fork with this descriptor before exec closes it.
        let deadline = std::time::Instant::now() + std::time::Duration::from_secs(1);
        loop {
            if GuiInstance::claim(root.path(), false).unwrap().is_some() {
                break;
            }
            assert!(
                std::time::Instant::now() < deadline,
                "the owner lock must eventually be released"
            );
            std::thread::sleep(std::time::Duration::from_millis(10));
        }
    }
}
