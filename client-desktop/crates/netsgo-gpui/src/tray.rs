use tray_icon::menu::{Menu, MenuEvent, MenuItem, PredefinedMenuItem};
use tray_icon::{Icon, TrayIcon, TrayIconBuilder, TrayIconEvent};

pub const MENU_ID_SHOW: &str = "netsgo.show_window";
pub const MENU_ID_CONNECT: &str = "netsgo.connect_toggle";
pub const MENU_ID_QUIT: &str = "netsgo.quit_app";
pub const MENU_ID_UPDATE: &str = "netsgo.check_update";

pub struct DesktopTray {
    tray: TrayIcon,
    toggle_item: MenuItem,
    quit_item: MenuItem,
}

impl DesktopTray {
    pub fn build(connected_count: usize, total_profiles: usize, service_managed: bool) -> Option<Self> {
        let menu = Menu::new();
        let show_item = MenuItem::with_id(MENU_ID_SHOW, "打开 NetsGo 窗口", true, None);
        let (toggle_label, toggle_enabled) =
            toggle_label(connected_count, 0, total_profiles);
        let toggle_item =
            MenuItem::with_id(MENU_ID_CONNECT, toggle_label, toggle_enabled, None);
        let separator = PredefinedMenuItem::separator();
        let quit_item = MenuItem::with_id(MENU_ID_QUIT, "退出 NetsGo", true, None);
        let update_item = MenuItem::with_id(MENU_ID_UPDATE, "检查更新…", true, None);
        let settings_item = MenuItem::with_id("netsgo.settings", "启动设置…", true, None);
        if service_managed { quit_item.set_text("退出界面（保持后台连接）"); }

        let _ = menu.append(&show_item);
        let _ = menu.append(&toggle_item);
        let _ = menu.append(&separator);
        let _ = menu.append(&update_item);
        let _ = menu.append(&settings_item);
        let _ = menu.append(&quit_item);

        let icon = build_status_icon(connected_count > 0)?;
        let tray = TrayIconBuilder::new()
            .with_tooltip("NetsGo 客户端")
            .with_icon(icon)
            .with_menu(Box::new(menu))
            .build()
            .ok()?;

        Some(Self { tray, toggle_item, quit_item })
    }

    pub fn sync_summary(
        &self,
        connected_count: usize,
        running_count: usize,
        total_profiles: usize,
        total_tunnels: usize,
        service_managed: bool,
    ) {
        let (label, enabled) = toggle_label(connected_count, running_count, total_profiles);
        self.toggle_item.set_text(label);
        self.toggle_item.set_enabled(enabled);
        self.quit_item.set_text(if service_managed { "退出界面（保持后台连接）" } else { "退出 NetsGo" });

        if let Some(icon) = build_status_icon(connected_count > 0) {
            let _ = self.tray.set_icon(Some(icon));
        }
        let tooltip = if connected_count > 0 {
            format!("NetsGo · {connected_count} 个服务在线 · {total_tunnels} 条隧道")
        } else if running_count > 0 {
            format!("NetsGo · {running_count} 个服务正在连接…")
        } else {
            "NetsGo · 未连接".to_string()
        };
        let _ = self.tray.set_tooltip(Some(tooltip));
    }
}

pub fn drain_tray_actions() -> Vec<TrayCommand> {
    let mut actions = Vec::new();
    while let Ok(ev) = MenuEvent::receiver().try_recv() {
        match ev.id().0.as_str() {
            MENU_ID_SHOW => actions.push(TrayCommand::ShowWindow),
            MENU_ID_CONNECT => actions.push(TrayCommand::ToggleAll),
            MENU_ID_QUIT => actions.push(TrayCommand::Quit),
            MENU_ID_UPDATE => actions.push(TrayCommand::CheckUpdate),
            "netsgo.settings" => actions.push(TrayCommand::Settings),
            _ => {}
        }
    }
    while let Ok(ev) = TrayIconEvent::receiver().try_recv() {
        if matches!(ev, TrayIconEvent::DoubleClick { .. }) {
            actions.push(TrayCommand::ShowWindow);
        }
    }
    actions
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum TrayCommand {
    Settings,
    CheckUpdate,
    ShowWindow,
    ToggleAll,
    Quit,
}

fn toggle_label(
    connected_count: usize,
    running_count: usize,
    total_profiles: usize,
) -> (String, bool) {
    if running_count > 0 {
        (format!("断开全部连接 ({running_count})"), true)
    } else if total_profiles > 0 {
        let _ = connected_count;
        ("恢复已保存连接".to_string(), true)
    } else {
        ("尚未添加服务器".to_string(), false)
    }
}

fn build_status_icon(connected: bool) -> Option<Icon> {
    let png_bytes = include_bytes!("../../../../desktop/src-tauri/icons/32x32.png");
    let decoded = image::load_from_memory(png_bytes).ok()?.into_rgba8();
    let (width, height) = decoded.dimensions();
    let mut rgba = decoded.into_raw();

    if !connected {
        for px in rgba.as_chunks_mut::<4>().0 {
            if px[3] > 0 {
                let lum =
                    ((u16::from(px[0]) * 77 + u16::from(px[1]) * 150 + u16::from(px[2]) * 29)
                        >> 8) as u8;
                px[0] = lum;
                px[1] = lum;
                px[2] = lum;
                px[3] = ((u16::from(px[3]) * 185) / 255) as u8;
            }
        }
    }

    Icon::from_rgba(rgba, width, height).ok()
}
