import { useEffect, useMemo, useRef, useState } from "react";
import { appLocalDataDir } from "@tauri-apps/api/path";
import { invoke } from "@tauri-apps/api/core";
import { listen } from "@tauri-apps/api/event";
import toast from "react-hot-toast";
import { LAST_SERVER_KEY, SERVER_RELEASE_DELAY_MS, SIDECAR_EVENT_NAME, TOKEN_RECONNECT_KEY } from "./clientConstants";
import { formatClientEventError } from "./clientErrors";
import type { ClientEvent, ClientSidecarEvent, ClientSidecarStatus, ConnectionState } from "./clientTypes";
import { logDesktop } from "./desktopLog";

export function useClientConnection() {
  const [server, setServer] = useState("");
  const [key, setKey] = useState("");
  const [lastServer, setLastServer] = useState<string | null>(null);
  const [state, setState] = useState<ConnectionState>("idle");
  const [statusText, setStatusText] = useState("");

  const intentionalDisconnectRef = useRef(false);
  const activeServerRef = useRef("");
  const lastServerRef = useRef<string | null>(null);
  const startingRef = useRef(false);
  const startSeqRef = useRef(0);
  const lastErrorMsg = useRef("");
  const activeModeRef = useRef<"key" | "token">("key");
  const closeWaitersRef = useRef<Array<() => void>>([]);
  const preparingConnectionRef = useRef(false);
  const disconnectTargetRef = useRef<ConnectionState | null>(null);
  const sidecarRunningRef = useRef(false);

  useEffect(() => {
    logDesktop("info", "desktop.app_started", "Desktop app started");
    let disposed = false;
    let unlisten: (() => void) | null = null;
    let unlistenTrayStart: (() => void) | null = null;
    let unlistenTrayDisconnect: (() => void) | null = null;

    void (async () => {
      unlisten = await listen<ClientSidecarEvent>(SIDECAR_EVENT_NAME, (event) => {
        if (!disposed) {
          handleSidecarEvent(event.payload);
        }
      });
      unlistenTrayStart = await listen("netsgo://tray-start-request", () => {
        if (!disposed && lastServerRef.current) {
          void startClient({ server: lastServerRef.current, mode: "token" });
        }
      });
      unlistenTrayDisconnect = await listen("netsgo://tray-disconnect-request", () => {
        if (!disposed) {
          void disconnect();
        }
      });

      if (!disposed) {
        await restoreInitialState();
      }
    })();

    return () => {
      disposed = true;
      logDesktop("info", "desktop.app_unmount", "Desktop app unmounting");
      unlisten?.();
      unlistenTrayStart?.();
      unlistenTrayDisconnect?.();
    };
  }, []);

  useEffect(() => {
    invoke("update_tray_connection_menu", {
      request: {
        state,
        hasSavedConnection: Boolean(lastServer),
      },
    }).catch((error) => {
      logDesktop("warn", "desktop.tray_menu_update_failed", "Tray menu update failed", {
        state,
        hasSavedConnection: Boolean(lastServer),
        error: error instanceof Error ? error.message : String(error),
      });
    });
  }, [state, lastServer]);

  const canConnect = useMemo(
    () =>
      server.trim().length > 0 &&
      key.trim().length > 0 &&
      state !== "connecting" &&
      state !== "disconnecting",
    [server, key, state]
  );

  const canReconnect = useMemo(
    () => Boolean(lastServer) && state !== "connecting" && state !== "disconnecting",
    [lastServer, state]
  );

  const visibleServer = lastServer || activeServerRef.current;
  const visibleConnectingServer = activeServerRef.current || lastServer || server;

  function saveLastServer(value: string | null) {
    lastServerRef.current = value;
    setLastServer(value);
    if (value) {
      logDesktop("info", "desktop.saved_connection_updated", "Saved connection updated", {
        server: value,
      });
      window.localStorage.setItem(LAST_SERVER_KEY, value);
    } else {
      logDesktop("info", "desktop.saved_connection_cleared", "Saved connection cleared");
      window.localStorage.removeItem(LAST_SERVER_KEY);
    }
  }

  async function clearClientState(reason: string) {
    logDesktop("info", "desktop.client_state_clear_requested", "Client state clear requested", {
      reason,
    });
    await invoke("clear_client_state_dir");
    logDesktop("info", "desktop.client_state_cleared", "Client state cleared", { reason });
  }

  async function restoreInitialState() {
    const saved = window.localStorage.getItem(LAST_SERVER_KEY);
    if (saved) {
      logDesktop("info", "desktop.saved_connection_found", "Saved connection found", {
        server: saved,
      });
      saveLastServer(saved);
    }

    const status = await invoke<ClientSidecarStatus>("client_sidecar_status");
    logDesktop("info", "desktop.sidecar_status_restored", "Sidecar status restored", {
      status,
    });
    applySidecarStatus(status, saved);

    if (!status.running && saved) {
      setState("saved");
      void startClient({ server: saved, mode: "token" });
    }
  }

  function applySidecarStatus(status: ClientSidecarStatus, fallbackServer?: string | null) {
    sidecarRunningRef.current = status.running;
    const sidecarServer = status.server || fallbackServer || "";
    if (sidecarServer) {
      activeServerRef.current = sidecarServer;
      saveLastServer(sidecarServer);
    }
    if (status.mode === "key" || status.mode === "token") {
      activeModeRef.current = status.mode;
    }

    if (status.running && status.connected) {
      setState("connected");
      setStatusText("");
      setServer("");
      setKey("");
      return;
    }

    if (status.running) {
      setState("connecting");
      setStatusText("正在恢复运行中的连接");
      return;
    }

    setState(sidecarServer ? "saved" : "idle");
    setStatusText("");
  }

  function waitForServerRelease(reason: string) {
    logDesktop("debug", "desktop.server_release_wait_started", "Waiting for server session release", {
      reason,
      delayMs: SERVER_RELEASE_DELAY_MS,
    });
    return new Promise<void>((resolve) => {
      window.setTimeout(() => {
        logDesktop("debug", "desktop.server_release_wait_finished", "Server session release wait finished", {
          reason,
        });
        resolve();
      }, SERVER_RELEASE_DELAY_MS);
    });
  }

  async function startClient(options: { server: string; key?: string; mode: "key" | "token" }) {
    const trimmedServer = options.server.trim();
    if (!trimmedServer || sidecarRunningRef.current || startingRef.current) {
      logDesktop("warn", "desktop.sidecar_start_skipped", "Sidecar start skipped", {
        hasServer: Boolean(trimmedServer),
        hasChild: sidecarRunningRef.current,
        starting: startingRef.current,
        preparing: preparingConnectionRef.current,
      });
      return;
    }

    startingRef.current = true;
    const startSeq = ++startSeqRef.current;
    logDesktop("info", "desktop.sidecar_start_requested", "Sidecar start requested", {
      server: trimmedServer,
      hasKey: Boolean(options.key && options.key !== TOKEN_RECONNECT_KEY),
      tokenReconnect: options.mode === "token",
      mode: options.mode,
      startSeq,
    });
    setState("connecting");
    setStatusText(options.mode === "key" ? "正在验证新的连接" : "正在恢复上次连接");
    intentionalDisconnectRef.current = false;
    activeServerRef.current = trimmedServer;
    activeModeRef.current = options.mode;
    lastErrorMsg.current = "";

    try {
      const dataDir = await appLocalDataDir();
      if (startSeq !== startSeqRef.current || intentionalDisconnectRef.current) {
        logDesktop("warn", "desktop.sidecar_start_cancelled", "Sidecar start cancelled before spawn", {
          startSeq,
          currentSeq: startSeqRef.current,
          intentionalDisconnect: intentionalDisconnectRef.current,
        });
        startingRef.current = false;
        return;
      }

      const sidecarKey = options.mode === "key" ? options.key?.trim() : "";
      const loggedArgs = [
        "client",
        "--server",
        trimmedServer,
        "--data-dir",
        dataDir,
        "--log-format",
        "json",
      ];
      logDesktop("debug", "desktop.sidecar_args_ready", "Sidecar args ready", {
        args: loggedArgs,
        hasKey: Boolean(sidecarKey),
        tokenReconnect: options.mode === "token",
        startSeq,
      });

      const status = await invoke<ClientSidecarStatus>("start_client_sidecar", {
        request: {
          server: trimmedServer,
          key: sidecarKey || null,
          mode: options.mode,
          dataDir,
        },
      });
      if (startSeq !== startSeqRef.current || intentionalDisconnectRef.current) {
        logDesktop("warn", "desktop.sidecar_spawn_obsolete", "Spawned sidecar became obsolete", {
          startSeq,
          currentSeq: startSeqRef.current,
          intentionalDisconnect: intentionalDisconnectRef.current,
        });
        await invoke("stop_client_sidecar");
        startingRef.current = false;
        return;
      }

      sidecarRunningRef.current = status.running;
      startingRef.current = false;
      logDesktop("info", "desktop.sidecar_spawned", "Sidecar spawned", { startSeq, status });
    } catch (error) {
      if (startSeq !== startSeqRef.current) {
        logDesktop("warn", "desktop.sidecar_start_error_ignored", "Obsolete sidecar start error ignored", {
          startSeq,
          currentSeq: startSeqRef.current,
          error: error instanceof Error ? error.message : String(error),
        });
        startingRef.current = false;
        return;
      }
      sidecarRunningRef.current = false;
      startingRef.current = false;
      setState(lastServerRef.current ? "saved" : "idle");
      lastErrorMsg.current = error instanceof Error ? error.message : String(error);
      logDesktop("error", "desktop.sidecar_start_failed", "Sidecar start failed", {
        startSeq,
        error: lastErrorMsg.current,
      });
      showConnectionError();
    }
  }

  function resolveCloseWaiters() {
    const waiters = closeWaitersRef.current;
    closeWaitersRef.current = [];
    for (const resolve of waiters) {
      resolve();
    }
  }

  function waitForSidecarClose() {
    if (!sidecarRunningRef.current && !startingRef.current) {
      return Promise.resolve();
    }
    return new Promise<void>((resolve) => {
      closeWaitersRef.current.push(resolve);
    });
  }

  function handleSidecarEvent(event: ClientSidecarEvent) {
    logDesktop(event.kind === "error" ? "error" : "debug", "desktop.sidecar_manager_event", "Sidecar manager event received", {
      event,
    });

    if (event.server) {
      activeServerRef.current = event.server;
    }
    if (event.mode === "key" || event.mode === "token") {
      activeModeRef.current = event.mode;
    }

    if (event.kind === "line" && event.event) {
      if (event.line) {
        logDesktop("debug", "desktop.sidecar_stderr", "Sidecar output line", {
          chunk: `${event.line}\n`,
        });
      }
      handleClientEvent(event.event);
      return;
    }

    if (event.kind === "error") {
      sidecarRunningRef.current = false;
      lastErrorMsg.current = event.error || "sidecar error";
      resolveCloseWaiters();
      showConnectionError();
      return;
    }

    if (event.kind === "terminated") {
      sidecarRunningRef.current = false;
      startingRef.current = false;
      logDesktop(event.code === 0 ? "info" : "error", "desktop.sidecar_closed", "Sidecar closed", {
        code: event.code,
        signal: event.signal,
        intentionalDisconnect: intentionalDisconnectRef.current,
        lastError: lastErrorMsg.current,
      });
      resolveCloseWaiters();

      if (intentionalDisconnectRef.current) {
        const nextState = disconnectTargetRef.current ?? (lastServerRef.current ? "saved" : "idle");
        disconnectTargetRef.current = null;
        setState(nextState);
        setStatusText("");
        return;
      }

      if (event.code !== 0 && event.code !== null) {
        setState(lastServerRef.current ? "saved" : "idle");
        showConnectionError();
        return;
      }

      setState(lastServerRef.current ? "saved" : "idle");
      setStatusText("");
    }
  }

  function handleClientEvent(event: ClientEvent) {
    logDesktop(event.level === "error" ? "error" : "debug", "desktop.sidecar_event", "Sidecar JSON event received", {
      event,
    });
    switch (event.event) {
      case "client.connecting":
        setStatusText("正在连接服务器");
        break;
      case "client.connected":
        setStatusText("正在认证身份");
        break;
      case "client.authenticated":
        setStatusText("正在建立数据通道");
        break;
      case "client.data_channel_established": {
        const connectedServer = activeServerRef.current;
        saveLastServer(connectedServer);
        setState("connected");
        setStatusText("");
        setServer("");
        setKey("");
        break;
      }
      case "client.connection_lost":
      case "client.reconnecting":
      case "client.reconnect_attempt":
        setStatusText("连接中断，正在重连");
        break;
      case "client.auth_failed":
      case "client.data_channel_failed":
      case "client.token_clear_failed":
        lastErrorMsg.current = formatClientEventError(event, activeModeRef.current);
        break;
      case "client.fatal":
        if (!lastErrorMsg.current) {
          lastErrorMsg.current = formatClientEventError(event, activeModeRef.current);
        }
        break;
      default:
        if (event.level === "error") {
          lastErrorMsg.current = formatClientEventError(event, activeModeRef.current);
        }
        break;
    }
  }

  function connect() {
    if (!canConnect || preparingConnectionRef.current) return;
    preparingConnectionRef.current = true;
    setState("connecting");
    setStatusText("正在准备新的连接");
    logDesktop("info", "desktop.connect_clicked", "Connect clicked", {
      server: server.trim(),
      hasKey: Boolean(key.trim()),
    });
    void (async () => {
      try {
        await waitForSidecarClose();
        await clearClientState("new_connection");
        await startClient({ server, key, mode: "key" });
      } catch (error) {
        lastErrorMsg.current = error instanceof Error ? error.message : String(error);
        setState("idle");
        setStatusText("");
        logDesktop("error", "desktop.connect_failed_before_sidecar", "Connect failed before sidecar start", {
          error: lastErrorMsg.current,
        });
        showConnectionError();
      } finally {
        preparingConnectionRef.current = false;
      }
    })();
  }

  function reconnect() {
    if (!lastServer || !canReconnect) return;
    logDesktop("info", "desktop.reconnect_clicked", "Reconnect clicked", {
      server: lastServer,
    });
    void startClient({ server: lastServer, mode: "token" });
  }

  async function disconnect(targetState?: ConnectionState) {
    logDesktop("info", "desktop.disconnect_requested", "Disconnect requested", {
      targetState,
      hasChild: sidecarRunningRef.current,
    });
    intentionalDisconnectRef.current = true;
    disconnectTargetRef.current = targetState ?? null;
    startSeqRef.current += 1;
    if (!sidecarRunningRef.current) {
      resolveCloseWaiters();
      disconnectTargetRef.current = null;
      setState(targetState ?? (lastServerRef.current ? "saved" : "idle"));
      setStatusText("");
      return;
    }
    setState("disconnecting");
    setStatusText("正在断开连接");
    try {
      await invoke("stop_client_sidecar");
      logDesktop("info", "desktop.sidecar_kill_sent", "Sidecar kill sent");
    } catch (error) {
      console.error("Failed to kill process:", error);
      logDesktop("error", "desktop.sidecar_kill_failed", "Sidecar kill failed", {
        error: error instanceof Error ? error.message : String(error),
      });
    }
  }

  function forgetConnection() {
    logDesktop("info", "desktop.forget_connection_clicked", "Forget connection clicked");
    intentionalDisconnectRef.current = true;
    startSeqRef.current += 1;
    startingRef.current = false;
    setState("disconnecting");
    setStatusText("正在清理本地连接");
    void (async () => {
      await disconnect("disconnecting");
      await waitForSidecarClose();
      await waitForServerRelease("forget_connection");
      try {
        await clearClientState("forget_connection");
        saveLastServer(null);
        setServer("");
        setKey("");
        setState("idle");
        setStatusText("");
      } catch (error) {
        logDesktop("error", "desktop.client_state_clear_failed", "Client state clear failed", {
          error: error instanceof Error ? error.message : String(error),
        });
        setState(lastServerRef.current ? "saved" : "idle");
        setStatusText("");
        showConnectionError();
      }
    })();
  }

  function showConnectionError() {
    let cause = lastErrorMsg.current || "请检查服务器地址和访问凭证";
    if (cause.length > 72) {
      cause = cause.substring(0, 72) + "...";
    }

    logDesktop("error", "desktop.connection_error_toast", "Connection error shown", {
      cause,
    });
    toast.error(`连接失败：${cause}`, {
      style: {
        borderRadius: "6px",
        background: "var(--input-bg)",
        color: "var(--text-primary)",
        border: "1px solid var(--border-color)",
        fontSize: "13px",
      },
    });
  }

  return {
    server,
    setServer,
    key,
    setKey,
    lastServer,
    state,
    statusText,
    visibleServer,
    visibleConnectingServer,
    canConnect,
    canReconnect,
    connect,
    reconnect,
    disconnect,
    forgetConnection,
  };
}
