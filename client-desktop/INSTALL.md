# NetsGo 桌面客户端安装说明

目前提供不含 Apple / Microsoft 认可的开发者身份签名的桌面包。它们不是 App Store / Microsoft Store 应用，系统可能提示无法验证开发者。更新清单仍使用 NetsGo 自有密钥签名，客户端会验证更新包的完整性。

请从 NetsGo 的 GitHub 或 CNB 官方仓库下载与系统、处理器匹配的安装包：

- GitHub：https://github.com/zsio/netsgo/releases
- CNB：https://cnb.cool/zsio/netsgo/-/releases

## macOS

1. Apple 芯片选择 `osx-arm64`，Intel 处理器选择 `osx-x64`。
2. 下载名称含 `Portable.zip` 的文件，解压后将 `NetsGo.app` 拖到“应用程序”，再打开。
3. 如果系统提示无法验证开发者，确认文件来自上述官方仓库后，在“系统设置 → 隐私与安全性”中查看该应用的“仍要打开”选项。仅在你信任该应用时确认。
4. 若电脑由组织管理、没有“仍要打开”选项或提示包含恶意软件，请停止安装并联系管理员或项目维护者。不要关闭 Gatekeeper、杀毒软件或执行来源不明的绕过命令。

macOS 包使用免费的 ad-hoc 签名封装完整应用，但没有 Apple Developer ID 或公证。这不会消除系统对开发者身份的提示。系统策略也可能在升级后要求再次确认。

Apple 官方说明：https://support.apple.com/zh-cn/102445

## Windows

1. 64 位 Windows 选择 `win-x64` 的 `Setup.exe`，或下载 `Portable.zip` 并解压完整目录。
2. 如果 SmartScreen 提示“Windows 已保护你的电脑”，确认来自官方仓库后，可查看“更多信息”；只有在系统提供“仍要运行”选项且你信任应用时再继续。
3. Smart App Control、组织策略或杀毒软件可能直接阻止未签名应用。这种情况不保证能安装；不要为了安装 NetsGo 关闭系统保护，请联系管理员或项目维护者。

Microsoft 官方说明：https://learn.microsoft.com/windows/apps/package-and-deploy/smartscreen-reputation

## 后续更新

启动客户端后，点击左下角版本号检查更新。客户端从 GitHub / CNB 获取签名清单并验证下载的文件，不需要 Apple / Microsoft 开发者账号。下载完成后选择“重启并更新”。这会短暂中断隧道，已有配置会保留，并尝试恢复之前运行的连接。

系统签名与更新签名是两件事：更新签名能检查文件是否来自 NetsGo 发布方，不能代替操作系统对开发者身份的认可，也不能保证软件没有缺陷。
