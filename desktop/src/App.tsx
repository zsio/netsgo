import { useMemo, useRef, useState } from "react";
import { appLocalDataDir } from "@tauri-apps/api/path";
import { Command } from "@tauri-apps/plugin-shell";
import { openUrl } from "@tauri-apps/plugin-opener";
import { ShieldCheck, Globe, KeyRound } from "lucide-react";
import toast, { Toaster } from "react-hot-toast";
import "./App.css";

type ConnectionState = "idle" | "connecting" | "connected" | "error";

function App() {
  const [server, setServer] = useState("");
  const [key, setKey] = useState("");
  const [state, setState] = useState<ConnectionState>("idle");
  
  const childRef = useRef<{ kill: () => Promise<void> } | null>(null);
  const intentionalDisconnectRef = useRef<boolean>(false);
  const lastErrorMsg = useRef<string>("");

  const canConnect = useMemo(
    () => server.trim().length > 0 && key.trim().length > 0 && state !== "connecting",
    [server, key, state]
  );

  async function connect() {
    if (!canConnect) return;
    setState("connecting");
    intentionalDisconnectRef.current = false;
    lastErrorMsg.current = "";

    try {
      const dataDir = await appLocalDataDir();
      const command = Command.sidecar("binaries/netsgo", [
        "client",
        "--server",
        server.trim(),
        "--key",
        key.trim(),
        "--data-dir",
        dataDir,
      ]);

      const handleLog = (line: string) => {
        const text = line.toLowerCase();
        // ONLY set to connected when the entire connection flow (including auth and data channel) is complete
        if (text.includes("data channel established")) {
          setState("connected");
        }
        
        // Try to capture meaningful error messages
        if (text.includes("error") || text.includes("fail") || text.includes("invalid") || text.includes("fatal")) {
            lastErrorMsg.current = line.trim();
        }
      };

      command.stdout.on("data", handleLog);
      command.stderr.on("data", handleLog);

      command.on("close", ({ code }) => {
        childRef.current = null;
        
        // If we intentionally disconnected, just reset state implicitly.
        if (intentionalDisconnectRef.current) {
          setState("idle");
          return;
        }

        // It crashed or exited unexpectedly
        if (code !== 0) {
          setState("idle"); // reset form state properly so they can edit right away
          
          let cause = lastErrorMsg.current || "请检查服务器地址和凭证是否正确";
          // Simplify very long error logs to be more readable in a tiny toast
          if (cause.length > 50) {
              cause = cause.substring(0, 50) + "...";
          }
          
          toast.error(`连接失败: ${cause}`, {
            style: {
              borderRadius: '6px',
              background: 'var(--input-bg)',
              color: 'var(--text-primary)',
              border: '1px solid var(--border-color)',
              fontSize: '13px'
            },
          });
        } else {
          setState("idle");
        }
      });

      childRef.current = await command.spawn();
    } catch (error) {
      childRef.current = null;
      setState("idle");
      toast.error(error instanceof Error ? error.message : String(error), {
        style: {
          borderRadius: '6px',
          background: 'var(--input-bg)',
          color: 'var(--text-primary)',
          border: '1px solid var(--border-color)',
          fontSize: '13px'
        },
      });
    }
  }

  async function disconnect() {
    intentionalDisconnectRef.current = true;
    if (!childRef.current) {
        setState("idle");
        return;
    }
    try {
      await childRef.current.kill();
    } catch (e) {
      console.error("Failed to kill process:", e);
    }
    setState("idle");
  }

  return (
    <div className="app-wrapper">
      <Toaster position="top-right" toastOptions={{ duration: 1000 }} />
      
      <header className="brand-header">
        <div className="brand-icon-wrapper">
          <img src="/logo.svg" className="brand-logo" alt="NetsGo Logo" />
        </div>
        <h1 className="brand-title">NetsGo</h1>
      </header>

      <main className="content-area">
        {state === "connected" ? (
          <div className="status-view">
            <div className="connection-pulse">
              <div className="pulse-core">
                <ShieldCheck size={28} />
              </div>
            </div>
            <h2>已连接</h2>
            <div 
              className="server-badge" 
              style={{ cursor: 'pointer' }}
              onClick={() => openUrl(server.trim())}
              title="在浏览器中打开"
            >
              <Globe size={14} />
              <span>{server}</span>
            </div>
          </div>
        ) : (
          <div className="form-view">
            <div className="input-group">
              <label>服务器节点</label>
              <label className={`input-wrapper ${state === 'connecting' ? 'disabled' : ''}`}>
                <Globe className="input-icon" size={14} />
                <input
                  className="input-field"
                  value={server}
                  onChange={(e) => setServer(e.target.value)}
                  placeholder="https://example.com:9527"
                  disabled={state === "connecting"}
                  spellCheck={false}
                />
              </label>
            </div>

            <div className="input-group">
              <label>访问凭证</label>
              <label className={`input-wrapper ${state === 'connecting' ? 'disabled' : ''}`}>
                <KeyRound className="input-icon" size={14} />
                <input
                  className="input-field"
                  type="password"
                  value={key}
                  onChange={(e) => setKey(e.target.value)}
                  placeholder="请输入您的 Client Key"
                  disabled={state === "connecting"}
                />
              </label>
            </div>
          </div>
        )}
      </main>

      <footer className="action-area">
        {state === "connected" ? (
          <button className="btn btn-danger" onClick={disconnect}>
            断开连接
          </button>
        ) : (
          <button 
            className="btn btn-primary" 
            disabled={!canConnect} 
            onClick={connect}
          >
            {state === "connecting" ? "连接中..." : "连接"}
          </button>
        )}
      </footer>
    </div>
  );
}

export default App;
