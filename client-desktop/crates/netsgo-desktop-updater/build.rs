use std::{env, fs, path::PathBuf};

fn main() {
    // Keep the desktop trust root in sync with the existing CLI release key.
    let script = PathBuf::from(env::var_os("CARGO_MANIFEST_DIR").unwrap())
        .join("../../../scripts/common-update.sh");
    println!("cargo:rerun-if-changed={}", script.display());
    let source = fs::read_to_string(script).expect("read release public key");
    let key = source
        .split("NETSGO_RELEASE_PUBLIC_KEY_PEM='")
        .nth(1)
        .and_then(|value| value.split('\'').next())
        .expect("embedded public key");
    fs::write(
        PathBuf::from(env::var_os("OUT_DIR").unwrap()).join("release-public-key.pem"),
        key,
    )
    .expect("write release public key");
}
