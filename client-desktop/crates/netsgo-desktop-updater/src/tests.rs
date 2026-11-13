use std::{
    io::{Read, Write},
    net::TcpListener,
    thread,
    time::Duration,
};

use base64::{engine::general_purpose::STANDARD, Engine as _};
use ed25519_dalek::{Signer, SigningKey};
use sha2::{Digest, Sha256};
use velopack::VelopackAsset;

use crate::{feed::*, transport::*, APP_ID};

fn release(tag: &str, body: &[u8]) -> Release {
    Release {
        tag: tag.into(),
        asset: VelopackAsset {
            PackageId: APP_ID.into(),
            Version: tag[1..].into(),
            Type: "Full".into(),
            FileName: format!("NetsGo.Desktop-{}-osx-arm64-full.nupkg", &tag[1..]),
            Size: body.len() as u64,
            SHA256: format!("{:x}", Sha256::digest(body)),
            ..Default::default()
        },
        urls: vec![DownloadUrl {
            provider: "github".into(),
            url: "https://example.com/update.nupkg".into(),
        }],
    }
}

fn feed(releases: Vec<Release>) -> Feed {
    Feed {
        schema: 1,
        product: "netsgo-desktop".into(),
        target: "osx-arm64".into(),
        releases,
    }
}

fn sign(feed: &Feed) -> Vec<u8> {
    let key = SigningKey::from_bytes(&[7; 32]); // Test fixture only, never the release trust root.
    let payload = serde_json::to_vec(feed).unwrap();
    serde_json::to_vec(&SignedFeed {
        signature: STANDARD.encode(key.sign(&payload).to_bytes()),
        payload: STANDARD.encode(payload),
    })
    .unwrap()
}

fn server(status: u16, body: Vec<u8>, delay: Duration) -> String {
    let listener = TcpListener::bind("127.0.0.1:0").unwrap();
    let addr = listener.local_addr().unwrap();
    thread::spawn(move || {
        let (mut socket, _) = listener.accept().unwrap();
        socket
            .set_read_timeout(Some(Duration::from_secs(2)))
            .unwrap();
        let _ = socket.read(&mut [0; 4096]);
        thread::sleep(delay);
        let header = format!(
            "HTTP/1.1 {status} Test\r\nContent-Length: {}\r\nConnection: close\r\n\r\n",
            body.len()
        );
        let _ = socket.write_all(header.as_bytes());
        let _ = socket.write_all(&body);
    });
    format!("http://{addr}/test")
}

#[test]
fn strict_channels_reject_development_versions() {
    for value in [
        "dev",
        "0.1.0",
        "v1.0.0-dirty",
        "v1.0.0+build",
        "v1.0.0-rc.1",
        "v1.0.0-beta.0",
        "v1.0.0-beta.01",
        "v1.0.0-beta.1.2",
    ] {
        assert!(release_version(value).is_err(), "{value}");
    }
    assert!(release_version("v1.0.0-beta.10").is_ok());
}

#[test]
fn stable_ignores_beta_and_beta_can_graduate_to_stable() {
    let releases = feed(vec![
        release("v1.1.0", b"stable"),
        release("v1.2.0-beta.10", b"beta"),
    ]);
    assert_eq!(
        candidate(&releases, &release_version("v1.0.0").unwrap())
            .unwrap()
            .tag,
        "v1.1.0"
    );
    assert_eq!(
        candidate(&releases, &release_version("v1.2.0-beta.9").unwrap())
            .unwrap()
            .tag,
        "v1.2.0-beta.10"
    );
    let releases = feed(vec![
        release("v1.2.0", b"stable"),
        release("v1.2.0-beta.10", b"beta"),
    ]);
    assert_eq!(
        candidate(&releases, &release_version("v1.2.0-beta.9").unwrap())
            .unwrap()
            .tag,
        "v1.2.0"
    );
}

#[test]
fn cli_release_does_not_advance_desktop_and_never_downgrades() {
    let desktop = feed(vec![release("v1.0.0", b"desktop")]);
    assert!(candidate(&desktop, &release_version("v1.0.0").unwrap()).is_none());
    assert!(candidate(&desktop, &release_version("v1.1.0").unwrap()).is_none());
}

#[test]
fn signatures_bind_platform_product_version_and_package() {
    let key = SigningKey::from_bytes(&[7; 32]).verifying_key();
    let good = sign(&feed(vec![release("v1.0.0", b"ok")]));
    assert!(verify_feed(&good, "osx-arm64", &key).is_ok());
    assert!(verify_feed(&good, "win-x64", &key).is_err());
    assert!(verify_feed(
        &good,
        "osx-arm64",
        &SigningKey::from_bytes(&[8; 32]).verifying_key()
    )
    .is_err());
    let mut bad: SignedFeed = serde_json::from_slice(&good).unwrap();
    bad.payload = STANDARD.encode(b"{}");
    assert!(verify_feed(&serde_json::to_vec(&bad).unwrap(), "osx-arm64", &key).is_err());
    for field in ["path", "version", "product", "hash"] {
        let mut value = feed(vec![release("v1.0.0", b"ok")]);
        match field {
            "path" => value.releases[0].asset.FileName = "../outside-full.nupkg".into(),
            "version" => value.releases[0].asset.Version = "2.0.0".into(),
            "product" => value.product = "netsgo-cli".into(),
            _ => value.releases[0].asset.SHA256.clear(),
        }
        assert!(
            verify_feed(&sign(&value), "osx-arm64", &key).is_err(),
            "{field}"
        );
    }
}

#[test]
fn both_regions_are_queried_and_stale_mirror_cannot_hide_update() {
    let current = release_version("v1.0.0").unwrap();
    let sources = vec![
        FeedSource {
            provider: "cnb".into(),
            url: server(
                200,
                sign(&feed(vec![release("v1.0.0", b"old")])),
                Duration::ZERO,
            ),
        },
        FeedSource {
            provider: "github".into(),
            url: server(
                200,
                sign(&feed(vec![release("v1.1.0", b"new")])),
                Duration::from_millis(30),
            ),
        },
    ];
    let found = Transport::default()
        .check(
            &sources,
            "osx-arm64",
            &current,
            &SigningKey::from_bytes(&[7; 32]).verifying_key(),
        )
        .unwrap()
        .unwrap();
    assert_eq!(found.tag, "v1.1.0");
}

#[test]
fn either_region_can_fail_without_preventing_discovery() {
    for failed_provider in ["github", "cnb"] {
        let sources = vec![
            FeedSource {
                provider: failed_provider.into(),
                url: server(503, vec![], Duration::ZERO),
            },
            FeedSource {
                provider: "available".into(),
                url: server(
                    200,
                    sign(&feed(vec![release("v1.1.0", b"new")])),
                    Duration::ZERO,
                ),
            },
        ];
        assert!(Transport::default()
            .check(
                &sources,
                "osx-arm64",
                &release_version("v1.0.0").unwrap(),
                &SigningKey::from_bytes(&[7; 32]).verifying_key()
            )
            .unwrap()
            .is_some());
    }
}

#[test]
fn unavailable_sources_are_not_reported_as_up_to_date() {
    let sources = vec![FeedSource {
        provider: "failed".into(),
        url: server(404, vec![], Duration::ZERO),
    }];
    assert!(Transport::default()
        .check(
            &sources,
            "osx-arm64",
            &release_version("v1.0.0").unwrap(),
            &SigningKey::from_bytes(&[7; 32]).verifying_key()
        )
        .is_err());
}

#[test]
fn hanging_mirror_has_bounded_wait() {
    let http = Transport {
        connect_timeout: Duration::from_millis(80),
        read_timeout: Duration::from_secs(2),
        check_timeout: Duration::from_millis(200),
    };
    let sources = vec![
        FeedSource {
            provider: "stalled".into(),
            url: server(200, vec![], Duration::from_secs(2)),
        },
        FeedSource {
            provider: "available".into(),
            url: server(
                200,
                sign(&feed(vec![release("v1.1.0", b"new")])),
                Duration::ZERO,
            ),
        },
    ];
    let start = std::time::Instant::now();
    assert!(http
        .check(
            &sources,
            "osx-arm64",
            &release_version("v1.0.0").unwrap(),
            &SigningKey::from_bytes(&[7; 32]).verifying_key()
        )
        .unwrap()
        .is_some());
    assert!(start.elapsed() < Duration::from_secs(1));
}

#[test]
fn corrupt_download_falls_back_and_never_leaves_an_invalid_package() {
    let directory = tempfile::tempdir().unwrap();
    let dest = directory.path().join("update.partial");
    let mut package = release("v1.1.0", b"correct");
    package.urls = vec![
        DownloadUrl {
            provider: "cnb".into(),
            url: server(200, b"corrupt".to_vec(), Duration::ZERO),
        },
        DownloadUrl {
            provider: "github".into(),
            url: server(200, b"correct".to_vec(), Duration::ZERO),
        },
    ];
    Transport::default()
        .download(&package, &dest, None)
        .unwrap();
    verify_file(&dest, &package.asset).unwrap();
    package.urls = vec![DownloadUrl {
        provider: "failed".into(),
        url: server(200, b"short".to_vec(), Duration::ZERO),
    }];
    assert!(Transport::default()
        .download(&package, &dest, None)
        .is_err());
    assert!(!dest.exists());
}

#[test]
fn cached_package_is_checked_again_before_install() {
    let directory = tempfile::tempdir().unwrap();
    let path = directory.path().join("update.nupkg");
    let package = release("v1.1.0", b"trusted");
    std::fs::write(&path, b"trusted").unwrap();
    verify_file(&path, &package.asset).unwrap();
    std::fs::write(&path, b"changed").unwrap();
    assert!(verify_file(&path, &package.asset).is_err());
}
