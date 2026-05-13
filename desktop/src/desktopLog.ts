import { invoke } from "@tauri-apps/api/core";
import type { DesktopLogEntry } from "./clientTypes";

export function logDesktop(
  level: DesktopLogEntry["level"],
  event: string,
  message: string,
  fields?: Record<string, unknown>
) {
  const entry: DesktopLogEntry = {
    time: new Date().toISOString(),
    level,
    event,
    message,
    fields,
  };

  invoke("append_desktop_log", { entry }).catch((error) => {
    console.error("Failed to append desktop log:", error);
  });
}
