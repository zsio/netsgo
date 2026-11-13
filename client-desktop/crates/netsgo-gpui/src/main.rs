#![cfg_attr(all(target_os = "windows", not(debug_assertions)), windows_subsystem = "windows")]

mod theme_tokens;
mod tray;
mod update_ui;

use std::cell::RefCell;
use std::rc::Rc;
use std::sync::mpsc::TryRecvError;
use std::time::Duration;

use gpui_kit::component::{
    button::{Button, ButtonCustomVariant, ButtonVariants as _},
    h_flex,
    input::{Input, InputContentType, InputEvent, InputState, Textarea, TextareaState},
    notification::{Notification, NotificationType},
    scroll::ScrollableElement as _,
    switch::Switch,
    v_flex, Disableable as _, Icon, IconName, Sizable as _, TitleBar, WindowExt as _,
};
use gpui_kit::prelude::FluentBuilder as _;
use gpui_kit::*;
use netsgo_client_core::{
    default_desktop_data_dir, normalize_server_compare, parse_connect_command,
    AppearancePreference, ClientSupervisor, ConnectionPhase, ParsedConnectCommand,
    ProfileSessionView, StartRequest, SupervisorNotification, SupervisorViewState,
};
use netsgo_sidecar_proto::{AuthMode, SidecarTunnelSnapshot};
use netsgo_desktop_updater::{DesktopUpdater, UpdateStatus, UpdateView};
use theme_tokens::{apply_appearance_preference, brand_logo_image, format_uptime, SurfaceTokens};
use tray::{drain_tray_actions, DesktopTray, TrayCommand};

actions!(netsgo_desktop, [QuitApp, RefreshSnapshot]);

#[derive(Debug, Clone, PartialEq, Eq)]
enum DetailMode {
    Updates,
    /// Selected server's live status, quick controls, and tunnels
    SessionDetail,
    /// Add a new server or update credentials for an existing server
    AddOrEditServer { editing_profile_id: Option<String> },
    /// Live structured logs for the selected server
    Logs,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum OnboardingEntryTab {
    SmartCommand,
    ManualFields,
}

struct NetsGoDesktopView {
    updater: DesktopUpdater,
    update_state: UpdateView,
    supervisor: ClientSupervisor,
    state: SupervisorViewState,
    detail_mode: DetailMode,
    entry_tab: OnboardingEntryTab,
    parsed_preview: Option<ParsedConnectCommand>,
    show_advanced_tls: bool,
    tls_skip_verify: bool,
    editing_remark_profile_id: Option<String>,
    remark_inline_input: Entity<InputState>,
    command_textarea: Entity<TextareaState>,
    server_input: Entity<InputState>,
    name_input: Entity<InputState>,
    key_input: Entity<InputState>,
    fingerprint_input: Entity<InputState>,
    _subscriptions: Vec<Subscription>,
}

impl NetsGoDesktopView {
    fn new(
        supervisor: ClientSupervisor,
        updater: DesktopUpdater,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) -> Self {
        let initial = supervisor.view_state();
        apply_appearance_preference(initial.appearance, Some(window), cx);

        let command_textarea = cx.new(|cx| {
            TextareaState::new(window, cx).placeholder(
                "粘贴服务端生成的任意连接指令：\n• curl -fsSL .../install.sh | sh -s -- --client --server ... --key ...\n• netsgo client --server ... --key ...\n• docker run / docker-compose.yml 配置片段",
            )
        });
        let server_input = cx.new(|cx| {
            InputState::new(window, cx).placeholder("https://netsgo.example.com")
        });
        let name_input = cx.new(|cx| InputState::new(window, cx).placeholder("备注名称"));
        let key_input = cx.new(|cx| {
            InputState::new(window, cx)
                .placeholder("请输入 Client Key")
                .masked(true)
        });
        let fingerprint_input = cx.new(|cx| {
            InputState::new(window, cx).placeholder("AA:BB:CC:...")
        });
        let remark_inline_input =
            cx.new(|cx| InputState::new(window, cx).placeholder("输入备注名称（留空恢复默认地址）"));

        let mut subscriptions = Vec::new();

        // Automatically parse onboarding commands as soon as the user types or pastes!
        subscriptions.push(cx.subscribe_in(
            &command_textarea,
            window,
            |this, state, ev, _, cx| {
                if matches!(ev, InputEvent::Change) {
                    let raw = state.read(cx).value().to_string();
                    this.parsed_preview =
                        parse_connect_command(&raw).filter(ParsedConnectCommand::is_complete);
                    cx.notify();
                }
            },
        ));

        // Even in the Server URL input, if someone accidentally pastes a full `netsgo client ...` command, auto-split it!
        subscriptions.push(cx.subscribe_in(
            &server_input,
            window,
            |this, state, ev, window, cx| {
                if matches!(ev, InputEvent::Change) {
                    let raw = state.read(cx).value().to_string();
                    if raw.contains("--key") || raw.contains("NETSGO_KEY") {
                        if let Some(parsed) = parse_connect_command(&raw) {
                            if parsed.is_complete() {
                                this.populate_inputs_from_parsed(&parsed, window, cx);
                                this.push_toast(NotificationType::Success, "已自动从命令提取服务器与凭证", window, cx);
                                return;
                            }
                        }
                    }
                    cx.notify();
                }
            },
        ));

        for input in [&name_input, &key_input, &fingerprint_input] {
            subscriptions.push(cx.subscribe_in(input, window, |_, _, ev, _, cx| {
                if matches!(ev, InputEvent::Change) {
                    cx.notify();
                }
            }));
        }
        subscriptions.push(cx.subscribe_in(
            &remark_inline_input,
            window,
            |this, _, ev, window, cx| match ev {
                InputEvent::Change => cx.notify(),
                InputEvent::PressEnter { .. } => {
                    this.save_inline_remark(window, cx);
                }
                _ => {}
            },
        ));

        // Automatically follow OS light/dark transitions when preference is `AppearancePreference::System`.
        subscriptions.push(cx.observe_window_appearance(window, |this, window, cx| {
            if this.state.appearance == AppearancePreference::System {
                apply_appearance_preference(AppearancePreference::System, Some(window), cx);
                cx.notify();
            }
        }));

        let initial_mode = if initial.sessions.is_empty() {
            DetailMode::AddOrEditServer {
                editing_profile_id: None,
            }
        } else {
            DetailMode::SessionDetail
        };

        Self {
            update_state: updater.view(),
            updater,
            supervisor,
            state: initial,
            detail_mode: initial_mode,
            entry_tab: OnboardingEntryTab::SmartCommand,
            parsed_preview: None,
            show_advanced_tls: false,
            tls_skip_verify: false,
            editing_remark_profile_id: None,
            remark_inline_input,
            command_textarea,
            server_input,
            name_input,
            key_input,
            fingerprint_input,
            _subscriptions: subscriptions,
        }
    }

    fn reload_from_supervisor(&mut self, cx: &mut Context<Self>) {
        self.state = self.supervisor.view_state();
        self.update_state = self.updater.view();
        cx.notify();
    }

    fn push_toast(
        &self,
        kind: NotificationType,
        msg: impl Into<SharedString>,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        struct AppFeedbackToast;
        window.push_notification(
            Notification::new()
                .id::<AppFeedbackToast>()
                .with_type(kind)
                .placement(Anchor::BottomRight)
                .message(msg),
            cx,
        );
    }

    fn copy_with_toast(
        &self,
        value: String,
        label: &str,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        cx.write_to_clipboard(ClipboardItem::new_string(value));
        self.push_toast(
            NotificationType::Success,
            format!("已复制{label}"),
            window,
            cx,
        );
    }

    fn cycle_appearance(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        let next = self.state.appearance.cycle_next();
        if self.supervisor.set_appearance(next).is_ok() {
            self.state.appearance = next;
            apply_appearance_preference(next, Some(window), cx);
            self.push_toast(
                NotificationType::Info,
                format!("界面外观已切换为「{}」", next.label()),
                window,
                cx,
            );
        }
    }

    fn parse_strict_command(&mut self, raw: &str, cx: &mut Context<Self>) -> bool {
        self.parsed_preview =
            parse_connect_command(raw).filter(ParsedConnectCommand::is_complete);
        let ok = self.parsed_preview.is_some();
        cx.notify();
        ok
    }

    fn populate_inputs_from_parsed(
        &mut self,
        parsed: &ParsedConnectCommand,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        if let Some(srv) = &parsed.server {
            let s = srv.clone();
            self.server_input
                .update(cx, |input, cx| input.set_value(s, window, cx));
        }
        if let Some(key) = &parsed.key {
            let k = key.clone();
            self.key_input
                .update(cx, |input, cx| input.set_value(k, window, cx));
        }
        if parsed.tls_skip_verify {
            self.tls_skip_verify = true;
            self.show_advanced_tls = true;
        }
        if let Some(fp) = &parsed.tls_fingerprint {
            let f = fp.clone();
            self.fingerprint_input
                .update(cx, |input, cx| input.set_value(f, window, cx));
            self.show_advanced_tls = true;
        }
    }

    fn paste_clipboard_command(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        let Some(clip) = cx.read_from_clipboard().and_then(|item| item.text()) else {
            self.push_toast(
                NotificationType::Warning,
                "剪贴板中没有可识别的文本内容",
                window,
                cx,
            );
            return;
        };
        let trimmed = clip.trim().to_string();
        if trimmed.is_empty() {
            self.push_toast(NotificationType::Warning, "剪贴板为空", window, cx);
            return;
        }
        if self.parse_strict_command(&trimmed, cx) {
            self.command_textarea
                .update(cx, |area, cx| area.set_value(trimmed, window, cx));
            self.push_toast(
                NotificationType::Success,
                "已识别连接命令参数",
                window,
                cx,
            );
        } else {
            self.push_toast(
                NotificationType::Warning,
                "识别失败：剪贴板内容不是有效的首次连接命令",
                window,
                cx,
            );
        }
    }

    fn start_inline_remark_edit(
        &mut self,
        session: &ProfileSessionView,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        let current = session.profile.name.clone();
        let pid = session.profile.id.clone();
        self.remark_inline_input.update(cx, |input, cx| {
            input.set_value(current, window, cx);
            input.focus(window, cx);
        });
        self.editing_remark_profile_id = Some(pid);
        cx.notify();
    }

    fn save_inline_remark(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        let Some(pid) = self.editing_remark_profile_id.take() else {
            return;
        };
        let new_name = self.remark_inline_input.read(cx).value().trim().to_string();
        match self.supervisor.rename_profile(&pid, &new_name) {
            Ok(()) => {
                self.reload_from_supervisor(cx);
                self.push_toast(NotificationType::Success, "已更新备注名称", window, cx);
            }
            Err(err) => {
                self.push_toast(NotificationType::Error, err.to_string(), window, cx);
            }
        }
    }

    fn open_add_server_form(
        &mut self,
        editing: Option<&ProfileSessionView>,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        self.editing_remark_profile_id = None;
        self.parsed_preview = None;
        self.command_textarea
            .update(cx, |s, cx| s.clean(window, cx));
        self.key_input.update(cx, |s, cx| s.clean(window, cx));

        if let Some(session) = editing {
            let p = &session.profile;
            self.entry_tab = OnboardingEntryTab::ManualFields;
            self.tls_skip_verify = p.tls_skip_verify;
            self.show_advanced_tls = p.tls_skip_verify || !p.tls_fingerprint.is_empty();
            let srv = p.server.clone();
            let name = p.name.clone();
            let fp = p.tls_fingerprint.clone();
            self.server_input
                .update(cx, |s, cx| s.set_value(srv, window, cx));
            self.name_input
                .update(cx, |s, cx| s.set_value(name, window, cx));
            self.fingerprint_input
                .update(cx, |s, cx| s.set_value(fp, window, cx));
            self.detail_mode = DetailMode::AddOrEditServer {
                editing_profile_id: Some(p.id.clone()),
            };
        } else {
            self.entry_tab = OnboardingEntryTab::SmartCommand;
            self.tls_skip_verify = false;
            self.show_advanced_tls = false;
            self.server_input.update(cx, |s, cx| s.clean(window, cx));
            self.name_input.update(cx, |s, cx| s.clean(window, cx));
            self.fingerprint_input
                .update(cx, |s, cx| s.clean(window, cx));
            self.detail_mode = DetailMode::AddOrEditServer {
                editing_profile_id: None,
            };
        }
        cx.notify();
    }

    fn submit_server_form(
        &mut self,
        editing_profile_id: Option<String>,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        if let Some(pid) = editing_profile_id {
            match self.entry_tab {
                OnboardingEntryTab::SmartCommand => {
                    let raw = self.command_textarea.read(cx).value().to_string();
                    let Some(parsed) =
                        parse_connect_command(&raw).filter(ParsedConnectCommand::is_complete)
                    else {
                        self.push_toast(
                            NotificationType::Warning,
                            "未识别到有效的首次连接命令，请检查命令或切换至「手动填写参数」",
                            window,
                            cx,
                        );
                        return;
                    };
                    let current_name = self.name_input.read(cx).value().trim().to_string();
                    match self.supervisor.save_or_reconnect_edited_profile(
                        &pid,
                        parsed.server.as_deref().unwrap_or_default(),
                        &current_name,
                        parsed.key.as_deref(),
                        parsed.tls_skip_verify,
                        parsed.tls_fingerprint.as_deref().unwrap_or_default(),
                    ) {
                        Ok(saved_id) => {
                            let _ = self.supervisor.select_profile(&saved_id);
                            self.command_textarea
                                .update(cx, |s, cx| s.clean(window, cx));
                            self.key_input.update(cx, |s, cx| s.clean(window, cx));
                            self.parsed_preview = None;
                            self.detail_mode = DetailMode::SessionDetail;
                            self.reload_from_supervisor(cx);
                        }
                        Err(err) => {
                            self.push_toast(NotificationType::Error, err.to_string(), window, cx);
                        }
                    }
                    return;
                }
                OnboardingEntryTab::ManualFields => {
                    let server = self.server_input.read(cx).value().trim().to_string();
                    let key = self.key_input.read(cx).value().trim().to_string();
                    let name = self.name_input.read(cx).value().trim().to_string();
                    let fingerprint = self.fingerprint_input.read(cx).value().trim().to_string();
                    let tls_skip_verify = self.tls_skip_verify;

                    if server.is_empty() {
                        self.push_toast(
                            NotificationType::Warning,
                            "请填写服务器地址",
                            window,
                            cx,
                        );
                        return;
                    }

                    match self.supervisor.save_or_reconnect_edited_profile(
                        &pid,
                        &server,
                        &name,
                        (!key.is_empty()).then_some(key.as_str()),
                        tls_skip_verify,
                        &fingerprint,
                    ) {
                        Ok(saved_id) => {
                            let _ = self.supervisor.select_profile(&saved_id);
                            self.key_input.update(cx, |s, cx| s.clean(window, cx));
                            self.detail_mode = DetailMode::SessionDetail;
                            self.reload_from_supervisor(cx);
                        }
                        Err(err) => {
                            self.push_toast(NotificationType::Error, err.to_string(), window, cx);
                        }
                    }
                    return;
                }
            }
        }

        let (server, key, name, fingerprint, tls_skip_verify) = match self.entry_tab {
            OnboardingEntryTab::SmartCommand => {
                let raw = self.command_textarea.read(cx).value().to_string();
                let Some(parsed) =
                    parse_connect_command(&raw).filter(ParsedConnectCommand::is_complete)
                else {
                    self.push_toast(
                        NotificationType::Warning,
                        "未识别到有效的首次连接命令，请检查命令或切换至「手动填写参数」",
                        window,
                        cx,
                    );
                    return;
                };
                (
                    parsed.server.unwrap_or_default(),
                    parsed.key.unwrap_or_default(),
                    String::new(),
                    parsed.tls_fingerprint.unwrap_or_default(),
                    parsed.tls_skip_verify,
                )
            }
            OnboardingEntryTab::ManualFields => {
                let server = self.server_input.read(cx).value().trim().to_string();
                let key = self.key_input.read(cx).value().trim().to_string();
                if server.is_empty() || key.is_empty() {
                    self.push_toast(
                        NotificationType::Warning,
                        "请填写完整的服务器地址与访问凭证 (Key)",
                        window,
                        cx,
                    );
                    return;
                }
                (
                    server,
                    key,
                    self.name_input.read(cx).value().trim().to_string(),
                    self.fingerprint_input.read(cx).value().trim().to_string(),
                    self.tls_skip_verify,
                )
            }
        };

        match self.supervisor.connect(StartRequest {
            profile_id: String::new(),
            name,
            server,
            key: Some(key),
            mode: AuthMode::Key,
            tls_skip_verify,
            tls_fingerprint: fingerprint,
        }) {
            Ok(profile_id) => {
                let _ = self.supervisor.select_profile(&profile_id);
                self.command_textarea
                    .update(cx, |s, cx| s.clean(window, cx));
                self.key_input.update(cx, |s, cx| s.clean(window, cx));
                self.parsed_preview = None;
                self.detail_mode = DetailMode::SessionDetail;
                self.reload_from_supervisor(cx);
            }
            Err(err) => {
                self.push_toast(NotificationType::Error, err.to_string(), window, cx);
            }
        }
    }

    fn toggle_session_switch(
        &mut self,
        session: &ProfileSessionView,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        let pid = session.profile.id.clone();
        match session.phase {
            ConnectionPhase::Connected
            | ConnectionPhase::Connecting
            | ConnectionPhase::Reconnecting => {
                let sup = self.supervisor.clone();
                cx.background_executor()
                    .spawn(async move {
                        let _ = sup.disconnect_profile(&pid);
                    })
                    .detach();
            }
            ConnectionPhase::Disconnecting => {}
            ConnectionPhase::Saved | ConnectionPhase::Error | ConnectionPhase::Idle => {
                if session.has_saved_token {
                    let sup = self.supervisor.clone();
                    cx.background_executor()
                        .spawn(async move {
                            let _ = sup.reconnect_profile(&pid);
                        })
                        .detach();
                } else {
                    let _ = self.supervisor.select_profile(&pid);
                    let cloned = session.clone();
                    self.open_add_server_form(Some(&cloned), window, cx);
                }
            }
        }
    }

    // ─────────────────────────── Top Unified Titlebar ───────────────────────────

    fn render_titlebar(&self, tokens: &SurfaceTokens, cx: &mut Context<Self>) -> impl IntoElement {
        let pref = self.state.appearance;
        let online_summary = if self.state.connected_count > 0 {
            format!(
                "{} 个服务在线 · {} 条隧道",
                self.state.connected_count, self.state.total_tunnels
            )
        } else if self.state.running_count > 0 {
            format!("{} 个服务正在握手…", self.state.running_count)
        } else {
            "就绪".to_string()
        };

        let theme_icon = match pref {
            AppearancePreference::System => IconName::Palette,
            AppearancePreference::Light => IconName::Sun,
            AppearancePreference::Dark => IconName::Moon,
        };

        TitleBar::new()
            .bg(tokens.sidebar_bg)
            .border_color(tokens.border)
            .child(
                h_flex()
                    .w_full()
                    .pr_3()
                    .items_center()
                    .justify_between()
                    .child(
                        h_flex()
                            .gap_2()
                            .items_center()
                            .child(img(brand_logo_image()).size(px(16.)).rounded(px(4.)))
                            .child(
                                div()
                                    .text_size(px(12.5))
                                    .font_weight(FontWeight::SEMIBOLD)
                                    .text_color(tokens.text_primary)
                                    .child("NetsGo"),
                            )
                            .child(
                                div()
                                    .px_2()
                                    .py_0p5()
                                    .rounded(px(99.))
                                    .bg(if self.state.connected_count > 0 {
                                        tokens.online_soft
                                    } else {
                                        tokens.subtle_fill
                                    })
                                    .text_size(px(10.5))
                                    .font_weight(FontWeight::MEDIUM)
                                    .text_color(if self.state.connected_count > 0 {
                                        tokens.online
                                    } else {
                                        tokens.text_muted
                                    })
                                    .child(online_summary),
                            ),
                    )
                    .child(
                        h_flex()
                            .gap_1p5()
                            .items_center()
                            .child(
                                div()
                                    .id("appearance-cycle-btn")
                                    .flex()
                                    .items_center()
                                    .gap_1()
                                    .px_2()
                                    .py_1()
                                    .rounded(px(6.))
                                    .cursor_pointer()
                                    .text_size(px(11.))
                                    .text_color(tokens.text_secondary)
                                    .hover(|s| {
                                        s.bg(tokens.subtle_fill).text_color(tokens.text_primary)
                                    })
                                    .on_click(cx.listener(|this, _, window, cx| {
                                        this.cycle_appearance(window, cx);
                                    }))
                                    .child(Icon::new(theme_icon).xsmall())
                                    .child(pref.label()),
                            ),
                    ),
            )
    }

    // ─────────────────────────── Left Multi-Server Rail ───────────────────────────

    fn render_server_rail(
        &self,
        tokens: &SurfaceTokens,
        cx: &mut Context<Self>,
    ) -> impl IntoElement {
        let sessions = self.state.sessions.clone();
        let selected_id = self
            .state
            .selected_session()
            .map(|s| s.profile.id.clone());
        let is_adding_new = matches!(
            self.detail_mode,
            DetailMode::AddOrEditServer {
                editing_profile_id: None
            }
        );

        v_flex()
            .w(px(236.))
            .h_full()
            .flex_shrink_0()
            .bg(tokens.sidebar_bg)
            .border_r_1()
            .border_color(tokens.border)
            .p_2p5()
            .justify_between()
            .child(
                v_flex()
                    .w_full()
                    .gap_2()
                    .overflow_hidden()
                    .child(
                        h_flex()
                            .px_1p5()
                            .py_0p5()
                            .items_center()
                            .justify_between()
                            .child(
                                div()
                                    .text_size(px(11.))
                                    .font_weight(FontWeight::SEMIBOLD)
                                    .text_color(tokens.text_muted)
                                    .child("连接的服务器"),
                            )
                            .child(
                                div()
                                    .id("rail-add-server-icon")
                                    .p_1()
                                    .rounded(px(6.))
                                    .cursor_pointer()
                                    .text_color(tokens.text_secondary)
                                    .hover(|s| {
                                        s.bg(tokens.subtle_fill).text_color(tokens.text_primary)
                                    })
                                    .on_click(cx.listener(|this, _, window, cx| {
                                        this.open_add_server_form(None, window, cx);
                                    }))
                                    .child(Icon::new(IconName::Plus).small()),
                            ),
                    )
                    .child(
                        div()
                            .id("server-rail-scroll")
                            .w_full()
                            .min_w_0()
                            .flex()
                            .flex_col()
                            .gap_1()
                            .overflow_y_scrollbar()
                            .children(sessions.into_iter().map(|session| {
                                self.render_rail_item(
                                    session,
                                    !is_adding_new,
                                    selected_id.as_deref(),
                                    tokens,
                                    cx,
                                )
                            })),
                    ),
            )
            .child(self.render_version_footer(tokens, cx))
    }

    fn render_rail_item(
        &self,
        session: ProfileSessionView,
        allow_selection_highlight: bool,
        selected_id: Option<&str>,
        tokens: &SurfaceTokens,
        cx: &mut Context<Self>,
    ) -> impl IntoElement {
        let is_selected =
            allow_selection_highlight && selected_id == Some(session.profile.id.as_str());
        let dot_color = tokens.phase_color(&session.phase);
        let is_switched_on = session.phase.is_running();
        let pid_select = session.profile.id.clone();
        let session_for_toggle = session.clone();
        let tunnel_len = session.snapshot.tunnels.len();

        let subtitle = if session.phase == ConnectionPhase::Connected {
            format!("在线 · {tunnel_len} 条隧道")
        } else if session.phase.is_running() {
            session.phase.label().to_string()
        } else if session.phase == ConnectionPhase::Error {
            "连接失败".to_string()
        } else if !session.profile.name.trim().is_empty() {
            session.profile.server.clone()
        } else {
            "未连接".to_string()
        };

        div()
            .id(SharedString::from(format!("rail-srv-{}", session.profile.id)))
            .w_full()
            .px_2p5()
            .py_2()
            .rounded(px(10.))
            .cursor_pointer()
            .bg(if is_selected {
                tokens.card_bg
            } else {
                gpui_kit::transparent_black()
            })
            .border_1()
            .border_color(if is_selected {
                tokens.border_strong
            } else {
                gpui_kit::transparent_black()
            })
            .when(is_selected, |el| el.shadow(tokens.card_shadow()))
            .when(!is_selected, |el| el.hover(|s| s.bg(tokens.subtle_fill)))
            .on_click(cx.listener(move |this, _, _, cx| {
                if this.supervisor.select_profile(&pid_select).is_ok() {
                    this.editing_remark_profile_id = None;
                    this.detail_mode = DetailMode::SessionDetail;
                    this.reload_from_supervisor(cx);
                }
            }))
            .child(
                h_flex()
                    .w_full()
                    .items_center()
                    .justify_between()
                    .gap_2()
                    .child(
                        h_flex()
                            .flex_1()
                            .min_w_0()
                            .gap_2()
                            .items_center()
                            .overflow_hidden()
                            .child(
                                div()
                                    .size(px(8.))
                                    .rounded(px(99.))
                                    .flex_shrink_0()
                                    .bg(dot_color),
                            )
                            .child(
                                v_flex()
                                    .flex_1()
                                    .min_w_0()
                                    .gap_0p5()
                                    .overflow_hidden()
                                    .child(
                                        div()
                                            .text_size(px(12.5))
                                            .font_weight(if is_selected {
                                                FontWeight::SEMIBOLD
                                            } else {
                                                FontWeight::MEDIUM
                                            })
                                            .text_color(tokens.text_primary)
                                            .truncate()
                                            .child(session.profile.display_title().to_string()),
                                    )
                                    .child(
                                        div()
                                            .text_size(px(10.5))
                                            .text_color(tokens.text_muted)
                                            .truncate()
                                            .child(subtitle),
                                    ),
                            ),
                    )
                    // Independent inline toggle pill so multiple servers can be switched on/off side-by-side!
                    .child(
                        div()
                            .id(SharedString::from(format!(
                                "rail-switch-{}",
                                session.profile.id
                            )))
                            .flex_shrink_0()
                            .w(px(30.))
                            .h(px(17.))
                            .px(px(2.))
                            .rounded(px(99.))
                            .flex()
                            .items_center()
                            .when(is_switched_on, |el| {
                                el.justify_end().bg(if session.phase == ConnectionPhase::Connected {
                                    tokens.online
                                } else {
                                    tokens.accent
                                })
                            })
                            .when(!is_switched_on, |el| {
                                el.justify_start()
                                    .bg(tokens.border_strong)
                            })
                            .on_click(cx.listener(
                                move |this, _ev: &ClickEvent, window, cx| {
                                    cx.stop_propagation();
                                    this.toggle_session_switch(&session_for_toggle, window, cx);
                                },
                            ))
                            .child(
                                div()
                                    .size(px(13.))
                                    .rounded(px(99.))
                                    .bg(rgb(0xFFFFFF)),
                            ),
                    ),
            )
    }

    // ─────────────────────────── Right Pane 1: Session Detail ───────────────────────────

    fn render_session_detail(
        &self,
        session: &ProfileSessionView,
        tokens: &SurfaceTokens,
        cx: &mut Context<Self>,
    ) -> impl IntoElement {
        let phase = &session.phase;
        let phase_fg = tokens.phase_color(phase);
        let phase_bg = tokens.phase_soft_bg(phase);
        let is_connected = *phase == ConnectionPhase::Connected;
        let is_running = phase.is_running();
        let snap = &session.snapshot;
        let session_for_switch = session.clone();
        let session_for_edit = session.clone();
        let session_for_rename = session.clone();
        let is_editing_remark =
            self.editing_remark_profile_id.as_deref() == Some(session.profile.id.as_str());
        let pid_delete = session.profile.id.clone();
        let pid_refresh = session.profile.id.clone();
        let console_url = session.profile.server.clone();
        let server_to_copy = session.profile.server.clone();

        let status_subtitle = if is_connected {
            format!(
                "持续运行 {} · 承载 {} 条隧道",
                format_uptime(snap.uptime_seconds),
                snap.tunnels.len()
            )
        } else if !session.status_text.is_empty() {
            session.status_text.clone()
        } else if session.has_saved_token {
            "本地凭证就绪，打开右侧开关即可连接".to_string()
        } else {
            "需要输入访问凭证完成首次绑定".to_string()
        };

        let pub_ip = if !snap.public_ipv4.is_empty() {
            snap.public_ipv4.clone()
        } else if !snap.public_ipv6.is_empty() {
            snap.public_ipv6.clone()
        } else {
            "—".to_string()
        };
        let direct_count = snap
            .tunnels
            .iter()
            .filter(|t| t.effective_path == "peer_direct")
            .count();

        v_flex()
            .w_full()
            .min_w_0()
            .gap_4()
            // Primary Native macOS Settings Card
            .child(
                v_flex()
                    .w_full()
                    .p_4()
                    .gap_3p5()
                    .rounded(px(14.))
                    .bg(tokens.card_bg)
                    .border_1()
                    .border_color(tokens.border)
                    .shadow(tokens.card_shadow())
                    .child(
                        h_flex()
                            .w_full()
                            .items_start()
                            .justify_between()
                            .gap_3()
                            .child(
                                v_flex()
                                    .flex_1()
                                    .min_w_0()
                                    .gap_1()
                                    .overflow_hidden()
                                    .child(
                                        if is_editing_remark {
                                            h_flex()
                                                .w_full()
                                                .min_w_0()
                                                .gap_1p5()
                                                .items_center()
                                                .child(
                                                    div()
                                                        .flex_1()
                                                        .min_w_0()
                                                        .max_w(px(280.))
                                                        .child(
                                                            Input::new(&self.remark_inline_input)
                                                                .small()
                                                                .cleanable(true),
                                                        ),
                                                )
                                                .child(
                                                    subtle_action_chip(
                                                        "save-inline-remark",
                                                        IconName::Check,
                                                        "保存",
                                                        tokens,
                                                    )
                                                    .on_click(cx.listener(|this, _, window, cx| {
                                                        this.save_inline_remark(window, cx);
                                                    })),
                                                )
                                                .child(
                                                    div()
                                                        .id("cancel-inline-remark")
                                                        .px_2()
                                                        .py_1()
                                                        .rounded(px(6.))
                                                        .cursor_pointer()
                                                        .text_size(px(11.5))
                                                        .text_color(tokens.text_muted)
                                                        .hover(|s| {
                                                            s.bg(tokens.subtle_fill)
                                                                .text_color(tokens.text_primary)
                                                        })
                                                        .on_click(cx.listener(|this, _, _, cx| {
                                                            this.editing_remark_profile_id = None;
                                                            cx.notify();
                                                        }))
                                                        .child("取消"),
                                                )
                                                .into_any_element()
                                        } else {
                                            h_flex()
                                                .min_w_0()
                                                .gap_1p5()
                                                .items_center()
                                                .child(
                                                    div()
                                                        .text_size(px(17.))
                                                        .font_weight(FontWeight::BOLD)
                                                        .text_color(tokens.text_primary)
                                                        .truncate()
                                                        .child(
                                                            session
                                                                .profile
                                                                .display_title()
                                                                .to_string(),
                                                        ),
                                                )
                                                .child(
                                                    div()
                                                        .id("edit-inline-remark-btn")
                                                        .p_1()
                                                        .rounded(px(6.))
                                                        .flex_shrink_0()
                                                        .cursor_pointer()
                                                        .text_color(tokens.text_muted)
                                                        .hover(|s| {
                                                            s.bg(tokens.subtle_fill)
                                                                .text_color(tokens.text_primary)
                                                        })
                                                        .on_click(cx.listener(
                                                            move |this, _, window, cx| {
                                                                this.start_inline_remark_edit(
                                                                    &session_for_rename,
                                                                    window,
                                                                    cx,
                                                                );
                                                            },
                                                        ))
                                                        .child(
                                                            Icon::new(assets::IconName::Pencil)
                                                                .xsmall(),
                                                        ),
                                                )
                                                .child(badge_chip(
                                                    phase.label(),
                                                    phase_bg,
                                                    phase_fg,
                                                ))
                                                .into_any_element()
                                        },
                                    )
                                    .child(
                                        div()
                                            .text_size(px(11.5))
                                            .text_color(tokens.text_muted)
                                            .child(status_subtitle),
                                    ),
                            )
                            // Primary Connect / Disconnect Action Button
                            .child(
                                div().flex_shrink_0().child(
                                    primary_switch_button(is_running, phase, tokens, cx).on_click(
                                        cx.listener(move |this, _, window, cx| {
                                            this.toggle_session_switch(&session_for_switch, window, cx);
                                        }),
                                    ),
                                ),
                            ),
                    )
                    // Crisp Key-Value Strip
                    .child(
                        h_flex()
                            .w_full()
                            .min_w_0()
                            .pt_3()
                            .border_t_1()
                            .border_color(tokens.border)
                            .justify_between()
                            .gap_3()
                            .child(
                                div()
                                    .id("copy-detail-server-addr")
                                    .flex_1()
                                    .min_w_0()
                                    .px_2()
                                    .py_1()
                                    .ml(px(-8.))
                                    .rounded(px(7.))
                                    .cursor_pointer()
                                    .hover(|s| s.bg(tokens.subtle_fill))
                                    .on_click(cx.listener(move |this, _, window, cx| {
                                        this.copy_with_toast(
                                            server_to_copy.clone(),
                                            "服务端地址",
                                            window,
                                            cx,
                                        );
                                    }))
                                    .child(
                                        v_flex()
                                            .min_w_0()
                                            .gap_0p5()
                                            .child(
                                                h_flex()
                                                    .gap_1()
                                                    .items_center()
                                                    .child(
                                                        div()
                                                            .text_size(px(10.5))
                                                            .text_color(tokens.text_muted)
                                                            .child("服务端地址"),
                                                    )
                                                    .child(
                                                        Icon::new(IconName::Copy)
                                                            .size(px(10.5))
                                                            .text_color(tokens.text_muted),
                                                    ),
                                            )
                                            .child(
                                                div()
                                                    .text_size(px(12.5))
                                                    .font_weight(FontWeight::SEMIBOLD)
                                                    .text_color(tokens.text_primary)
                                                    .truncate()
                                                    .child(session.profile.server.clone()),
                                            ),
                                    ),
                            )
                            .child(kv_cell(
                                "活跃流",
                                format!("{}", snap.active_connections),
                                tokens,
                            ))
                            .child(kv_cell(
                                "P2P 直连",
                                if is_connected {
                                    format!("{direct_count} / {}", snap.tunnels.len())
                                } else {
                                    "—".to_string()
                                },
                                tokens,
                            ))
                            .child(kv_cell("公网出口", pub_ip, tokens)),
                    )
                    // Secondary Native Toolbar Row
                    .child(
                        h_flex()
                            .w_full()
                            .pt_2p5()
                            .border_t_1()
                            .border_color(tokens.border)
                            .items_center()
                            .justify_between()
                            .flex_wrap()
                            .gap_2()
                            .child(
                                h_flex()
                                    .flex_wrap()
                                    .gap_1p5()
                                    .items_center()
                                    .child(
                                        subtle_action_chip(
                                            "open-srv-panel",
                                            IconName::ExternalLink,
                                            "打开 Web 面板",
                                            tokens,
                                        )
                                        .on_click(move |_, _, _| {
                                            let _ = open::that(&console_url);
                                        }),
                                    )
                                    .child(
                                        subtle_action_chip(
                                            "edit-srv-key",
                                            IconName::Settings,
                                            "更新凭证 / 配置",
                                            tokens,
                                        )
                                        .on_click(cx.listener(move |this, _, window, cx| {
                                            this.open_add_server_form(
                                                Some(&session_for_edit),
                                                window,
                                                cx,
                                            );
                                        })),
                                    )
                                    .child(
                                        subtle_action_chip(
                                            "view-srv-logs",
                                            IconName::SquareTerminal,
                                            "运行日志",
                                            tokens,
                                        )
                                        .on_click(cx.listener(|this, _, _, cx| {
                                            this.detail_mode = DetailMode::Logs;
                                            cx.notify();
                                        })),
                                    ),
                            )
                            .child(
                                div()
                                    .id("remove-srv-btn")
                                    .flex()
                                    .items_center()
                                    .gap_1()
                                    .px_2()
                                    .py_1()
                                    .rounded(px(7.))
                                    .cursor_pointer()
                                    .text_size(px(11.5))
                                    .text_color(tokens.text_muted)
                                    .hover(|s| {
                                        s.bg(tokens.danger_soft).text_color(tokens.danger)
                                    })
                                    .on_click(cx.listener(move |this, _, window, cx| {
                                        let _ = this.supervisor.forget_profile(&pid_delete);
                                        this.state = this.supervisor.view_state();
                                        if this.state.sessions.is_empty() {
                                            this.open_add_server_form(None, window, cx);
                                        } else {
                                            this.detail_mode = DetailMode::SessionDetail;
                                            cx.notify();
                                        }
                                    }))
                                    .child(Icon::new(IconName::Delete).xsmall())
                                    .child("移除"),
                            ),
                    ),
            )
            // Provisioned Tunnels Group Card
            .child(self.render_tunnels_group(
                &snap.tunnels,
                is_connected,
                &pid_refresh,
                tokens,
                cx,
            ))
    }

    fn render_tunnels_group(
        &self,
        tunnels: &[SidecarTunnelSnapshot],
        is_connected: bool,
        profile_id: &str,
        tokens: &SurfaceTokens,
        cx: &mut Context<Self>,
    ) -> impl IntoElement {
        let pid = profile_id.to_string();

        v_flex()
            .w_full()
            .gap_2()
            .child(
                h_flex()
                    .px_1()
                    .items_center()
                    .justify_between()
                    .child(
                        div()
                            .text_size(px(12.))
                            .font_weight(FontWeight::SEMIBOLD)
                            .text_color(tokens.text_secondary)
                            .child(format!("下发至本机的隧道 ({})", tunnels.len())),
                    )
                    .when(is_connected, |el| {
                        el.child(
                            div()
                                .id("sync-snap-btn")
                                .px_2()
                                .py_0p5()
                                .rounded(px(6.))
                                .cursor_pointer()
                                .text_size(px(11.))
                                .text_color(tokens.text_muted)
                                .hover(|s| {
                                    s.bg(tokens.subtle_fill).text_color(tokens.text_primary)
                                })
                                .on_click(cx.listener(move |this, _, window, cx| {
                                    this.supervisor.request_snapshot(&pid);
                                    this.push_toast(
                                        NotificationType::Info,
                                        "已请求同步隧道状态",
                                        window,
                                        cx,
                                    );
                                }))
                                .child("同步快照"),
                        )
                    }),
            )
            .child(
                v_flex()
                    .w_full()
                    .rounded(px(14.))
                    .bg(tokens.card_bg)
                    .border_1()
                    .border_color(tokens.border)
                    .shadow(tokens.card_shadow())
                    .overflow_hidden()
                    .map(|card| {
                        if tunnels.is_empty() {
                            card.child(
                                v_flex()
                                    .w_full()
                                    .py_7()
                                    .px_4()
                                    .items_center()
                                    .gap_1()
                                    .child(
                                        div()
                                            .text_size(px(13.))
                                            .font_weight(FontWeight::MEDIUM)
                                            .text_color(tokens.text_secondary)
                                            .child(if is_connected {
                                                "该服务器暂未向本机分配隧道"
                                            } else {
                                                "连接建立后将自动展示隧道端点"
                                            }),
                                    )
                                    .child(
                                        div()
                                            .text_size(px(11.5))
                                            .text_color(tokens.text_muted)
                                            .child("在服务端 Web 面板创建 TCP / UDP / SOCKS5 等隧道后即时生效"),
                                    ),
                            )
                        } else {
                            let total = tunnels.len();
                            card.children(tunnels.iter().cloned().enumerate().map(
                                |(idx, tunnel)| {
                                    self.render_tunnel_row(tunnel, idx + 1 < total, tokens, cx)
                                },
                            ))
                        }
                    }),
            )
    }

    fn render_tunnel_row(
        &self,
        tunnel: SidecarTunnelSnapshot,
        has_divider: bool,
        tokens: &SurfaceTokens,
        cx: &mut Context<Self>,
    ) -> impl IntoElement {
        let (path_bg, path_fg, path_label) = match tunnel.effective_path.as_str() {
            "peer_direct" => (tokens.online_soft, tokens.online, "P2P 直连"),
            "server_relay" => (tokens.accent_soft, tokens.accent, "Server 中继"),
            _ => (tokens.subtle_fill, tokens.text_muted, "待协商"),
        };

        let endpoint = tunnel.local_endpoint.clone();
        let copyable = !endpoint.is_empty() && endpoint != "SOCKS5 Connect";
        let role_desc = if tunnel.role == "ingress" {
            "本机监听入口"
        } else {
            "转发至本机目标"
        };
        let row_id = SharedString::from(format!("tun-row-{}-{}", tunnel.tunnel_id, tunnel.role));

        div()
            .id(row_id)
            .w_full()
            .px_4()
            .py_3()
            .when(has_divider, |el| {
                el.border_b_1().border_color(tokens.border)
            })
            .when(copyable, |el| {
                let ep = endpoint.clone();
                let name = tunnel.display_name().to_string();
                el.cursor_pointer()
                    .hover(|s| s.bg(tokens.card_hover))
                    .on_click(cx.listener(move |this, _, window, cx| {
                        this.copy_with_toast(ep.clone(), &format!(" {name} 地址"), window, cx);
                    }))
            })
            .child(
                h_flex()
                    .w_full()
                    .items_center()
                    .justify_between()
                    .gap_3()
                    .child(
                        v_flex()
                            .gap_1()
                            .overflow_hidden()
                            .child(
                                h_flex()
                                    .gap_2()
                                    .items_center()
                                    .child(
                                        div()
                                            .text_size(px(13.5))
                                            .font_weight(FontWeight::SEMIBOLD)
                                            .text_color(tokens.text_primary)
                                            .child(tunnel.display_name().to_string()),
                                    )
                                    .child(badge_chip(
                                        tunnel.endpoint_kind_label(),
                                        tokens.subtle_fill,
                                        tokens.text_secondary,
                                    ))
                                    .child(badge_chip(path_label, path_bg, path_fg)),
                            )
                            .child(
                                div()
                                    .text_size(px(11.5))
                                    .text_color(tokens.text_muted)
                                    .child(if endpoint.is_empty() {
                                        role_desc.to_string()
                                    } else {
                                        format!("{role_desc} · {endpoint}")
                                    }),
                            ),
                    )
                    .child(
                        h_flex()
                            .gap_2()
                            .items_center()
                            .when(tunnel.role == "ingress" && tunnel.active_connections > 0, |el| {
                                el.child(
                                    div()
                                        .text_size(px(11.))
                                        .font_weight(FontWeight::MEDIUM)
                                        .text_color(tokens.online)
                                        .child(format!("{} 路活跃", tunnel.active_connections)),
                                )
                            })
                            .when(copyable, |el| {
                                el.child(
                                    h_flex()
                                        .gap_1()
                                        .items_center()
                                        .text_size(px(11.))
                                        .text_color(tokens.text_muted)
                                        .child(Icon::new(IconName::Copy).xsmall())
                                        .child("复制"),
                                )
                            }),
                    ),
            )
    }

    // ─────────────────────────── Right Pane 2: Smart Command & Add Server ───────────────────────────

    fn render_add_or_edit_server(
        &self,
        editing_profile_id: Option<String>,
        tokens: &SurfaceTokens,
        cx: &mut Context<Self>,
    ) -> impl IntoElement {
        let editing_session = editing_profile_id.as_deref().and_then(|id| {
            self.state
                .sessions
                .iter()
                .find(|s| s.profile.id == id)
                .cloned()
        });
        let is_editing = editing_session.is_some();
        let server_input_val = self.server_input.read(cx).value().trim().to_string();
        let key_input_val = self.key_input.read(cx).value().trim().to_string();

        let server_changed_while_editing = editing_session.as_ref().is_some_and(|orig| {
            !normalize_server_compare(&orig.profile.server)
                .eq_ignore_ascii_case(&normalize_server_compare(&server_input_val))
        });
        let can_reuse_saved_auth = editing_session
            .as_ref()
            .is_some_and(|orig| orig.has_saved_token && !server_changed_while_editing);

        let can_submit = match self.entry_tab {
            OnboardingEntryTab::SmartCommand => self.parsed_preview.is_some(),
            OnboardingEntryTab::ManualFields => {
                !server_input_val.is_empty() && (!key_input_val.is_empty() || can_reuse_saved_auth)
            }
        };
        let submit_edit_id = editing_profile_id.clone();

        v_flex()
            .w_full()
            .min_w_0()
            .gap_3p5()
            .child(
                h_flex()
                    .w_full()
                    .min_w_0()
                    .items_start()
                    .justify_between()
                    .gap_3()
                    .child(
                        v_flex()
                            .flex_1()
                            .min_w_0()
                            .gap_0p5()
                            .child(
                                div()
                                    .text_size(px(17.))
                                    .font_weight(FontWeight::BOLD)
                                    .text_color(tokens.text_primary)
                                    .child(if is_editing {
                                        "更新服务器凭证与配置"
                                    } else {
                                        "添加服务器连接"
                                    }),
                            )
                            .child(
                                div()
                                    .w_full()
                                    .text_size(px(12.))
                                    .text_color(tokens.text_secondary)
                                    .child(
                                        "粘贴服务端「添加客户端」生成的安装或启动命令自动识别参数；支持多个服务器同时在线。",
                                    ),
                            ),
                    )
                    .when(!self.state.sessions.is_empty(), |el| {
                        el.child(
                            div().flex_shrink_0().child(
                                subtle_action_chip(
                                    "cancel-add-srv",
                                    IconName::ArrowLeft,
                                    "返回详情",
                                    tokens,
                                )
                                .on_click(cx.listener(|this, _, _, cx| {
                                    this.detail_mode = DetailMode::SessionDetail;
                                    cx.notify();
                                })),
                            ),
                        )
                    }),
            )
            // Segmented Mode Switcher: [ 一键命令识别 ] | [ 手动填写参数 ]
            .child(
                h_flex()
                    .p_1()
                    .gap_1()
                    .rounded(px(10.))
                    .bg(tokens.sidebar_bg)
                    .border_1()
                    .border_color(tokens.border)
                    .w(px(280.))
                    .child(segmented_tab(
                        "seg-smart-cmd",
                        "智能识别连接命令",
                        self.entry_tab == OnboardingEntryTab::SmartCommand,
                        tokens,
                        cx.listener(|this, _, _, cx| {
                            this.entry_tab = OnboardingEntryTab::SmartCommand;
                            cx.notify();
                        }),
                    ))
                    .child(segmented_tab(
                        "seg-manual-fields",
                        "手动填写参数",
                        self.entry_tab == OnboardingEntryTab::ManualFields,
                        tokens,
                        cx.listener(|this, _, _, cx| {
                            this.entry_tab = OnboardingEntryTab::ManualFields;
                            cx.notify();
                        }),
                    )),
            )
            // Smart Command Import Card
            .when(self.entry_tab == OnboardingEntryTab::SmartCommand, |el| {
                el.child(
                    v_flex()
                        .w_full()
                        .min_w_0()
                        .p_4()
                        .gap_3()
                        .rounded(px(14.))
                        .bg(tokens.card_bg)
                        .border_1()
                        .border_color(tokens.border)
                        .shadow(tokens.card_shadow())
                        .child(
                            h_flex()
                                .w_full()
                                .min_w_0()
                                .items_center()
                                .justify_between()
                                .gap_2()
                                .child(
                                    div()
                                        .flex_1()
                                        .min_w_0()
                                        .text_size(px(12.5))
                                        .font_weight(FontWeight::SEMIBOLD)
                                        .text_color(tokens.text_primary)
                                        .truncate()
                                        .child("首次连接命令（curl / CLI / Docker / Compose）"),
                                )
                                .child(
                                    div().flex_shrink_0().child(
                                        subtle_action_chip(
                                            "paste-from-clip-btn",
                                            IconName::Copy,
                                            "粘贴并识别",
                                            tokens,
                                        )
                                        .on_click(cx.listener(|this, _, window, cx| {
                                            this.paste_clipboard_command(window, cx);
                                        })),
                                    ),
                                ),
                        )
                        .child(Textarea::new(&self.command_textarea).h(px(120.)))
                        .child(self.render_parsed_preview(tokens)),
                )
            })
            // Manual Parameter Card (ONLY shown in ManualFields tab — never appended under SmartCommand)
            .when(self.entry_tab == OnboardingEntryTab::ManualFields, |el| {
                    el.child(
                        v_flex()
                            .w_full()
                            .min_w_0()
                            .p_4()
                            .gap_3()
                            .rounded(px(14.))
                            .bg(tokens.card_bg)
                            .border_1()
                            .border_color(tokens.border)
                            .shadow(tokens.card_shadow())
                            .child(field_row(
                                "服务器地址",
                                Input::new(&self.server_input)
                                    .cleanable(true)
                                    .content_type(InputContentType::Url),
                                tokens,
                            ))
                            .child(field_row(
                                if is_editing {
                                    if server_changed_while_editing {
                                        "访问凭证 (Key) · 地址已变更，需填写新凭证"
                                    } else if can_reuse_saved_auth {
                                        "访问凭证 (Key) · 留空则继续使用原有认证"
                                    } else {
                                        "访问凭证 (Key)"
                                    }
                                } else {
                                    "访问凭证 (Key)"
                                },
                                Input::new(&self.key_input)
                                    .mask_toggle()
                                    .content_type(InputContentType::Password),
                                tokens,
                            ))
                            .child(field_row(
                                "备注名称",
                                Input::new(&self.name_input).cleanable(true),
                                tokens,
                            ))
                            .child(
                                div()
                                    .id("adv-tls-expander")
                                    .flex()
                                    .items_center()
                                    .justify_between()
                                    .pt_1()
                                    .cursor_pointer()
                                    .text_size(px(11.5))
                                    .text_color(tokens.text_muted)
                                    .hover(|s| s.text_color(tokens.text_primary))
                                    .on_click(cx.listener(|this, _, _, cx| {
                                        this.show_advanced_tls = !this.show_advanced_tls;
                                        cx.notify();
                                    }))
                                    .child("高级 TLS 证书选项")
                                    .child(
                                        Icon::new(if self.show_advanced_tls {
                                            IconName::ChevronUp
                                        } else {
                                            IconName::ChevronDown
                                        })
                                        .xsmall(),
                                    ),
                            )
                            .when(self.show_advanced_tls, |tls_box| {
                                tls_box.child(
                                    v_flex()
                                        .gap_2p5()
                                        .pt_2()
                                        .border_t_1()
                                        .border_color(tokens.border)
                                        .child(
                                            Switch::new("tls-skip-switch")
                                                .checked(self.tls_skip_verify)
                                                .label("跳过 CA 链验证（使用首次连接 TOFU 指纹保护）")
                                                .on_change(cx.listener(|this, checked, _, cx| {
                                                    this.tls_skip_verify = *checked;
                                                    cx.notify();
                                                })),
                                        )
                                        .child(field_row(
                                            "固定证书 SHA-256 指纹（可选）",
                                            Input::new(&self.fingerprint_input).cleanable(true),
                                            tokens,
                                        )),
                                )
                            }),
                    )
                },
            )
            .child(
                h_flex().justify_end().child(
                    accent_button(
                        "confirm-connect-srv-btn",
                        if is_editing {
                            "保存配置"
                        } else {
                            "验证并连接此服务器"
                        },
                        tokens,
                        cx,
                    )
                    .disabled(!can_submit)
                    .on_click(cx.listener(move |this, _, window, cx| {
                        this.submit_server_form(submit_edit_id.clone(), window, cx);
                    })),
                ),
            )
    }

    fn render_parsed_preview(&self, tokens: &SurfaceTokens) -> Div {
        let (dot_color, text_color, status_line) = match &self.parsed_preview {
            Some(p) => (
                tokens.online,
                tokens.online,
                format!(
                    "已就绪：目标节点 {}",
                    p.server.as_deref().unwrap_or_default()
                ),
            ),
            None => (
                tokens.text_muted,
                tokens.text_muted,
                "在服务端 Web 面板复制「首次连接命令」后粘贴于此，识别成功后直接连接。".to_string(),
            ),
        };

        h_flex()
            .w_full()
            .min_w_0()
            .h(px(22.))
            .items_center()
            .gap_2()
            .child(div().size(px(6.)).rounded(px(99.)).flex_shrink_0().bg(dot_color))
            .child(
                div()
                    .flex_1()
                    .min_w_0()
                    .text_size(px(11.5))
                    .text_color(text_color)
                    .truncate()
                    .child(status_line),
            )
    }

    // ─────────────────────────── Right Pane 3: Logs ───────────────────────────

    fn render_logs_view(
        &self,
        session: &ProfileSessionView,
        tokens: &SurfaceTokens,
        cx: &mut Context<Self>,
    ) -> impl IntoElement {
        let logs = session.recent_logs.clone();

        v_flex()
            .w_full()
            .min_w_0()
            .gap_3()
            .child(
                h_flex()
                    .w_full()
                    .min_w_0()
                    .items_center()
                    .justify_between()
                    .gap_2()
                    .child(
                        v_flex()
                            .flex_1()
                            .min_w_0()
                            .gap_0p5()
                            .child(
                                div()
                                    .text_size(px(16.))
                                    .font_weight(FontWeight::BOLD)
                                    .text_color(tokens.text_primary)
                                    .child(format!("{} · 运行日志", session.profile.display_title())),
                            )
                            .child(
                                div()
                                    .text_size(px(11.5))
                                    .text_color(tokens.text_muted)
                                    .child(match session.pid {
                                        Some(pid) => format!(
                                            "当前会话日志（最多保留最近 80 条） · PID {pid}"
                                        ),
                                        None => "当前会话日志（每次新连接自动重置，最多保留 80 条）"
                                            .to_string(),
                                    }),
                            ),
                    )
                    .child(
                        h_flex()
                            .gap_1p5()
                            .when(!logs.is_empty(), |el| {
                                let pid_clear = session.profile.id.clone();
                                el.child(
                                    subtle_action_chip(
                                        "clear-srv-logs",
                                        IconName::Delete,
                                        "清空",
                                        tokens,
                                    )
                                    .on_click(cx.listener(move |this, _, window, cx| {
                                        this.supervisor.clear_logs(&pid_clear);
                                        this.reload_from_supervisor(cx);
                                        this.push_toast(
                                            NotificationType::Info,
                                            "已清空当前节点日志",
                                            window,
                                            cx,
                                        );
                                    })),
                                )
                            })
                            .child(
                                subtle_action_chip(
                                    "copy-srv-logs",
                                    IconName::Copy,
                                    "复制日志",
                                    tokens,
                                )
                                .on_click({
                                    let logs_copy = logs.clone();
                                    cx.listener(move |this, _, window, cx| {
                                        let text = logs_copy
                                            .iter()
                                            .map(|l| {
                                                format!(
                                                    "[{}] [{}] {} | {}",
                                                    l.timestamp, l.level, l.event, l.message
                                                )
                                            })
                                            .collect::<Vec<_>>()
                                            .join("\n");
                                        this.copy_with_toast(text, "节点日志", window, cx);
                                    })
                                }),
                            )
                            .child(
                                subtle_action_chip(
                                    "back-from-logs",
                                    IconName::ArrowLeft,
                                    "返回详情",
                                    tokens,
                                )
                                .on_click(cx.listener(|this, _, _, cx| {
                                    this.detail_mode = DetailMode::SessionDetail;
                                    cx.notify();
                                })),
                            ),
                    ),
            )
            .child(
                v_flex()
                    .w_full()
                    .p_3()
                    .gap_1()
                    .rounded(px(14.))
                    .bg(tokens.card_bg)
                    .border_1()
                    .border_color(tokens.border)
                    .shadow(tokens.card_shadow())
                    .children(if logs.is_empty() {
                        vec![div()
                            .py_8()
                            .text_center()
                            .text_size(px(12.))
                            .text_color(tokens.text_muted)
                            .child("暂无运行事件")
                            .into_any_element()]
                    } else {
                        logs.into_iter()
                            .rev()
                            .take(100)
                            .map(|entry| {
                                let dot = match entry.level.as_str() {
                                    "error" => tokens.danger,
                                    "warn" => tokens.warning,
                                    _ => tokens.online,
                                };
                                h_flex()
                                    .py_1()
                                    .gap_2()
                                    .border_b_1()
                                    .border_color(tokens.subtle_fill)
                                    .items_center()
                                    .child(div().size(px(5.)).rounded(px(99.)).flex_shrink_0().bg(dot))
                                    .child(
                                        div()
                                            .text_size(px(11.))
                                            .text_color(tokens.text_muted)
                                            .flex_shrink_0()
                                            .child(entry.timestamp),
                                    )
                                    .child(
                                        div()
                                            .text_size(px(11.))
                                            .font_weight(FontWeight::MEDIUM)
                                            .text_color(tokens.text_secondary)
                                            .flex_shrink_0()
                                            .child(entry.event),
                                    )
                                    .child(
                                        div()
                                            .text_size(px(11.5))
                                            .text_color(tokens.text_primary)
                                            .truncate()
                                            .child(entry.message),
                                    )
                                    .into_any_element()
                            })
                            .collect()
                    }),
            )
    }
}

impl Render for NetsGoDesktopView {
    fn render(&mut self, _: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        let tokens = SurfaceTokens::resolve(cx);
        let selected = self.state.selected_session().cloned();

        v_flex()
            .size_full()
            .bg(tokens.window_bg)
            .text_color(tokens.text_primary)
            .child(self.render_titlebar(&tokens, cx))
            .child(
                h_flex()
                    .flex_1()
                    .w_full()
                    .overflow_hidden()
                    .child(self.render_server_rail(&tokens, cx))
                    .child(
                        div()
                            .flex_1()
                            .min_w_0()
                            .h_full()
                            .overflow_hidden()
                            .child(
                                div()
                                    .id("detail-pane-scroll")
                                    .w_full()
                                    .min_w_0()
                                    .h_full()
                                    .p_5()
                                    .overflow_y_scrollbar()
                                    .child(match (&self.detail_mode, &selected) {
                                        (DetailMode::Updates, _) => self.render_updates(&tokens, cx).into_any_element(),
                                        (
                                            DetailMode::AddOrEditServer { editing_profile_id },
                                            _,
                                        ) => self
                                            .render_add_or_edit_server(
                                                editing_profile_id.clone(),
                                                &tokens,
                                                cx,
                                            )
                                            .into_any_element(),
                                        (DetailMode::Logs, Some(session)) => self
                                            .render_logs_view(session, &tokens, cx)
                                            .into_any_element(),
                                        (_, Some(session)) => self
                                            .render_session_detail(session, &tokens, cx)
                                            .into_any_element(),
                                        (_, None) => self
                                            .render_add_or_edit_server(None, &tokens, cx)
                                            .into_any_element(),
                                    }),
                            ),
                    ),
            )
    }
}

// ─────────────────────────── Native UI Helpers ───────────────────────────

fn primary_switch_button(
    is_running: bool,
    phase: &ConnectionPhase,
    tokens: &SurfaceTokens,
    cx: &App,
) -> Button {
    if is_running {
        let variant = ButtonCustomVariant::new(cx)
            .color(tokens.subtle_fill)
            .hover(tokens.danger_soft)
            .active(tokens.danger_soft)
            .foreground(tokens.text_primary);
        Button::new("detail-disconnect-btn")
            .custom(variant)
            .label(if *phase == ConnectionPhase::Connecting {
                "取消连接"
            } else {
                "断开连接"
            })
    } else {
        accent_button("detail-connect-btn", "连接服务器", tokens, cx)
    }
}

fn accent_button(
    id: &'static str,
    label: &'static str,
    tokens: &SurfaceTokens,
    cx: &App,
) -> Button {
    let variant = ButtonCustomVariant::new(cx)
        .color(tokens.accent)
        .hover(tokens.accent_hover)
        .active(tokens.accent)
        .foreground(rgb(0xFFFFFF).into())
        .shadow(true);
    Button::new(id).custom(variant).label(label)
}

fn subtle_action_chip(
    id: &'static str,
    icon: IconName,
    label: &'static str,
    tokens: &SurfaceTokens,
) -> Stateful<Div> {
    div()
        .id(id)
        .flex()
        .items_center()
        .gap_1p5()
        .px_2p5()
        .py_1()
        .rounded(px(7.))
        .cursor_pointer()
        .bg(tokens.subtle_fill)
        .text_size(px(11.5))
        .font_weight(FontWeight::MEDIUM)
        .text_color(tokens.text_secondary)
        .hover(|s| s.bg(tokens.card_hover).text_color(tokens.text_primary))
        .child(Icon::new(icon).xsmall())
        .child(label)
}

fn segmented_tab(
    id: &'static str,
    label: &'static str,
    active: bool,
    tokens: &SurfaceTokens,
    on_click: impl Fn(&ClickEvent, &mut Window, &mut App) + 'static,
) -> Stateful<Div> {
    div()
        .id(id)
        .flex_1()
        .py_1()
        .px_2()
        .rounded(px(7.))
        .cursor_pointer()
        .text_center()
        .text_size(px(12.))
        .font_weight(if active {
            FontWeight::SEMIBOLD
        } else {
            FontWeight::MEDIUM
        })
        .bg(if active {
            tokens.card_bg
        } else {
            gpui_kit::transparent_black()
        })
        .text_color(if active {
            tokens.text_primary
        } else {
            tokens.text_secondary
        })
        .when(active, |el| el.shadow(tokens.card_shadow()))
        .on_click(on_click)
        .child(label)
}

fn badge_chip(text: &str, bg: Hsla, fg: Hsla) -> Div {
    div()
        .px_2()
        .py_0p5()
        .rounded(px(99.))
        .bg(bg)
        .text_size(px(10.5))
        .font_weight(FontWeight::SEMIBOLD)
        .text_color(fg)
        .child(text.to_string())
}

fn kv_cell(label: &'static str, value: String, tokens: &SurfaceTokens) -> Div {
    v_flex()
        .gap_0p5()
        .child(
            div()
                .text_size(px(10.5))
                .text_color(tokens.text_muted)
                .child(label),
        )
        .child(
            div()
                .text_size(px(12.5))
                .font_weight(FontWeight::SEMIBOLD)
                .text_color(tokens.text_primary)
                .child(value),
        )
}

fn field_row(label: &'static str, control: impl IntoElement, tokens: &SurfaceTokens) -> Div {
    v_flex()
        .gap_1()
        .child(
            div()
                .text_size(px(11.5))
                .font_weight(FontWeight::MEDIUM)
                .text_color(tokens.text_secondary)
                .child(label),
        )
        .child(control)
}


// ─────────────────────────── Window & App Lifecycle ───────────────────────────

fn default_window_options(cx: &App) -> WindowOptions {
    WindowOptions {
        window_bounds: Some(WindowBounds::Windowed(Bounds::centered(
            None,
            size(px(780.), px(540.)),
            cx,
        ))),
        window_min_size: Some(size(px(660.), px(460.))),
        is_resizable: true,
        is_minimizable: true,
        ..TitleBar::window_options()
    }
}

type MainWindowSlot = Rc<RefCell<Option<(AnyWindowHandle, Entity<NetsGoDesktopView>)>>>;

fn ensure_main_window(
    window_slot: &MainWindowSlot,
    supervisor: &ClientSupervisor,
    updater: &DesktopUpdater,
    cx: &mut App,
) {
    let already_open = window_slot
        .borrow()
        .as_ref()
        .and_then(|(handle, _)| handle.update(cx, |_, window, _| window.activate_window()).ok())
        .is_some();

    if already_open {
        cx.activate(true);
        return;
    }

    let sup = supervisor.clone();
    let updater = updater.clone();
    if let Ok((handle, view)) = open_window(default_window_options(cx), cx, move |window, cx| {
        cx.new(|cx| NetsGoDesktopView::new(sup, updater, window, cx))
    }) {
        *window_slot.borrow_mut() = Some((handle, view));
        cx.activate(true);
    }
}

fn main() {
    velopack::VelopackApp::build().set_auto_apply_on_startup(false).run();
    if std::env::args().nth(1).as_deref() == Some("--version") {
        println!("NetsGo Desktop {}", netsgo_desktop_updater::BUILD_VERSION);
        return;
    }
    let updater = DesktopUpdater::new();
    let args: Vec<String> = std::env::args().collect();
    let resume_profiles = args.windows(2).find(|pair| pair[0] == "--netsgo-update-resume")
        .and_then(|pair| serde_json::from_str::<Vec<String>>(&pair[1]).ok());
    let data_dir = default_desktop_data_dir();
    let (supervisor, notify_rx) =
        ClientSupervisor::new(data_dir).expect("failed to initialize NetsGo desktop supervisor");

    application()
        .with_assets(assets::AllAssets)
        .with_quit_mode(QuitMode::Explicit)
        .run(move |cx| {
            init(cx);
            let initial = supervisor.view_state();
            apply_appearance_preference(initial.appearance, None, cx);

            let tray = DesktopTray::build(initial.connected_count, initial.sessions.len());

            let window_slot: MainWindowSlot = Rc::new(RefCell::new(None));
            ensure_main_window(&window_slot, &supervisor, &updater, cx);

            let sup_for_quit = supervisor.clone();
            cx.on_app_quit(move |_| {
                let sup = sup_for_quit.clone();
                async move {
                    let _ = sup.disconnect_all();
                }
            })
            .detach();

            if let Some(ids) = resume_profiles {
                let sup_resume = supervisor.clone();
                cx.background_executor().spawn(async move {
                    for id in ids { let _ = sup_resume.reconnect_profile(&id); }
                }).detach();
            } else if initial.auto_reconnect_on_launch {
                let sup_auto = supervisor.clone();
                cx.background_executor()
                    .spawn(async move {
                        sup_auto.reconnect_all_saved();
                    })
                    .detach();
            }

            let sup_poll = supervisor.clone();
            let slot_poll = Rc::clone(&window_slot);
            cx.spawn(async move |cx| {
                let mut tick: u64 = 0;
                let mut notified_version: Option<String> = None;
                loop {
                    cx.background_executor()
                        .timer(Duration::from_millis(80))
                        .await;
                    tick = tick.wrapping_add(1);
                    updater.tick();

                    let mut dirty = false;
                    let mut pending_errors: Vec<(String, String)> = Vec::new();
                    loop {
                        match notify_rx.try_recv() {
                            Ok(SupervisorNotification::Updated) => dirty = true,
                            Ok(SupervisorNotification::SessionError {
                                profile_title,
                                message,
                            }) => {
                                dirty = true;
                                pending_errors.push((profile_title, message));
                            }
                            Err(TryRecvError::Empty) => break,
                            Err(TryRecvError::Disconnected) => return,
                        }
                    }

                    let tray_cmds = drain_tray_actions();
                    if dirty || !tray_cmds.is_empty() || tick.is_multiple_of(12) {
                        let should_quit = cx.update(|cx| {
                            let update = updater.view();
                            if update.status == UpdateStatus::Restarting { return true; }
                            if let Some((handle, view)) = slot_poll.borrow().as_ref() {
                                let handle = *handle;
                                let view = view.clone();
                                if update.status == UpdateStatus::Available && update.available_version != notified_version {
                                    let message = format!("NetsGo {} 已发布，点击左下角版本号查看更新。", update.available_version.as_deref().unwrap_or_default());
                                    let _ = handle.update(cx, |_, window, cx| {
                                        view.update(cx, |this, cx| this.push_toast(NotificationType::Info, message, window, cx));
                                    });
                                    notified_version = update.available_version;
                                }
                                for (title, err_msg) in &pending_errors {
                                    let formatted = format!("{title}：{err_msg}");
                                    let _ = handle.update(cx, |_, window, cx| {
                                        view.update(cx, |this, cx| {
                                            this.push_toast(
                                                NotificationType::Error,
                                                formatted,
                                                window,
                                                cx,
                                            );
                                        });
                                    });
                                }
                            }
                            for cmd in tray_cmds {
                                match cmd {
                                    TrayCommand::ShowWindow => {
                                        ensure_main_window(&slot_poll, &sup_poll, &updater, cx);
                                    }
                                    TrayCommand::CheckUpdate => {
                                        ensure_main_window(&slot_poll, &sup_poll, &updater, cx);
                                        updater.check();
                                        if let Some((_, view)) = slot_poll.borrow().as_ref() {
                                            view.update(cx, |this, cx| { this.detail_mode = DetailMode::Updates; cx.notify(); });
                                        }
                                    }
                                    TrayCommand::ToggleAll => {
                                        let s = sup_poll.clone();
                                        if s.has_any_running() {
                                            cx.background_executor()
                                                .spawn(async move {
                                                    let _ = s.disconnect_all();
                                                })
                                                .detach();
                                        } else {
                                            cx.background_executor()
                                                .spawn(async move {
                                                    s.reconnect_all_saved();
                                                })
                                                .detach();
                                        }
                                    }
                                    TrayCommand::Quit => {
                                        return true;
                                    }
                                }
                            }

                            let latest = sup_poll.view_state();
                            if let Some(tray_ref) = tray.as_ref() {
                                tray_ref.sync_summary(
                                    latest.connected_count,
                                    latest.running_count,
                                    latest.sessions.len(),
                                    latest.total_tunnels,
                                );
                            }

                            if let Some((_, view)) = slot_poll.borrow().as_ref() {
                                let view = view.clone();
                                view.update(cx, |this, cx| {
                                    this.reload_from_supervisor(cx);
                                });
                            }
                            false
                        });

                        if should_quit {
                            let sup = sup_poll.clone();
                            cx.background_executor().spawn(async move { let _ = sup.disconnect_all(); }).await;
                            cx.update(|cx| cx.quit());
                            return;
                        }
                    }
                }
            })
            .detach();
        });
}
