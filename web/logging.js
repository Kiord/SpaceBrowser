import { LogDebug, LogError, LogWarning } from "./wailsjs/runtime/runtime.js";

function formatLogValue(value) {
  if (value instanceof Error) return value.message;
  if (typeof value === "string") return value;
  try {
    return JSON.stringify(value);
  } catch {
    return String(value);
  }
}

function formatLogMessage(values) {
  return values.map(formatLogValue).join(" ");
}

export function logDebug(...values) {
  LogDebug(formatLogMessage(values));
}

export function logWarning(...values) {
  LogWarning(formatLogMessage(values));
}

export function logError(...values) {
  LogError(formatLogMessage(values));
}
