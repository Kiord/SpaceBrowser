import { formatDuration } from "./format.js";

export const pendingScan = job => ["queued", "running", "paused"].includes(job?.state);

export function updateScanStatus(refs, job) {
  const progress = job.progress || {};
  const done = job.state === "completed";
  const paused = job.state === "paused";
  const queued = job.state === "queued";
  const idle = job.state === "idle" || job.state === "cancelled";
  const active = pendingScan(job);
  refs.container.hidden = false;
  refs.container.setAttribute("data-state", job.state);
  refs.container.setAttribute("data-complete", String(done));
  refs.label.hidden = idle;
  refs.time.textContent = done ? `Done in ${((progress.elapsedMilliseconds || 0) / 1000).toFixed(1)}s`
    : queued ? "Queued" : paused ? `Paused · ${formatDuration(progress.elapsedMilliseconds || 0)}`
    : job.state === "failed" ? "Scan failed" : formatDuration(progress.elapsedMilliseconds || 0);
  const fraction = done ? 1 : Math.max(0, Math.min(0.96, (progress.fraction || 0) * 0.96));
  refs.progress.hidden = idle;
  refs.percent.textContent = `${Math.floor(fraction * 100)}%`;
  refs.percent.hidden = !active || queued;
  refs.bar.style.setProperty("--scan-progress", `${fraction * 100}%`);
  refs.progress.setAttribute("aria-valuenow", String(Math.floor(fraction * 100)));
  refs.date.hidden = !done || !job.scannedAt;
  refs.date.textContent = job.scannedAt ? `${new Date(job.scannedAt).toLocaleString()}${job.saving ? " · Saving…" : job.cacheError ? " · Not saved" : ""}` : "";
  refs.date.title = job.cacheError || (job.cached ? "Saved scan" : "");
  const action = done ? "Rescan" : paused ? "Resume" : job.state === "running" ? "Pause" : job.state === "failed" ? "Retry" : "Scan";
  refs.primary.textContent = action;
  refs.primary.setAttribute("aria-label", `${action} ${job.path || "location"}`);
  refs.primary.hidden = queued;
  refs.cancel.hidden = !active;
}

export function createTileScanStatus(onPause, onCancel, onScan) {
  const container = document.createElement("div");
  container.className = "tile-scan-status";
  container.innerHTML = `<div class="compact-scan-label" data-scan="label"><span data-scan="time"></span><span data-scan="percent"></span></div><span class="compact-scan-progress" data-scan="progress" role="progressbar" aria-label="Scan progress" aria-valuemin="0" aria-valuemax="100"><span class="scan-progress-bar" data-scan="bar"></span></span><span class="scan-snapshot-date" data-scan="date"></span><div class="tile-scan-actions"><button type="button" class="tile-scan-primary" data-scan="primary">Scan</button><button type="button" class="scan-cancel-button" data-scan="cancel" aria-label="Cancel scan">Cancel</button></div>`;
  const refs = { container };
  for (const name of ["label", "time", "percent", "progress", "bar", "date", "primary", "cancel"]) refs[name] = container.querySelector(`[data-scan="${name}"]`);
  let current, busy = false;
  const render = () => {
    if (!current) return;
    updateScanStatus(refs, current);
    refs.primary.disabled = refs.cancel.disabled = busy;
  };
  const perform = async handler => {
    if (busy || !current) return;
    busy = true;
    render();
    try { await handler(current); }
    finally { busy = false; render(); }
  };
  refs.primary.addEventListener("click", () => {
    if (current?.state === "queued") return;
    return perform(job => ["running", "paused"].includes(job.state) ? onPause(job) : onScan(job));
  });
  refs.cancel.addEventListener("click", () => pendingScan(current) && perform(onCancel));
  return { element: container, update(job) { current = job; render(); } };
}
