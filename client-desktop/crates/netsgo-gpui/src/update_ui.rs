use super::*;

impl NetsGoDesktopView {
    pub(super) fn render_version_footer(
        &self,
        tokens: &SurfaceTokens,
        cx: &mut Context<Self>,
    ) -> impl IntoElement {
        let update = &self.update_state;
        let label = match update.status {
            UpdateStatus::Checking => "检查中…".to_owned(),
            UpdateStatus::Downloading => format!("下载 {}%", update.progress),
            UpdateStatus::Ready => "重启更新".to_owned(),
            UpdateStatus::Installing | UpdateStatus::Restarting => "正在更新…".to_owned(),
            _ if update.available_version.is_some() => "有新版本".to_owned(),
            UpdateStatus::Failed => "检查失败".to_owned(),
            _ => "".to_owned(),
        };
        let color = if update.available_version.is_some() {
            tokens.accent
        } else {
            tokens.text_muted
        };
        h_flex().gap_1().pt_2().border_t_1().border_color(tokens.border).child(
            h_flex()
                .id("desktop-version")
                .flex_1().min_w_0()
                .px_2()
                .py_2()
                .rounded(px(7.))
                .justify_between()
                .gap_2()
                .cursor_pointer()
                .hover(|s| s.bg(tokens.subtle_fill))
                .on_click(cx.listener(|this, _, _, cx| {
                    this.detail_mode = DetailMode::Updates;
                    if this.update_state.status == UpdateStatus::Idle {
                        this.updater.check();
                    }
                    this.update_state = this.updater.view();
                    cx.notify();
                }))
                .child(
                    div()
                        .text_size(px(11.))
                        .text_color(tokens.text_secondary)
                        .truncate()
                        .child(update.current_version.clone()),
                )
                .child(
                    div()
                        .text_size(px(10.5))
                        .flex_shrink_0()
                        .text_color(color)
                        .child(label),
                ),
        ).child(Button::new("desktop-settings").label("设置").small()
            .on_click(cx.listener(|this, _, _, cx| { this.detail_mode = DetailMode::Settings; cx.notify(); })))
    }

    pub(super) fn render_updates(
        &self,
        tokens: &SurfaceTokens,
        cx: &mut Context<Self>,
    ) -> impl IntoElement {
        let update = &self.update_state;
        let status = match update.status {
            UpdateStatus::Development => "开发版本不参与自动更新".to_owned(),
            UpdateStatus::Idle => "检查是否有可用的新版本".to_owned(),
            UpdateStatus::Checking => "正在检查更新…".to_owned(),
            UpdateStatus::UpToDate => "当前已是最新版本".to_owned(),
            UpdateStatus::Available => format!(
                "新版本 {} 可用",
                update.available_version.as_deref().unwrap_or_default()
            ),
            UpdateStatus::Downloading => format!("正在下载更新 · {}%", update.progress),
            UpdateStatus::Ready => "更新已准备好，可选择现在重启".to_owned(),
            UpdateStatus::Installing | UpdateStatus::Restarting => "正在准备重启更新…".to_owned(),
            UpdateStatus::Failed => "更新未完成，可稍后重试".to_owned(),
        };
        v_flex()
            .w_full()
            .gap_5()
            .child(
                v_flex()
                    .gap_2()
                    .child(
                        div()
                            .text_size(px(22.))
                            .font_weight(FontWeight::BOLD)
                            .child("版本与更新"),
                    )
                    .child(
                        div()
                            .text_size(px(12.))
                            .text_color(tokens.text_secondary)
                            .child(format!("NetsGo {}", update.current_version)),
                    ),
            )
            .child(
                v_flex()
                    .p_4()
                    .gap_3()
                    .rounded(px(12.))
                    .bg(tokens.card_bg)
                    .border_1()
                    .border_color(tokens.border)
                    .child(
                        div()
                            .text_size(px(14.))
                            .font_weight(FontWeight::SEMIBOLD)
                            .child(status),
                    )
                    .when(update.status == UpdateStatus::Downloading, |el| {
                        el.child(
                            div()
                                .h(px(5.))
                                .w_full()
                                .rounded(px(3.))
                                .bg(tokens.subtle_fill)
                                .child(
                                    div()
                                        .h_full()
                                        .w(relative(update.progress as f32 / 100.))
                                        .rounded(px(3.))
                                        .bg(tokens.accent),
                                ),
                        )
                    })
                    .when_some(update.error.clone(), |el, error| {
                        el.child(
                            div()
                                .text_size(px(12.))
                                .text_color(tokens.danger)
                                .child(error),
                        )
                    })
                    .when(
                        !update.can_install && update.status != UpdateStatus::Development,
                        |el| {
                            el.child(
                                div()
                                    .text_size(px(12.))
                                    .text_color(tokens.text_secondary)
                                    .child(
                                    "当前程序未通过安装包安装，请安装正式版本以启用应用内更新。",
                                ),
                            )
                        },
                    )
                    .when(update.downloaded, |el| {
                        el.child(
                            div()
                                .text_size(px(12.))
                                .text_color(tokens.text_secondary)
                                .child(
                                "重启会暂时中断所有隧道。更新后将尝试恢复当前连接，配置会保留。",
                            ),
                        )
                    })
                    .child(
                        h_flex()
                            .gap_2()
                            .child(
                                Button::new("check-desktop-update")
                                    .label("检查更新")
                                    .small()
                                    .disabled(
                                        update.status.busy()
                                            || update.status == UpdateStatus::Development
                                            || update.downloaded,
                                    )
                                    .on_click(cx.listener(|this, _, _, cx| {
                                        this.updater.check();
                                        this.update_state = this.updater.view();
                                        cx.notify();
                                    })),
                            )
                            .when(
                                update.available_version.is_some() && update.can_install,
                                |el| {
                                    el.child(
                                        Button::new("apply-desktop-update")
                                            .label(if update.downloaded {
                                                "重启并更新"
                                            } else {
                                                "下载更新"
                                            })
                                            .primary()
                                            .small()
                                            .disabled(update.status.busy() || (update.downloaded && self.supervisor.startup_view().busy))
                                            .on_click(cx.listener(|this, _, _, cx| {
                                                if this.update_state.downloaded {
                                                    let service = this.supervisor.clone();
                                                    this.updater.install(
                                                        this.supervisor.running_profile_ids(),
                                                        move |package| service.update_for_app(package),
                                                    );
                                                } else {
                                                    this.updater.download();
                                                }
                                                this.update_state = this.updater.view();
                                                cx.notify();
                                            })),
                                    )
                                },
                            ),
                    ),
            )
            .when(!update.notes.is_empty(), |el| {
                el.child(
                    v_flex()
                        .gap_2()
                        .child(
                            div()
                                .text_size(px(12.))
                                .font_weight(FontWeight::SEMIBOLD)
                                .child("更新说明"),
                        )
                        .child(
                            div()
                                .text_size(px(12.))
                                .text_color(tokens.text_secondary)
                                .child(update.notes.clone()),
                        ),
                )
            })
            .child(
                div()
                    .text_size(px(11.))
                    .text_color(tokens.text_muted)
                    .child("自动检查每天进行一次。发现新版本后由你决定何时下载和重启。"),
            )
    }
}
