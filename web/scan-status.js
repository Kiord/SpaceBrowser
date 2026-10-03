import { formatDuration } from "./format.js";

export const pendingScan = job => ["queued", "running", "paused"].includes(job?.state);

export function updateScanStatus(refs, job) {
  const progress = job.progress || {};
  const done = job.state === "completed";
  const paused = job.state === "paused";
  const queued = job.state === "queued";
  const active = pendingScan(job);
  refs.container.hidden = job.state === "cancelled";
  refs.container.setAttribute("data-state", job.state);
  refs.container.setAttribute("data-complete", String(done));
  refs.time.textContent = done ? `Done in ${(progress.elapsedMilliseconds / 1000).toFixed(1)}s`
    : queued ? "Queued" : paused ? `Paused · ${formatDuration(progress.elapsedMilliseconds || 0)}`
    : job.state === "failed" ? "Scan failed" : job.state === "cancelled" ? "Cancelled" : formatDuration(progress.elapsedMilliseconds || 0);
  const fraction = done ? 1 : Math.max(0, Math.min(0.96, (progress.fraction || 0) * 0.96));
  refs.percent.textContent = `${Math.floor(fraction * 100)}%`;
  refs.percent.hidden = !active || queued;
  refs.bar.style.setProperty("--scan-progress", `${fraction * 100}%`);
  refs.progress.setAttribute("aria-valuenow", String(Math.floor(fraction * 100)));
  refs.actions.hidden = !active;
  refs.pause.disabled = false;
  const label = paused ? "Continue scan" : "Pause scan";
  refs.pause.setAttribute("aria-label", label);
  refs.pause.setAttribute("data-tooltip", label);
  refs.pause.setAttribute("aria-pressed", String(paused));
  refs.icon.setAttribute("d", paused ? "M8 4l12 8-12 8Z" : "M8 5v14M16 5v14");
}

export function createTileScanStatus(onPause, onCancel) {
  const container = document.createElement("div");
  container.className = "tile-scan-status";
  container.innerHTML = `<div class="tile-scan-title">Scan progress</div><div class="compact-scan-label"><span data-scan="time"></span><span data-scan="percent"></span><span class="compact-scan-actions" data-scan="actions"><button type="button" data-scan="pause"><svg viewBox="0 0 24 24" aria-hidden="true"><path data-scan="icon"></path></svg></button><button type="button" class="scan-cancel-button" data-scan="cancel" aria-label="Cancel scan" data-tooltip="Cancel scan"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="m6 6 12 12M18 6 6 18"></path></svg></button></span></div><span class="compact-scan-progress" data-scan="progress" role="progressbar" aria-label="Scan progress" aria-valuemin="0" aria-valuemax="100"><span class="scan-progress-bar" data-scan="bar"></span></span>`;
  const refs = { container };
  for (const name of ["time", "percent", "actions", "pause", "icon", "progress", "bar"]) refs[name] = container.querySelector(`[data-scan="${name}"]`);
  let current;
  refs.pause.addEventListener("click", () => current && onPause(current));
  container.querySelector('[data-scan="cancel"]').addEventListener("click", () => current && onCancel(current));
  return { element: container, update(job) { current = job; updateScanStatus(refs, job); } };
}
