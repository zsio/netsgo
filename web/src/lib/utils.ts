import { clsx, type ClassValue } from "clsx"
import { twMerge } from "tailwind-merge"

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}

export function createLocalId(prefix = "id") {
  if (typeof globalThis !== "undefined") {
    const c = globalThis.crypto
    if (c && typeof c.randomUUID === "function") {
      return c.randomUUID()
    }
  }
  return `${prefix}-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`
}
