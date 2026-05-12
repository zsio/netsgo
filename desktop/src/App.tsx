import { useMemo, useRef, useState } from "react";
import { Command } from "@tauri-apps/plugin-shell";
import "./App.css";

type ConnectionState = "idle" | "connecting" | "connected" | "stopped" | "error";

function stateLabel(state: ConnectionState) {
  switch (state) {
    case "connecting":
      return "连接中";
    case "connected":
      return "已连接";
    case "stopped":
      return "已停止";
    case "error":
      return "连接失败";
    default:
      return "未连接";
  }
}

function App() {
  const [server, setServer] = useState("https://");
  const [key, setKey] = useState("");
  const [state, setState] = useState<ConnectionState>("idle");
  const [lastMessage, setLastMessage] = useState("填写服务地址和 Key 后启动客户端。");
  const [logs, setLogs] = useState<string[]>([]);
  const childRef = useRef<{ kill: () => Promise<void> } | null>(null);

  const canConnect = useMemo(
    () => server.trim().length > "https://".length && key.trim().length > 0 && !childRef.current,
    [server, key],
  );

  const appendLog = (line: string) => {
    setLogs((current) => [line, ...current].slice(0, 80));
  };

  async function connect() {
    if (!canConnect) return;
    setState("connecting");
    setLastMessage("正在启动 NetsGo client...");
    setLogs([]);

    try {
      const command = Command.sidecar("binaries/netsgo", [
        "client",
        "--server",
        server.trim(),
        "--key",
        key.trim(),
      ]);

      command.stdout.on("data", (line) => appendLog(line));
      command.stderr.on("data", (line) => {
        appendLog(line);
        if (line.includes("connecting") || line.includes("Client connecting")) {
          setState("connecting");
        }
        if (line.includes("authenticated") || line.includes("data channel")) {
          setState("connected");
          setLastMessage("客户端正在运行。");
        }
      });

      command.on("close", ({ code }) => {
        childRef.current = null;
        setState(code === 0 ? "stopped" : "error");
        setLastMessage(code === 0 ? "客户端已停止。" : `客户端退出，代码 ${code}。`);
      });

      childRef.current = await command.spawn();
      setState("connected");
      setLastMessage("客户端进程已启动，正在保持连接。");
    } catch (error) {
      childRef.current = null;
      setState("error");
      setLastMessage(error instanceof Error ? error.message : String(error));
    }
  }

  async function disconnect() {
    if (!childRef.current) return;
    setLastMessage("正在停止客户端...");
    await childRef.current.kill();
  }

  return (
    <main className="shell">
      <section className="connection-panel">
        <div className="masthead">
          <div>
            <p className="eyebrow">NetsGo Client</p>
            <h1>连接到 NetsGo Server</h1>
          </div>
          <div className={`status status-${state}`}>
            <span />
            {stateLabel(state)}
          </div>
        </div>

        <div className="form-grid">
          <label>
            <span>服务地址</span>
            <input
              value={server}
              onChange={(event) => setServer(event.currentTarget.value)}
              placeholder="https://example.com:9527"
              disabled={Boolean(childRef.current)}
            />
          </label>
          <label>
            <span>Client Key</span>
            <input
              value={key}
              onChange={(event) => setKey(event.currentTarget.value)}
              placeholder="sk-..."
              type="password"
              disabled={Boolean(childRef.current)}
            />
          </label>
        </div>

        <div className="actions">
          {!childRef.current ? (
            <button className="primary" disabled={!canConnect} onClick={connect}>
              连接
            </button>
          ) : (
            <button className="danger" onClick={disconnect}>
              断开
            </button>
          )}
          <p>{lastMessage}</p>
        </div>
      </section>

      <section className="log-panel">
        <div className="log-title">运行日志</div>
        <div className="log-list">
          {logs.length === 0 ? (
            <p className="empty-log">暂无日志</p>
          ) : (
            logs.map((line, index) => <pre key={`${index}-${line}`}>{line}</pre>)
          )}
        </div>
      </section>
    </main>
  );
}

export default App;
