export const SIZE_UNITS = Object.freeze({
  B: 1,
  KB: 1024,
  MB: 1024 ** 2,
  GB: 1024 ** 3,
});

export function splitSizeIntoUnit(bytes) {
  for (const unit of ["GB", "MB", "KB"]) {
    const multiplier = SIZE_UNITS[unit];
    if (bytes >= multiplier && bytes % multiplier === 0) {
      return { value: bytes / multiplier, unit };
    }
  }
  return { value: bytes, unit: "B" };
}

export function formatDuration(milliseconds, roundUp = false) {
  const rawSeconds = milliseconds / 1000;
  const totalSeconds = Math.max(0, roundUp ? Math.ceil(rawSeconds) : Math.round(rawSeconds));
  const hours = Math.floor(totalSeconds / 3600);
  const minutes = Math.floor((totalSeconds % 3600) / 60);
  const seconds = totalSeconds % 60;
  if (hours > 0) return `${hours}:${String(minutes).padStart(2, "0")}:${String(seconds).padStart(2, "0")}`;
  return `${minutes}:${String(seconds).padStart(2, "0")}`;
}

export function formatModTime(seconds) {
  if (!seconds) return "";
  const date = new Date(seconds * 1000);
  const year = date.getFullYear();
  const month = String(date.getMonth() + 1).padStart(2, "0");
  const day = String(date.getDate()).padStart(2, "0");
  return `${year}-${month}-${day}`;
}

export function detailedByteSize(bytes) {
  return `${Number(bytes || 0).toLocaleString()} bytes`;
}

export function formatSize(bytes) {
  if (!bytes) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB", "PB"];
  const index = Math.min(units.length - 1, Math.floor(Math.log(bytes) / Math.log(1024)));
  const value = bytes / Math.pow(1024, index);
  return `${value.toFixed(value < 10 ? 1 : 0)} ${units[index]}`;
}

export function formatCompactSize(bytes) {
  return formatSize(bytes).replace(/\.0 (?=[A-Z])/, "").replace(" ", "");
}

export function debounce(func, delay) {
  let timer;
  return (...args) => {
    clearTimeout(timer);
    timer = setTimeout(() => func(...args), delay);
  };
}
