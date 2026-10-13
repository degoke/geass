import { condition, resourceDisplayName, resourceName } from "@/lib/api";
import { isObjectStoreEngine } from "@/lib/object-storage";

const PENDING_CHANGES_ANNOTATION = "geass.dev/pending-changes";
const PENDING_UPDATES_ANNOTATION = "geass.dev/pending-updates";
const DEPLOY_WORKFLOW_ANNOTATION = "geass.dev/deploy-workflow";
const LAST_DEPLOYED_ANNOTATION = "geass.dev/last-deployed";

/**
 * What a settings save means for the running resource. The API reports `applied` as "none"
 * (nothing changed), "live" (already in effect), "deploy" (waiting for a deploy) or "deployed".
 */
export function settingsSavedMessage(result) {
  switch (result?.applied) {
    case "none":
      return "Nothing to save";
    case "live":
      return "Saved. Applied now";
    case "deploy":
      return result.liveChanged ? "Saved. Some changes are live; deploy to apply the rest" : "Saved. Deploy to apply them";
    case "deployed":
      return "Saved and deployed";
    default:
      return "Settings saved";
  }
}

export function openResourceDialog() {
  window.dispatchEvent(new Event("geass:open-resource"));
}

export function usesDeployWorkflow(item) {
  return item?.metadata?.annotations?.[DEPLOY_WORKFLOW_ANNOTATION] === "true";
}

export function isService(item) {
  return Boolean(item?.spec?.source);
}

export function isObjectStore(item) {
  return isObjectStoreEngine(item?.spec?.engine);
}

export function isDatabase(item) {
  return Boolean(item?.spec?.engine) && Boolean(item?.spec?.project) && !isObjectStore(item) && !isService(item);
}

export function isDraftResource(item) {
  if (!usesDeployWorkflow(item)) return false;
  return !item?.spec?.deploy?.enabled;
}

export function isDraftService(item) {
  return isService(item) && isDraftResource(item);
}

export function isDraftObjectStore(item) {
  return isObjectStore(item) && Boolean(item?.spec?.project) && isDraftResource(item);
}

export function isDraftDatabase(item) {
  return isDatabase(item) && isDraftResource(item);
}

export function pendingChangeKinds(item) {
  if (!item) return [];
  const raw = item?.metadata?.annotations?.[PENDING_CHANGES_ANNOTATION] || "";
  let kinds = raw.split(",").map((part) => part.trim()).filter(Boolean);
  const deployEnabled = Boolean(item.spec?.deploy?.enabled);
  if (usesDeployWorkflow(item) && !deployEnabled && !kinds.includes("created")) {
    kinds = ["created", ...kinds];
  }
  kinds = kinds.filter((kind, index) => kinds.indexOf(kind) === index);
  if (kinds.length) return kinds;
  const count = Number(item?.metadata?.annotations?.[PENDING_UPDATES_ANNOTATION] || 0);
  if (count > 0) return ["settings"];
  return [];
}

export function pendingChangeCount(item) {
  const kinds = pendingChangeKinds(item);
  if (!kinds.length) return 0;
  const n = Number(item?.metadata?.annotations?.[PENDING_UPDATES_ANNOTATION] || 0);
  return n > 0 ? n : 1;
}

const PENDING_KIND_LABELS = { created: "draft", settings: "settings", variables: "variables" };

export function pendingKindsSummary(kinds) {
  if (!kinds?.length) return "";
  const named = kinds.map((kind) => PENDING_KIND_LABELS[kind] || kind);
  if (named.length === 1) return named[0];
  if (named.length === 2) return `${named[0]} and ${named[1]}`;
  return `${named.slice(0, -1).join(", ")}, and ${named[named.length - 1]}`;
}

export function pendingChangeCopy(item) {
  const kinds = pendingChangeKinds(item);
  if (!kinds.length) return null;
  const actionLabel = item.spec?.deploy?.enabled ? "Deploy to update" : "Deploy";
  if (kinds.length === 1 && kinds[0] === "created") {
    const noun = isService(item) ? "service" : isObjectStore(item) ? "bucket" : "resource";
    return { message: `This ${noun} is a draft`, detail: "Change settings or variables, then deploy when you are ready.", action: actionLabel };
  }
  return {
    message: "You made these changes",
    detail: `${pendingKindsSummary(kinds)}. Do you want to deploy?`,
    action: actionLabel,
  };
}

export function deployPathForKind(kind, name) {
  return `/${kind}/${name}/deploy`;
}

export function retryProvisionPathForKind(kind, name) {
  return `/${kind}/${name}/retry-provision`;
}

export function provisionFailed(item) {
  return readyConditionReason(item) === "ProvisionFailed";
}

function readyConditionEntry(item) {
  return item?.status?.conditions?.find((row) => row.type === "Ready");
}

export function resourceProvisionAttempts(item) {
  return item?.status?.provision?.provisionAttempts || [];
}

/** Deploy history for UI, including a single synthetic row when status predates provision tracking. */
export function resourceProvisionAttemptsForUI(item) {
  const stored = resourceProvisionAttempts(item);
  if (stored.length) return stored;
  // A draft has never been deployed: the operator parks it in ReconcileError
  // ("configured and waiting for deployment") without recording an attempt, and
  // that must not be mistaken for a legacy in-flight deploy.
  if (isDraftResource(item)) return [];
  const reason = readyConditionReason(item);
  if (reason !== "ProvisionFailed" && reason !== "ReconcileError") return [];
  const entry = readyConditionEntry(item);
  const detail = latestProvisionFailureDetail(item);
  return [{
    id: "latest",
    phase: reason === "ProvisionFailed" ? "Failed" : "Running",
    startedAt: entry?.lastTransitionTime || entry?.lastUpdateTime,
    summary: reason === "ProvisionFailed" ? "Provisioning failed" : undefined,
    errors: detail && !isGenericProvisionUserCopy(detail) ? [{ message: detail }] : [],
    legacy: true,
  }];
}

function isGenericProvisionUserCopy(message) {
  if (!message) return true;
  const text = String(message).trim();
  if (text === "Provisioning failed") return true;
  return /update settings or platform configuration/i.test(text);
}

/** Short deploy label for drawer header / list (never the full status banner copy). */
export function deploymentRowHeadline(row) {
  if (!row) return "";
  const candidate = String(row.title || "").trim();
  if (candidate && !isGenericProvisionUserCopy(candidate) && candidate.length <= 120) {
    return candidate;
  }
  if (row.at) return `Deploy · ${formatRelativeTime(row.at)}`;
  return "Deploy";
}

export function latestProvisionFailureDetail(item) {
  const attempts = resourceProvisionAttempts(item);
  const failed = attempts.find((attempt) => attempt.phase === "Failed") || attempts[0];
  if (failed) {
    const errors = failed.errors || [];
    if (errors.length) return errors[errors.length - 1].message;
    return failed.summary || "";
  }
  return "";
}

export function formatProvisionAttemptLabel(attempt) {
  const started = attempt?.startedAt ? new Date(attempt.startedAt) : null;
  const when = started && !Number.isNaN(started.getTime())
    ? started.toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" })
    : "Deploy";
  return when;
}

export function formatRelativeTime(value) {
  const date = value ? new Date(value) : null;
  if (!date || Number.isNaN(date.getTime())) return "";
  const seconds = (date.getTime() - Date.now()) / 1000;
  const rtf = new Intl.RelativeTimeFormat(undefined, { numeric: "auto" });
  const steps = [
    ["year", 365 * 24 * 3600],
    ["month", 30 * 24 * 3600],
    ["week", 7 * 24 * 3600],
    ["day", 24 * 3600],
    ["hour", 3600],
    ["minute", 60],
  ];
  for (const [unit, size] of steps) {
    if (Math.abs(seconds) >= size) return rtf.format(Math.round(seconds / size), unit);
  }
  return rtf.format(Math.round(seconds), "second");
}

function deploymentTimestamp(value) {
  const t = new Date(value || 0).getTime();
  return Number.isNaN(t) ? 0 : t;
}

function rowFromProvisionAttempt(attempt, index) {
  const errors = attempt.errors || [];
  const phase = attempt.phase || "Running";
  let statusLabel = phase;
  if (phase === "Running") statusLabel = "In progress";
  if (phase === "Succeeded") statusLabel = "Succeeded";
  if (phase === "Failed") statusLabel = "Failed";
  const errorMessages = errors.map((e) => e.message).filter(Boolean);
  const lastError = errorMessages.length ? errorMessages[errorMessages.length - 1] : "";
  // Errors recorded on an attempt that went on to succeed were transient (a cache
  // race, an optimistic-concurrency conflict). They are history, not a live problem,
  // so they must not surface as `detail`, which the UI renders as a warning.
  const succeeded = phase === "Succeeded";
  return {
    id: attempt.id || `attempt-${index}`,
    logId: attempt.id || `attempt-${index}`,
    title: attempt.summary || (phase === "Failed" ? "Provisioning failed" : "Deploy"),
    meta: [attempt.buildRef ? `Build ${attempt.buildRef}` : "", attempt.deploymentRef ? `Release ${attempt.deploymentRef}` : ""].filter(Boolean).join(" · "),
    at: attempt.completedAt || attempt.startedAt,
    statusLabel,
    skipped: false,
    attemptId: attempt.id,
    deploymentName: attempt.deploymentRef || "",
    buildRef: attempt.buildRef || "",
    legacy: Boolean(attempt.legacy),
    errors: errorMessages,
    detail: succeeded ? "" : lastError,
    resolvedErrors: succeeded ? errorMessages : [],
  };
}

function rowFromGeassDeployment(dep, attempt, index) {
  const phase = String(dep.status?.phase || "Recorded");
  const skipped = phase.toLowerCase() === "skipped";
  return {
    id: resourceName(dep),
    logId: attempt?.id || resourceName(dep),
    title: dep.spec?.changeTitle || dep.spec?.image || "Deploy",
    meta: [dep.spec?.source, dep.spec?.actor].filter(Boolean).join(" · "),
    at: dep.status?.completedAt || dep.status?.startedAt || dep.metadata?.creationTimestamp,
    statusLabel: skipped ? "Skipped" : phase,
    skipped,
    attemptId: attempt?.id || "",
    deploymentName: resourceName(dep),
    buildRef: attempt?.buildRef || dep.spec?.buildRef || "",
  };
}

/** All deploy rows for list UI (newest first). Apps use GeassDeployment + provision attempts; buckets/DBs use attempts. */
export function resourceDeploymentList(item, geassDeployments = []) {
  const attempts = resourceProvisionAttempts(item);
  const byDeploymentRef = new Map();
  for (const attempt of attempts) {
    if (attempt.deploymentRef) byDeploymentRef.set(attempt.deploymentRef, attempt);
  }

  const rows = [];
  if (geassDeployments.length) {
    const sorted = [...geassDeployments].sort((a, b) => {
      return deploymentTimestamp(b.status?.completedAt || b.status?.startedAt || b.metadata?.creationTimestamp)
        - deploymentTimestamp(a.status?.completedAt || a.status?.startedAt || a.metadata?.creationTimestamp);
    });
    for (let index = 0; index < sorted.length; index += 1) {
      const dep = sorted[index];
      const name = resourceName(dep);
      rows.push(rowFromGeassDeployment(dep, byDeploymentRef.get(name), index));
    }
  }

  const linkedDeps = new Set(rows.map((row) => row.deploymentName));
  for (let index = 0; index < attempts.length; index += 1) {
    const attempt = attempts[index];
    if (attempt.deploymentRef && linkedDeps.has(attempt.deploymentRef)) continue;
    rows.push(rowFromProvisionAttempt(attempt, index));
  }

  if (!rows.length) {
    return resourceProvisionAttemptsForUI(item).map((attempt, index) => (
      enrichRailwayDeploymentRow(rowFromProvisionAttempt(attempt, index), index, item)
    ));
  }

  rows.sort((a, b) => deploymentTimestamp(b.at) - deploymentTimestamp(a.at));
  return rows.map((row, index) => enrichRailwayDeploymentRow(row, index, item));
}

function enrichRailwayDeploymentRow(row, index, item) {
  const ready = condition(item) === "True" || condition(item) === "Ready";
  const resourceFailed = resourceStatus(item) === "Failed";
  let badge = "REMOVED";
  if (row.skipped) badge = "SKIPPED";
  else if (index === 0) {
    if (row.statusLabel === "Failed" || resourceFailed) badge = "FAILED";
    else if (/progress|running/i.test(row.statusLabel)) badge = "ACTIVE";
    else if (ready && /recorded|succeeded/i.test(row.statusLabel)) badge = "ACTIVE";
    else if (!ready) badge = "ACTIVE";
  } else if (row.statusLabel === "Failed") badge = "FAILED";

  const outcomeOk = badge === "ACTIVE" && ready && !resourceFailed
    || row.statusLabel === "Succeeded"
    || (/recorded/i.test(row.statusLabel) && ready);
  let outcome = row.statusLabel;
  if (row.skipped) outcome = "Skipped";
  else if (outcomeOk) outcome = "Deployment successful";
  else if (badge === "FAILED" || row.statusLabel === "Failed") {
    const specific = (row.errors || []).find((msg) => !isGenericProvisionUserCopy(msg));
    outcome = specific || row.detail || "Provisioning failed";
    if (isGenericProvisionUserCopy(outcome)) outcome = "Provisioning failed";
  } else if (/progress|running/i.test(row.statusLabel)) outcome = "Provisioning…";

  return {
    ...row,
    title: deploymentRowHeadline(row),
    badge,
    outcome,
    outcomeOk,
    // Only the live/successful current deploy uses the ACTIVE card — failures belong in History.
    isActive: badge === "ACTIVE",
  };
}

/** Split live deploy (ACTIVE) vs History list — same as Railway. */
export function railwayDeploymentLayout(item, geassDeployments = []) {
  const rows = resourceDeploymentList(item, geassDeployments);
  const active = rows.find((row) => row.isActive) ?? null;
  const history = active ? rows.filter((row) => row !== active) : rows;
  return { active, history };
}

/** @deprecated use resourceDeploymentList */
export function deploymentHistoryRows(item, geassDeployments = []) {
  return resourceDeploymentList(item, geassDeployments);
}

function projectEnvMatch(item, projectName, environment) {
  if (item?.spec?.project !== projectName) return false;
  if (!environment) return true;
  return String(item?.spec?.environment || "") === String(environment);
}

export function countProjectPendingChanges(data, projectName, environment) {
  if (!data || !projectName) return 0;
  return projectPendingItems(data, projectName, environment).reduce((sum, row) => sum + row.count, 0);
}

function lastDeployedSpec(item) {
  const raw = item?.metadata?.annotations?.[LAST_DEPLOYED_ANNOTATION];
  if (!raw) return null;
  try {
    return JSON.parse(raw);
  } catch {
    return null;
  }
}

/**
 * Volume size the database was last deployed with, or "" while it is still a
 * draft. A deployed volume cannot shrink, so this is the floor for new sizes;
 * the spec may hold a larger value that is staged but not yet applied.
 */
export function deployedStorageSize(item) {
  if (!item?.spec?.deploy?.enabled) return "";
  return lastDeployedSpec(item)?.storageSize || item.spec?.storageSize || "";
}

function resourceLabel(item) {
  const buckets = item?.spec?.buckets;
  if (Array.isArray(buckets) && buckets.length) return buckets.join(", ");
  return "";
}

function pushChange(lines, text) {
  if (text && !lines.includes(text)) lines.push(text);
}

function formatResources(res) {
  const cpu = res?.requests?.cpu || res?.limits?.cpu;
  const memory = res?.requests?.memory || res?.limits?.memory;
  if (!cpu && !memory) return "";
  return [cpu, memory].filter(Boolean).join(" · ");
}

function diffObjectStoreChanges(item, live, deployed) {
  const lines = [];
  const liveBuckets = (live?.buckets || []).join(", ") || "(not set)";
  const deployedBuckets = (deployed?.buckets || []).join(", ") || "(not set)";
  if (liveBuckets !== deployedBuckets) {
    pushChange(lines, deployed ? `Bucket name: ${deployedBuckets} → ${liveBuckets}` : `Bucket name: ${liveBuckets}`);
  }
  if (Boolean(live?.createBucket) !== Boolean(deployed?.createBucket)) {
    pushChange(lines, live?.createBucket ? "Create bucket in cloud: enabled" : "Create bucket in cloud: disabled");
  }
  if ((live?.region || "") !== (deployed?.region || "")) {
    pushChange(lines, `Region: ${deployed?.region || "—"} → ${live?.region || "—"}`);
  }
  const liveRef = live?.connectionRef?.name || "";
  const deployedRef = deployed?.connectionRef?.name || "";
  if (liveRef !== deployedRef) {
    pushChange(lines, `AWS connection: ${deployedRef || "—"} → ${liveRef || "—"}`);
  }
  return lines;
}

function diffDatabaseChanges(live, deployed) {
  const lines = [];
  if ((live?.version || "") !== (deployed?.version || "")) {
    pushChange(lines, `Version: ${deployed?.version || "—"} → ${live?.version || "—"}`);
  }
  const liveSize = formatResources(live?.resources);
  const deployedSize = formatResources(deployed?.resources);
  if (liveSize !== deployedSize) {
    pushChange(lines, deployedSize ? `Size: ${deployedSize} → ${liveSize}` : `Size: ${liveSize || "updated"}`);
  }
  if (live?.highAvailability !== deployed?.highAvailability) {
    pushChange(lines, live?.highAvailability ? "High availability: enabled" : "High availability: disabled");
  }
  return lines;
}

function sourceName(source) {
  if (source?.function) return `function (${source.function.runtime})`;
  return source?.image?.image || source?.git?.repository;
}

function diffVariableChanges(live, deployed) {
  const lines = [];
  const liveConfig = live?.configData || {};
  const deployedConfig = deployed?.configData || {};
  for (const key of Object.keys(liveConfig).sort()) {
    if (!(key in deployedConfig)) pushChange(lines, `Variable added: ${key}`);
    else if (liveConfig[key] !== deployedConfig[key]) pushChange(lines, `Variable changed: ${key}`);
  }
  for (const key of Object.keys(deployedConfig).sort()) {
    if (!(key in liveConfig)) pushChange(lines, `Variable removed: ${key}`);
  }
  const names = (spec) => (spec?.env || []).map((entry) => entry.name);
  const liveEnv = names(live);
  const deployedEnv = names(deployed);
  liveEnv.filter((name) => !deployedEnv.includes(name)).forEach((name) => pushChange(lines, `Reference added: ${name}`));
  deployedEnv.filter((name) => !liveEnv.includes(name)).forEach((name) => pushChange(lines, `Reference removed: ${name}`));
  const liveShared = live?.sharedVariableRefs || [];
  const deployedShared = deployed?.sharedVariableRefs || [];
  liveShared.filter((name) => !deployedShared.includes(name)).forEach((name) => pushChange(lines, `Shared variable attached: ${name}`));
  deployedShared.filter((name) => !liveShared.includes(name)).forEach((name) => pushChange(lines, `Shared variable detached: ${name}`));
  return lines;
}

function diffAppChanges(live, deployed) {
  const lines = [];
  const liveImage = sourceName(live?.source);
  const deployedImage = sourceName(deployed?.source);
  if (liveImage !== deployedImage) {
    pushChange(lines, `Source: ${deployedImage || "—"} → ${liveImage || "—"}`);
  } else if (live?.source?.function && live.source.function.code !== deployed?.source?.function?.code) {
    pushChange(lines, "Function code updated");
  }
  if ((live?.port ?? 0) !== (deployed?.port ?? 0)) {
    pushChange(lines, `Port: ${deployed?.port || "default"} → ${live?.port || "default"}`);
  }
  if ((live?.ingress?.host || "") !== (deployed?.ingress?.host || "")) {
    pushChange(lines, `Domain: ${deployed?.ingress?.host || "none"} → ${live?.ingress?.host || "none"}`);
  }
  if ((live?.replicas ?? 1) !== (deployed?.replicas ?? 1)) {
    pushChange(lines, `Replicas: ${deployed?.replicas ?? 1} → ${live?.replicas ?? 1}`);
  }
  const liveSize = formatResources(live?.resources);
  const deployedSize = formatResources(deployed?.resources);
  if (liveSize !== deployedSize) {
    pushChange(lines, deployedSize ? `Size: ${deployedSize} → ${liveSize}` : `Size: ${liveSize}`);
  }
  const liveMax = live?.autoscaling?.maxReplicas;
  const deployedMax = deployed?.autoscaling?.maxReplicas;
  if (liveMax !== deployedMax) {
    pushChange(lines, `Autoscale max: ${deployedMax ?? "—"} → ${liveMax ?? "—"}`);
  }
  const liveSleep = live?.scaleToZero?.enabled ? `after ${Math.round((live.scaleToZero.idleSeconds || 300) / 60)} min idle` : "off";
  const deployedSleep = deployed?.scaleToZero?.enabled ? `after ${Math.round((deployed.scaleToZero.idleSeconds || 300) / 60)} min idle` : "off";
  if (liveSleep !== deployedSleep) {
    pushChange(lines, `Sleep when idle: ${deployedSleep} → ${liveSleep}`);
  }
  return lines;
}

/** Human-readable pending change lines for a single resource (for Details popover). */
export function resourcePendingChangeLines(item) {
  const kinds = pendingChangeKinds(item);
  const live = item?.spec || {};
  const deployed = lastDeployedSpec(item);
  const lines = [];

  for (const kind of kinds) {
    if (kind === "created") {
      pushChange(lines, "New resource — waiting for first deploy");
      continue;
    }
    if (kind === "variables") {
      const detail = isService(item) && deployed ? diffVariableChanges(live, deployed) : [];
      if (detail.length) detail.forEach((line) => pushChange(lines, line));
      else pushChange(lines, "Environment variables updated");
      continue;
    }
    if (kind === "settings") {
      let diff = [];
      if (isObjectStore(item)) diff = diffObjectStoreChanges(item, live, deployed);
      else if (isDatabase(item)) diff = diffDatabaseChanges(live, deployed);
      else if (isService(item)) diff = diffAppChanges(live, deployed);
      if (diff.length) {
        diff.forEach((line) => pushChange(lines, line));
      } else if (!deployed) {
        if (isObjectStore(item)) {
          const name = resourceLabel(item);
          pushChange(lines, name ? `Bucket name: ${name}` : "Bucket settings saved");
        } else if (isDatabase(item)) {
          pushChange(lines, formatResources(live.resources) ? `Size: ${formatResources(live.resources)}` : "Database settings saved");
        } else {
          pushChange(lines, "Service settings saved");
        }
      } else {
        pushChange(lines, "Settings updated — deploy to apply");
      }
    }
  }

  return lines.length ? lines : ["Configuration saved — deploy to apply"];
}

export function projectPendingItems(data, projectName, environment) {
  if (!data || !projectName) return [];
  const rows = [];
  const push = (segment, item, typeLabel) => {
    if (!projectEnvMatch(item, projectName, environment)) return;
    const kinds = pendingChangeKinds(item);
    if (!kinds.length) return;
    const name = resourceName(item);
    const label = resourceDisplayName(item);
    rows.push({
      segment,
      name: label,
      resourceId: name,
      typeLabel,
      summary: pendingKindsSummary(kinds),
      count: pendingChangeCount(item),
      changes: resourcePendingChangeLines(item),
      href: `/projects/${projectName}/${segment}/${name}?environment=${encodeURIComponent(environment || "")}`,
    });
  };
  for (const item of data.apps?.items || []) push("apps", item, "Service");
  for (const item of data.databases?.items || []) push("databases", item, item.spec?.engine || "Database");
  for (const item of data.objectStores?.items || []) {
    if (!item.spec?.project) continue;
    push("object-stores", item, item.spec?.engine === "S3" ? "S3 bucket" : "Bucket");
  }
  return rows;
}

function readyCondition(item) {
  return item?.status?.conditions?.find((row) => row.type === "Ready");
}

function readyConditionMessage(item) {
  const entry = readyCondition(item);
  return (entry?.message || entry?.reason || "").trim();
}

function readyConditionReason(item) {
  return (readyCondition(item)?.reason || "").trim();
}

export function isResourceDeleting(item) {
  return Boolean(item?.metadata?.deletionTimestamp);
}

function looksLikeOperatorMessage(raw) {
  return (
    /no such host|lookup .+\.svc/i.test(raw) ||
    /dial tcp/i.test(raw) ||
    /^Put \"http/i.test(raw) ||
    /EnsureBucket|s3:/i.test(raw) ||
    /\.svc\b|helmchart|geass-|kube-|reconcileerror|unsupported engine/i.test(raw) ||
    /\bnamespace\b/i.test(raw) ||
    /\bsecret\b/i.test(raw) ||
    /127\.0\.0\./.test(raw) ||
    /\bcontroller\b/i.test(raw) ||
    /credentials are unavailable/i.test(raw) ||
    raw.length > 160
  );
}

/** Classifies non-ready resources for status badges vs overview copy. */
export function resourceNotReadyKind(item) {
  const message = readyConditionMessage(item);
  const reason = readyConditionReason(item);
  if (
    /failed to start|service failed|storage is unavailable/i.test(message) ||
    reason === "RolloutFailed" ||
    reason === "ProvisionFailed"
  ) {
    return "failed";
  }
  if (/installing.*chart|storage is starting|service is starting|database is starting/i.test(message)) {
    return "provisioning";
  }
  if (
    reason === "ReconcileError" ||
    looksLikeOperatorMessage(message) ||
    /configure the bucket name|set up object storage|object storage credentials/i.test(message) ||
    /aws connection|external buckets require|aws credentials|access key and secret/i.test(message) ||
    /scale up the cluster|not enough capacity|cannot be scheduled/i.test(message)
  ) {
    return "unavailable";
  }
  return "provisioning";
}

export function resourceStatus(item) {
  if (isResourceDeleting(item)) return "Deleting";
  if (isDraftService(item) || isDraftObjectStore(item) || isDraftDatabase(item)) return "Draft";
  const ready = condition(item);
  if (ready === "True" || ready === "Ready") return ready;
  const kind = resourceNotReadyKind(item);
  if (kind === "failed") return "Failed";
  if (kind === "unavailable") return "Unavailable";
  return "Setting up";
}

function notReadyNoun(item) {
  if (isObjectStore(item)) return "bucket";
  if (isService(item)) return "service";
  if (isDatabase(item)) return "database";
  return "resource";
}

function genericUnavailableMessage(item) {
  const noun = notReadyNoun(item);
  if (isObjectStore(item)) {
    return `This ${noun} can't be provisioned until object storage is set up in Platform settings.`;
  }
  return `This ${noun} isn't available yet. Check Platform settings or your configuration.`;
}

function genericProvisioningMessage(item) {
  const noun = notReadyNoun(item);
  return `This ${noun} is still being set up. This usually takes a few minutes.`;
}

/** Plain-language Ready condition text for headers and overview (never operator or infra internals). */
export function userFacingReadyMessage(item) {
  if (isResourceDeleting(item)) {
    return "This resource is being removed. It will disappear from the canvas when shutdown finishes.";
  }
  const raw = readyConditionMessage(item);
  const reason = readyConditionReason(item);
  if (reason === "ProvisionFailed") {
    const detail = latestProvisionFailureDetail(item);
    if (detail) {
      return `Provisioning failed: ${detail} Update settings or platform configuration, then use Retry or deploy again.`;
    }
    return "Provisioning failed. Update settings or platform configuration, then use Retry or deploy again.";
  }
  if (!raw) return "";
  if (/waiting for deployment/i.test(raw)) {
    return "Finish configuration, then deploy to provision this resource.";
  }
  if (/configure the bucket name/i.test(raw)) {
    return "Add a bucket name in Settings, then deploy.";
  }
  if (/set up object storage/i.test(raw)) {
    return "Set up object storage in Platform settings, then try again.";
  }
  if (/object storage credentials/i.test(raw)) {
    return "Object storage isn't configured yet. Check Platform settings.";
  }
  if (/aws connection|external buckets require|aws credentials|access key and secret/i.test(raw)) {
    return "Connect your cloud account in Platform settings to use an external bucket.";
  }
  if (/installing.*chart/i.test(raw)) {
    return "Platform setup is still in progress. This can take a few minutes.";
  }
  if (/object storage is starting|service is starting/i.test(raw)) {
    return "Object storage is starting. Try again in a few minutes.";
  }
  if (/storage is starting/i.test(raw)) {
    return "Storage is starting. This can take a few minutes.";
  }
  if (/storage is unavailable/i.test(raw)) {
    return `Storage for this ${notReadyNoun(item)} is unavailable, so it can't start. Check that your cluster's nodes are healthy.`;
  }
  if (/scale up the cluster|not enough capacity|cannot be scheduled/i.test(raw)) {
    return "There isn't enough capacity for this size. Choose a smaller size or add capacity in Platform settings.";
  }
  if (/failed to start|service failed/i.test(raw)) {
    return `This ${notReadyNoun(item)} couldn't start. Review settings or try again later.`;
  }
  if (resourceNotReadyKind(item) === "unavailable") {
    return genericUnavailableMessage(item);
  }
  return genericProvisioningMessage(item);
}
