import { condition } from "@/lib/api";

const PENDING_CHANGES_ANNOTATION = "geass.dev/pending-changes";
const PENDING_UPDATES_ANNOTATION = "geass.dev/pending-updates";

export function openResourceDialog() {
  window.dispatchEvent(new Event("geass:open-resource"));
}

export function isService(item) {
  return Boolean(item?.spec?.source);
}

export function isDraftService(item) {
  return isService(item) && !item?.spec?.deploy?.enabled;
}

export function pendingChangeKinds(item) {
  const raw = item?.metadata?.annotations?.[PENDING_CHANGES_ANNOTATION] || "";
  const kinds = raw.split(",").map((part) => part.trim()).filter(Boolean);
  const count = Number(item?.metadata?.annotations?.[PENDING_UPDATES_ANNOTATION] || 0);
  if (isDraftService(item) && !kinds.includes("created")) kinds.unshift("created");
  if (kinds.length) return kinds.filter((kind, index) => kinds.indexOf(kind) === index);
  if (count > 0) return ["settings"];
  return [];
}

export function pendingChangeCopy(item) {
  const kinds = pendingChangeKinds(item);
  if (!kinds.length) return null;
  const actionLabel = item.spec?.deploy?.enabled ? "Deploy to update" : "Deploy";
  if (kinds.length === 1 && kinds[0] === "created") {
    return { message: "Pending: service is still a draft", action: actionLabel };
  }
  const labels = { created: "draft", settings: "settings", variables: "variables" };
  const named = kinds.map((kind) => labels[kind] || kind);
  const listText = named.length === 1
    ? named[0]
    : named.length === 2
      ? `${named[0]} and ${named[1]}`
      : `${named.slice(0, -1).join(", ")}, and ${named[named.length - 1]}`;
  return { message: `Pending: ${listText} not deployed yet`, action: actionLabel };
}

export function resourceStatus(item) {
  if (isDraftService(item)) return "Draft";
  return condition(item);
}
