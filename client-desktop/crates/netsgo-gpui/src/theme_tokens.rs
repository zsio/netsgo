use std::sync::{Arc, LazyLock};

use gpui_kit::component::theme::{ActiveTheme as _, Theme, ThemeMode};
use gpui_kit::*;
use netsgo_client_core::{AppearancePreference, ConnectionPhase};

/// Adaptive native Apple / macOS Settings-inspired semantic tokens.
/// Light mode uses crisp warm alabaster `#F6F7F9` canvas + pure white `#FFFFFF`
/// cards with subtle elevation; Dark mode uses refined macOS native slate
/// `#18191C` canvas + `#222429` elevated cards — never harsh pitch-black.
#[derive(Clone, Copy)]
pub struct SurfaceTokens {
    pub is_dark: bool,
    pub window_bg: Hsla,
    pub sidebar_bg: Hsla,
    pub card_bg: Hsla,
    pub card_hover: Hsla,
    pub subtle_fill: Hsla,
    pub border: Hsla,
    pub border_strong: Hsla,
    pub text_primary: Hsla,
    pub text_secondary: Hsla,
    pub text_muted: Hsla,
    pub accent: Hsla,
    pub accent_hover: Hsla,
    pub accent_soft: Hsla,
    pub online: Hsla,
    pub online_soft: Hsla,
    pub warning: Hsla,
    pub warning_soft: Hsla,
    pub danger: Hsla,
    pub danger_soft: Hsla,
}

impl SurfaceTokens {
    pub fn resolve(cx: &App) -> Self {
        let is_dark = cx.theme().is_dark();
        if is_dark {
            Self {
                is_dark: true,
                window_bg: rgb(0x18191C).into(),
                sidebar_bg: rgb(0x141518).into(),
                card_bg: rgb(0x212328).into(),
                card_hover: rgb(0x2D3038).into(),
                subtle_fill: rgba(0xFFFFFF0D).into(),
                border: rgba(0xFFFFFF13).into(),
                border_strong: rgba(0xFFFFFF24).into(),
                text_primary: rgb(0xF3F4F6).into(),
                text_secondary: rgb(0xA1A7B3).into(),
                text_muted: rgb(0x6C7280).into(),
                accent: rgb(0xF97316).into(),
                accent_hover: rgb(0xFB8524).into(),
                accent_soft: rgba(0xF9731624).into(),
                online: rgb(0x22C55E).into(),
                online_soft: rgba(0x22C55E22).into(),
                warning: rgb(0xF59E0B).into(),
                warning_soft: rgba(0xF59E0B22).into(),
                danger: rgb(0xEF4444).into(),
                danger_soft: rgba(0xEF444422).into(),
            }
        } else {
            Self {
                is_dark: false,
                window_bg: rgb(0xF5F6F8).into(),
                sidebar_bg: rgb(0xECEEF2).into(),
                card_bg: rgb(0xFFFFFF).into(),
                card_hover: rgb(0xF1F3F7).into(),
                subtle_fill: rgba(0x0F172A0A).into(),
                border: rgba(0x0F172A12).into(),
                border_strong: rgba(0x0F172A22).into(),
                text_primary: rgb(0x111827).into(),
                text_secondary: rgb(0x4B5563).into(),
                text_muted: rgb(0x8A94A6).into(),
                accent: rgb(0xEA680C).into(),
                accent_hover: rgb(0xD95B05).into(),
                accent_soft: rgba(0xEA680C16).into(),
                online: rgb(0x16A34A).into(),
                online_soft: rgba(0x16A34A16).into(),
                warning: rgb(0xD97706).into(),
                warning_soft: rgba(0xD9770616).into(),
                danger: rgb(0xDC2626).into(),
                danger_soft: rgba(0xDC262614).into(),
            }
        }
    }

    pub fn card_shadow(&self) -> Vec<BoxShadow> {
        if self.is_dark {
            vec![BoxShadow {
                color: rgba(0x00000036).into(),
                offset: point(px(0.), px(2.)),
                blur_radius: px(8.),
                spread_radius: px(0.),
                inset: false,
            }]
        } else {
            vec![BoxShadow {
                color: rgba(0x0F172A0A).into(),
                offset: point(px(0.), px(2.)),
                blur_radius: px(10.),
                spread_radius: px(0.),
                inset: false,
            }]
        }
    }

    pub fn phase_color(&self, phase: &ConnectionPhase) -> Hsla {
        match phase {
            ConnectionPhase::Connected => self.online,
            ConnectionPhase::Connecting | ConnectionPhase::Reconnecting => self.accent,
            ConnectionPhase::Disconnecting => self.warning,
            ConnectionPhase::Error => self.danger,
            ConnectionPhase::Saved | ConnectionPhase::Idle => self.text_muted,
        }
    }

    pub fn phase_soft_bg(&self, phase: &ConnectionPhase) -> Hsla {
        match phase {
            ConnectionPhase::Connected => self.online_soft,
            ConnectionPhase::Connecting | ConnectionPhase::Reconnecting => self.accent_soft,
            ConnectionPhase::Disconnecting => self.warning_soft,
            ConnectionPhase::Error => self.danger_soft,
            ConnectionPhase::Saved | ConnectionPhase::Idle => self.subtle_fill,
        }
    }
}

/// Applies the user's `AppearancePreference` (`System`, `Light`, or `Dark`) to
/// both GPUI Component's semantic `Theme` and the native OS window chrome.
pub fn apply_appearance_preference(
    pref: AppearancePreference,
    window: Option<&mut Window>,
    cx: &mut App,
) {
    match pref {
        AppearancePreference::System => {
            cx.set_window_appearance(None);
            Theme::sync_system_appearance(window, cx);
        }
        AppearancePreference::Light => {
            cx.set_window_appearance(Some(WindowAppearance::Light));
            Theme::change(ThemeMode::Light, window, cx);
        }
        AppearancePreference::Dark => {
            cx.set_window_appearance(Some(WindowAppearance::Dark));
            Theme::change(ThemeMode::Dark, window, cx);
        }
    }
}

static BRAND_LOGO: LazyLock<Arc<Image>> = LazyLock::new(|| {
    let bytes = include_bytes!("../../../../desktop/src-tauri/icons/128x128.png").to_vec();
    Arc::new(Image::from_bytes(ImageFormat::Png, bytes))
});

pub fn brand_logo_image() -> Arc<Image> {
    BRAND_LOGO.clone()
}

pub fn format_uptime(secs: i64) -> String {
    if secs <= 0 {
        return "00:00".to_string();
    }
    let h = secs / 3600;
    let m = (secs % 3600) / 60;
    let s = secs % 60;
    if h > 0 {
        format!("{h:02}:{m:02}:{s:02}")
    } else {
        format!("{m:02}:{s:02}")
    }
}
