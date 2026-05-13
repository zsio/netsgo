export type ConnectionState = "idle" | "connecting" | "connected" | "disconnecting" | "saved";

export type ClientEvent = {
  time?: string;
  level?: string;
  event?: string;
  message?: string;
  fields?: Record<string, unknown>;
};

export type DesktopLogEntry = {
  time: string;
  level: "debug" | "info" | "warn" | "error";
  event: string;
  message: string;
  fields?: Record<string, unknown>;
};

export type ClientSidecarStatus = {
  running: boolean;
  pid?: number | null;
  server?: string | null;
  mode?: "key" | "token" | string | null;
  connected: boolean;
  last_error?: string | null;
};

export type ClientSidecarEvent = {
  kind: "line" | "terminated" | "error" | "unknown";
  pid?: number | null;
  server?: string | null;
  mode?: "key" | "token" | string | null;
  line?: string | null;
  event?: ClientEvent | null;
  code?: number | null;
  signal?: number | null;
  error?: string | null;
};
