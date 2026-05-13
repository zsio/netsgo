import { openUrl } from "@tauri-apps/plugin-opener";
import { Globe, KeyRound } from "lucide-react";

export function ConnectedView({ server }: { server: string }) {
  return (
    <div className="status-view">

      <h2>已连接</h2>
      <p className="status-desc">安全隧道正在运行</p>
      <button className="server-badge" onClick={() => openUrl(server)} title="在浏览器中打开">
        <Globe size={14} />
        <span>{server}</span>
      </button>
    </div>
  );
}

export function SavedConnectionView({ server }: { server: string }) {
  return (
    <div className="saved-view">
      <p className="status-desc">已保存上次连接，可直接使用本地凭证恢复。</p>
      <div className="saved-server">
        <span>上次连接</span>
        <strong>{server}</strong>
      </div>
    </div>
  );
}

export function ConnectingView({ statusText, server }: { statusText: string; server: string }) {
  return (
    <div className="connecting-view">
      <div className="spinner-ring" />
      <h2>正在连接</h2>
      <p className="status-desc">{statusText || "请稍候"}</p>
      {server ? (
        <div className="server-badge muted">
          <Globe size={14} />
          <span>{server}</span>
        </div>
      ) : null}
    </div>
  );
}

export function NewConnectionForm({
  server,
  setServer,
  accessKey,
  setAccessKey,
}: {
  server: string;
  setServer: (value: string) => void;
  accessKey: string;
  setAccessKey: (value: string) => void;
}) {
  return (
    <div className="form-view">
      <div className="input-group">
        <label>服务器节点</label>
        <label className="input-wrapper">
          <Globe className="input-icon" size={14} />
          <input
            className="input-field"
            value={server}
            onChange={(event) => setServer(event.target.value)}
            placeholder="https://netsgo.example.com"
            spellCheck={false}
          />
        </label>
      </div>

      <div className="input-group">
        <label>访问凭证</label>
        <label className="input-wrapper">
          <KeyRound className="input-icon" size={14} />
          <input
            className="input-field"
            type="password"
            value={accessKey}
            onChange={(event) => setAccessKey(event.target.value)}
            placeholder="请输入 Client Key"
          />
        </label>
      </div>
    </div>
  );
}
