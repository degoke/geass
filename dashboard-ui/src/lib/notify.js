import { notifications } from "@mantine/notifications";

export function notify(message, tone = "neutral", options = {}) {
  const text = String(message);
  const color = tone === "error" ? "red" : tone === "success" ? "signal" : "gray";
  notifications.show({
    message: text,
    color,
    autoClose: options.autoClose ?? 4200,
    withBorder: true,
    radius: "md",
  });
}

const SUCCESS_HINT =
  /\b(saved|connected|created|added|removed|deleted|updated|matches|copied|requested|deployed|enabled|paused|resumed|delivered)\b/i;
const FAILURE_HINT = /\b(could not|failed|error|unavailable|invalid|forbidden|denied)\b/i;

function isSuccessMessage(text) {
  return SUCCESS_HINT.test(text) && !FAILURE_HINT.test(text);
}

/** User-facing confirmation (green) or problem (red). */
export function alertUser(message) {
  const text = String(message);
  notify(text, isSuccessMessage(text) ? "success" : "error");
}

/** Confirms a template was added. Its notes are in the dialog it was added from, where they are read before the deploy. */
export function notifyTemplateAdded(template) {
  notify(`Added ${template.name}`, "success");
}

export function alertError(message) {
  notify(String(message), "error");
}
