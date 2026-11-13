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
        Ok(Some(Self {
            _lock: lock,
            activation,
        }))
    }
    pub fn take_activation(&self) -> bool {
        fs::remove_file(&self.activation).is_ok()
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
        assert!(GuiInstance::claim(root.path(), false).unwrap().is_some());
    }
}
