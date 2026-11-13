use std::{
    fs::File,
    io::{Read, Write},
    path::Path,
    sync::mpsc,
    thread,
    time::{Duration, Instant},
};

use anyhow::{bail, Context, Result};
use ed25519_dalek::VerifyingKey;
use semver::Version;
use sha2::{Digest, Sha256};
use velopack::{sources::UpdateSource, VelopackAsset, VelopackAssetFeed};

use crate::feed::{candidate, release_version, verify_feed, Release};

const MAX_FEED_BYTES: u64 = 1024 * 1024;

#[derive(Clone)]
pub(crate) struct FeedSource {
    pub provider: String,
    pub url: String,
}

pub(crate) fn sources(target: &str) -> Vec<FeedSource> {
    vec![
        FeedSource {
            provider: "cnb".into(),
            url: format!("https://cnb.cool/zsio/netsgo/-/git/raw/release-index/updates/desktop-v1/{target}.json"),
        },
        FeedSource {
            provider: "github".into(),
            url: format!("https://raw.githubusercontent.com/zsio/netsgo/release-index/updates/desktop-v1/{target}.json"),
        },
    ]
}

#[derive(Clone)]
pub(crate) struct Transport {
    pub connect_timeout: Duration,
    pub read_timeout: Duration,
    pub check_timeout: Duration,
}

impl Default for Transport {
    fn default() -> Self {
        Self {
            connect_timeout: Duration::from_secs(4),
            read_timeout: Duration::from_secs(10),
            check_timeout: Duration::from_secs(12),
        }
    }
}

impl Transport {
    fn agent(&self, timeout: Duration) -> ureq::Agent {
        ureq::Agent::config_builder()
            .https_only(!cfg!(test))
            .timeout_connect(Some(self.connect_timeout))
            .timeout_recv_response(Some(self.read_timeout))
            .timeout_per_read(Some(self.read_timeout))
            .timeout_global(Some(timeout))
            .user_agent("NetsGo-Desktop-Updater")
            .build()
            .into()
    }

    fn feed(&self, url: &str, target: &str, key: &VerifyingKey) -> Result<crate::feed::Feed> {
        let response = self.agent(self.check_timeout).get(url).call()?;
        let mut bytes = Vec::new();
        response
            .into_body()
            .into_reader()
            .take(MAX_FEED_BYTES + 1)
            .read_to_end(&mut bytes)?;
        if bytes.len() as u64 > MAX_FEED_BYTES {
            bail!("更新清单过大");
        }
        verify_feed(&bytes, target, key)
    }

    pub fn check(
        &self,
        sources: &[FeedSource],
        target: &str,
        current: &Version,
        key: &VerifyingKey,
    ) -> Result<Option<Release>> {
        // Query both regions concurrently. A stale-but-reachable mirror must not hide a newer release.
        let deadline = Instant::now() + self.check_timeout;
        let (tx, rx) = mpsc::channel();
        for source in sources {
            let (tx, http, source, target, key) = (
                tx.clone(),
                self.clone(),
                source.clone(),
                target.to_owned(),
                *key,
            );
            thread::spawn(move || {
                let _ = tx.send((source.provider, http.feed(&source.url, &target, &key)));
            });
        }
        drop(tx);
        let mut valid = 0;
        let mut best: Option<Release> = None;
        let mut errors = Vec::new();
        loop {
            // Bound the coordinator too, including a resolver that outlives the HTTP deadline.
            let (provider, result) =
                match rx.recv_timeout(deadline.saturating_duration_since(Instant::now())) {
                    Ok(result) => result,
                    Err(mpsc::RecvTimeoutError::Disconnected) => break,
                    Err(mpsc::RecvTimeoutError::Timeout) => {
                        errors.push("部分更新源响应超时".into());
                        break;
                    }
                };
            match result {
                Ok(feed) => {
                    valid += 1;
                    if let Some(mut found) = candidate(&feed, current) {
                        found.urls.sort_by_key(|url| url.provider != provider);
                        if let Some(previous) = &best {
                            let cmp =
                                release_version(&found.tag)?.cmp(&release_version(&previous.tag)?);
                            if cmp.is_eq() && found.asset.SHA256 != previous.asset.SHA256 {
                                bail!("更新源返回了内容不同的同版本安装包，请稍后重试");
                            }
                            if !cmp.is_gt() {
                                continue;
                            }
                        }
                        best = Some(found);
                    }
                }
                Err(error) => errors.push(format!("{provider}: {error:#}")),
            }
        }
        if valid == 0 {
            bail!("暂时无法检查更新，请检查网络后重试。{}", errors.join("；"));
        }
        Ok(best)
    }

    fn download_one(
        &self,
        url: &str,
        asset: &VelopackAsset,
        path: &Path,
        progress: &Option<mpsc::Sender<i16>>,
    ) -> Result<()> {
        let response = self.agent(Duration::from_secs(30 * 60)).get(url).call()?;
        let mut reader = response.into_body().into_reader();
        let mut file = File::create(path)?;
        let mut hash = Sha256::new();
        let mut total = 0;
        let mut last_progress = -1;
        let mut buffer = [0; 64 * 1024];
        loop {
            let n = reader.read(&mut buffer)?;
            if n == 0 {
                break;
            }
            total += n as u64;
            if total > asset.Size {
                bail!("下载文件大小超出签名记录");
            }
            file.write_all(&buffer[..n])?;
            hash.update(&buffer[..n]);
            let percent = (total * 100 / asset.Size) as i16;
            if percent != last_progress {
                if let Some(tx) = progress {
                    let _ = tx.send(percent);
                }
                last_progress = percent;
            }
        }
        if total != asset.Size || format!("{:x}", hash.finalize()) != asset.SHA256.to_lowercase() {
            bail!("更新包校验失败，文件不完整或已被修改");
        }
        file.sync_all()?;
        Ok(())
    }

    pub fn download(
        &self,
        release: &Release,
        path: &Path,
        progress: Option<mpsc::Sender<i16>>,
    ) -> Result<()> {
        let mut errors = Vec::new();
        for url in &release.urls {
            if let Some(tx) = &progress {
                let _ = tx.send(0);
            }
            match self.download_one(&url.url, &release.asset, path, &progress) {
                Ok(()) => return Ok(()),
                Err(error) => {
                    let _ = std::fs::remove_file(path);
                    errors.push(format!("{}: {error:#}", url.provider));
                }
            }
        }
        bail!("下载失败，已尝试所有更新源。{}", errors.join("；"))
    }
}

pub(crate) fn verify_file(path: &Path, asset: &VelopackAsset) -> Result<()> {
    let mut file = File::open(path)?;
    if file.metadata()?.len() != asset.Size {
        bail!("更新包大小校验失败");
    }
    let mut hash = Sha256::new();
    let mut buffer = [0; 64 * 1024];
    loop {
        let n = file.read(&mut buffer)?;
        if n == 0 {
            break;
        }
        hash.update(&buffer[..n]);
    }
    if format!("{:x}", hash.finalize()) != asset.SHA256.to_lowercase() {
        bail!("更新包内容校验失败");
    }
    Ok(())
}

pub(crate) struct VerifiedSource {
    pub release: Release,
    pub http: Transport,
}

impl UpdateSource for VerifiedSource {
    fn get_release_feed(
        &self,
        _: &str,
        _: &velopack::bundle::Manifest,
        _: &str,
    ) -> Result<VelopackAssetFeed, velopack::Error> {
        Ok(VelopackAssetFeed {
            Assets: vec![self.release.asset.clone()],
        })
    }
    fn download_release_entry(
        &self,
        asset: &VelopackAsset,
        path: &Path,
        progress: Option<mpsc::Sender<i16>>,
    ) -> Result<(), velopack::Error> {
        if asset.FileName != self.release.asset.FileName
            || asset.SHA256 != self.release.asset.SHA256
        {
            return Err(velopack::Error::Other("未授权的更新包".into()));
        }
        self.http
            .download(&self.release, path, progress)
            .context("下载更新包")
            .map_err(|e| velopack::Error::Other(format!("{e:#}")))
    }
}
