use super::*;

#[derive(Clone, Copy)]
enum StartupAction {
    Login(bool),
    Boot(bool),
    Repair,
}

impl NetsGoDesktopView {
    fn change_startup(&mut self, action: StartupAction, cx: &mut Context<Self>) {
        if self.startup_pending.is_some() {
            return;
        }
        let supervisor = self.supervisor.clone();
        let (tx, rx) = std::sync::mpsc::channel();
        self.startup_feedback = None;
        self.startup_pending = Some(rx);
        std::thread::spawn(move || {
            let result = match action {
                StartupAction::Login(enabled) => supervisor.set_login_enabled(enabled).map(|_| {
                    if enabled {
                        "已开启登录后自动启动"
                    } else {
                        "已关闭登录后自动启动"
                    }
                }),
                StartupAction::Boot(enabled) => supervisor.set_boot_enabled(enabled).map(|_| {
                    if enabled {
                        "后台服务已接管连接，未登录系统时也会自动运行"
                    } else {
                        "后台服务已停用，连接交回桌面程序管理"
                    }
                }),
                StartupAction::Repair => {
                    supervisor.repair_service().map(|_| "后台服务已修复并更新")
                }
            };
            let _ = tx.send(
                result
                    .map(str::to_owned)
                    .map_err(|error| format!("{error:#}")),
            );
        });
        cx.notify();
    }

    pub(super) fn render_startup_settings(
        &self,
        tokens: &SurfaceTokens,
        cx: &mut Context<Self>,
    ) -> impl IntoElement {
        let startup = self.supervisor.startup_view();
        let busy = startup.busy || self.startup_pending.is_some();
        let enabled = self.update_state.can_install && !busy && !self.update_state.status.busy();
        let old_service = startup
            .service_version
            .as_deref()
            .is_some_and(|version| version != netsgo_desktop_updater::BUILD_VERSION);
        v_flex().w_full().gap_5()
            .child(v_flex().gap_2()
                .child(div().text_size(px(22.)).font_weight(FontWeight::BOLD).child("启动与后台运行"))
                .child(div().text_size(px(12.)).text_color(tokens.text_secondary).child("界面启动和后台连接可以分别设置。")))
            .child(v_flex().p_4().gap_3().rounded(px(12.)).bg(tokens.card_bg).border_1().border_color(tokens.border)
                .child(Switch::new("login-autostart").label("登录系统后自动启动界面").checked(startup.login_enabled).disabled(!enabled)
                    .on_change(cx.listener(|this, checked, _, cx| this.change_startup(StartupAction::Login(*checked), cx))))
                .child(div().text_size(px(12.)).text_color(tokens.text_secondary).child("登录后常驻托盘，不自动打开主窗口。若被系统禁用，还需要在系统启动项设置中允许。")))
            .child(v_flex().p_4().gap_3().rounded(px(12.)).bg(tokens.card_bg).border_1().border_color(tokens.border)
                .child(Switch::new("boot-autostart").label("未登录系统时也自动连接").checked(startup.boot_enabled).disabled(!enabled)
                    .on_change(cx.listener(|this, checked, _, cx| this.change_startup(StartupAction::Boot(*checked), cx))))
                .child(div().text_size(px(12.)).text_color(tokens.text_secondary).child("安装系统后台服务，需要管理员授权。切换时连接会短暂重启；启用后关闭窗口或注销用户，连接仍会继续。"))
                .when_some(startup.service_version, |el, version| el.child(div().text_size(px(12.)).text_color(tokens.text_secondary).child(format!("后台服务版本：{version}"))))
                .when_some(startup.service_error.clone(), |el, error| el.child(div().text_size(px(12.)).text_color(tokens.danger).child(error)))
                .when(old_service, |el| el.child(div().text_size(px(12.)).text_color(tokens.danger).child("后台服务版本与界面不同，请更新后台服务。")))
                .when(startup.boot_enabled && (old_service || startup.service_error.is_some()), |el| el.child(
                    Button::new("repair-background-service").label("修复或更新后台服务").small().disabled(!enabled)
                        .on_click(cx.listener(|this, _, _, cx| this.change_startup(StartupAction::Repair, cx)))))
                .child(div().text_size(px(11.)).text_color(tokens.text_muted).child("启动磁盘尚未解锁时无法运行，例如 FileVault 解锁界面或 BitLocker 启动 PIN。网络可用后才会建立连接。")))
            .when(!self.update_state.can_install, |el| el.child(div().text_size(px(12.)).text_color(tokens.text_secondary).child("请从完整安装包启动 NetsGo 后再配置启动设置。")))
            .when(busy, |el| el.child(div().text_size(px(12.)).text_color(tokens.text_secondary).child("正在配置，请按系统提示完成管理员授权…")))
            .when_some(self.startup_feedback.clone(), |el, (success, message)| el.child(div().text_size(px(12.)).text_color(if success { tokens.text_secondary } else { tokens.danger }).child(message)))
    }
}
