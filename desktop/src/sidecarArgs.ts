export function sanitizeSidecarArgs(args: string[]) {
  const sanitized = [...args];
  const keyIndex = sanitized.indexOf("--key");
  if (keyIndex >= 0 && keyIndex + 1 < sanitized.length) {
    sanitized[keyIndex + 1] = "***";
  }
  return sanitized;
}
