// How much of the cluster each project takes. Reserved is what a project's pods ask for; using is what
// they consume right now (only when Prometheus is installed). Both are read live from /api/usage.

const UNITS = ["B", "KB", "MB", "GB", "TB"];

export function formatBytes(value) {
  let size = Math.max(0, Number(value) || 0);
  let unit = 0;
  while (size >= 1024 && unit < UNITS.length - 1) {
    size /= 1024;
    unit += 1;
  }
  return `${size >= 100 || unit === 0 ? Math.round(size) : size.toFixed(1)} ${UNITS[unit]}`;
}

export function formatCPU(millis) {
  const cores = (Number(millis) || 0) / 1000;
  return `${cores >= 10 ? cores.toFixed(0) : cores.toFixed(2)} vCPU`;
}

export function formatPercent(fraction) {
  const percent = Math.max(0, Number(fraction) || 0) * 100;
  if (percent === 0) return "0%";
  return percent < 1 ? "<1%" : `${Math.round(percent)}%`;
}

/** "0.25 vCPU of 0.50 vCPU" style pair: what is used, against what is reserved. */
export function usingOfReserved(using, reserved, format) {
  if (using === undefined || using === null) return format(reserved);
  return `${format(using)} of ${format(reserved)}`;
}
