import type { ClientEvent } from "./clientTypes";

export function formatClientEventError(event: ClientEvent, mode: "key" | "token") {
  const fields = event.fields ?? {};
  const code = typeof fields.code === "string" ? fields.code : "";
  const mapped = mapAuthErrorCode(code, mode);
  if (mapped) return mapped;

  const raw = fields.error ?? fields.message ?? event.message ?? event.event ?? "未知错误";
  return normalizeErrorMessage(String(raw));
}

function mapAuthErrorCode(code: string, mode: "key" | "token") {
  switch (code) {
    case "invalid_key":
      if (mode === "token") {
        return "本地登录状态不可用，请更换连接后重新输入 Key";
      }
      return "Key 无效，请检查访问凭证";
    case "invalid_token":
      return "本地登录状态已失效，请更换连接后重新输入 Key";
    case "revoked_token":
      return "本地登录状态已被撤销，请更换连接后重新输入 Key";
    case "concurrent_session":
      return "该客户端已在线，请先断开其他连接";
    case "rate_limited":
      return "认证尝试过于频繁，请稍后再试";
    case "server_uninitialized":
      return "服务器尚未初始化";
    default:
      return "";
  }
}

function normalizeErrorMessage(message: string) {
  const lower = message.toLowerCase();
  if (lower.includes("failed to connect to server")) {
    return "无法连接服务器，请检查地址和网络";
  }
  if (lower.includes("tls certificate fingerprint")) {
    return "服务器证书指纹校验失败";
  }
  if (lower.includes("authentication failed")) {
    return "认证失败，请检查访问凭证";
  }
  if (lower.includes("failed to establish data channel")) {
    return "数据通道建立失败，请稍后重试";
  }
  if (lower.includes("failed to acquire client singleton lock") || lower.includes("获取锁失败")) {
    return "本机已有 NetsGo 客户端正在运行，请先退出后再连接";
  }
  return message;
}
