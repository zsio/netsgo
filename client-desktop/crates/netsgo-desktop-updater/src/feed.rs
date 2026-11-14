use anyhow::{bail, Context, Result};
use base64::{engine::general_purpose::STANDARD, Engine as _};
use ed25519_dalek::{pkcs8::DecodePublicKey, Signature, VerifyingKey};
use semver::Version;
use serde::{Deserialize, Serialize};
use velopack::VelopackAsset;

use crate::APP_ID;

#[derive(Clone, Debug, Deserialize, Serialize)]
pub(crate) struct DownloadUrl {
    pub provider: String,
    pub url: String,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
pub(crate) struct Release {
    pub tag: String,
    pub asset: VelopackAsset,
    pub urls: Vec<DownloadUrl>,
}

#[derive(Debug, Deserialize, Serialize)]
pub(crate) struct Feed {
    pub schema: u32,
    pub product: String,
    pub target: String,
    pub releases: Vec<Release>,
}

#[derive(Deserialize, Serialize)]
pub(crate) struct SignedFeed {
    pub payload: String,
    pub signature: String,
}

pub(crate) fn release_version(tag: &str) -> Result<Version> {
    let raw = tag.strip_prefix('v').context("版本必须以 v 开头")?;
    let version = Version::parse(raw).context("无效的版本号")?;
    if !version.build.is_empty() || version.to_string() != raw {
        bail!("非发布版本");
    }
    if !version.pre.is_empty() {
        let number = version
            .pre
            .as_str()
            .strip_prefix("beta.")
            .context("非 beta 发布版本")?;
        if number.starts_with('0')
            || !number.bytes().all(|c| c.is_ascii_digit())
            || number.is_empty()
        {
            bail!("无效的 beta 版本");
        }
    }
    Ok(version)
}

pub(crate) fn verify_feed(bytes: &[u8], target: &str, key: &VerifyingKey) -> Result<Feed> {
    let envelope: SignedFeed = serde_json::from_slice(bytes).context("更新清单格式错误")?;
    let payload = STANDARD
        .decode(envelope.payload)
        .context("更新清单编码错误")?;
    let signature = Signature::from_slice(&STANDARD.decode(envelope.signature)?)?;
    key.verify_strict(&payload, &signature)
        .context("更新清单签名验证失败")?;
    let feed: Feed = serde_json::from_slice(&payload)?;
    if feed.schema != 1 || feed.product != "netsgo-desktop" || feed.target != target {
        bail!("更新清单不适用于当前客户端");
    }
    if feed.releases.len() > 2 {
        bail!("更新清单包含过多发布通道");
    }
    for release in &feed.releases {
        let version = release_version(&release.tag)?;
        let asset = &release.asset;
        if asset.PackageId != APP_ID || asset.Type != "Full" || asset.Version != version.to_string()
        {
            bail!("更新包身份与版本不匹配");
        }
        // These names are joined to the package directory by the installation framework.
        if !asset.FileName.ends_with("-full.nupkg")
            || asset.FileName.contains("..")
            || !asset
                .FileName
                .bytes()
                .all(|c| c.is_ascii_alphanumeric() || b"._-".contains(&c))
            || asset.Size == 0
            || asset.Size > 2 * 1024 * 1024 * 1024
            || asset.SHA256.len() != 64
            || !asset.SHA256.bytes().all(|c| c.is_ascii_hexdigit())
            || release.urls.is_empty()
            || release.urls.len() > 4
        {
            bail!("更新包元数据无效");
        }
        for url in &release.urls {
            if !url.url.starts_with("https://") {
                bail!("更新下载必须使用 HTTPS");
            }
        }
    }
    Ok(feed)
}

pub(crate) fn public_key() -> Result<VerifyingKey> {
    Ok(VerifyingKey::from_public_key_pem(include_str!(concat!(
        env!("OUT_DIR"),
        "/release-public-key.pem"
    )))?)
}

pub(crate) fn candidate(feed: &Feed, current: &Version) -> Option<Release> {
    feed.releases
        .iter()
        .filter_map(|release| {
            let version = release_version(&release.tag).ok()?;
            (version > *current && (!current.pre.is_empty() || version.pre.is_empty()))
                .then_some((version, release))
        })
        .max_by(|(a, _), (b, _)| a.cmp(b))
        .map(|(_, release)| release.clone())
}
