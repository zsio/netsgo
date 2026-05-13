import { ArrowRight, Link2Off, Power, RotateCw } from "lucide-react";
import type { ConnectionState } from "./clientTypes";

export function BrandHeader() {
  return (
    <header className="brand-header">
      <div className="brand-icon-wrapper">
        <img src="/logo.svg" className="brand-logo" alt="NetsGo Logo" />
      </div>
      <h1 className="brand-title">NetsGo</h1>
    </header>
  );
}

export function ActionFooter({
  state,
  canConnect,
  canReconnect,
  onConnect,
  onDisconnect,
  onReconnect,
  onForgetConnection,
}: {
  state: ConnectionState;
  canConnect: boolean;
  canReconnect: boolean;
  onConnect: () => void;
  onDisconnect: () => void;
  onReconnect: () => void;
  onForgetConnection: () => void;
}) {
  return (
    <footer className="action-area">
      {state === "connected" ? (
        <button className="btn btn-secondary" onClick={onDisconnect}>
          <Power size={14} />
          断开连接
        </button>
      ) : state === "saved" ? (
        <div className="button-stack">
          <button className="btn btn-primary" disabled={!canReconnect} onClick={onReconnect}>
            <RotateCw size={14} />
            重新连接
          </button>
          <button className="btn btn-secondary" onClick={onForgetConnection}>
            <Link2Off size={14} />
            更换连接
          </button>
        </div>
      ) : (
        <button className="btn btn-primary" disabled={!canConnect} onClick={onConnect}>
          {state === "connecting" || state === "disconnecting" ? (
            <>
              <RotateCw className="spin" size={14} />
              {state === "disconnecting" ? "断开中" : "连接中"}
            </>
          ) : (
            <>
              <ArrowRight size={14} />
              连接
            </>
          )}
        </button>
      )}
    </footer>
  );
}
