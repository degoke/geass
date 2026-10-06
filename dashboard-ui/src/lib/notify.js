import { notifications } from "@mantine/notifications";

export function notify(message, tone = "neutral") {
  const text = String(message);
  const color = tone === "error" ? "red" : tone === "success" ? "green" : "gray";
  notifications.show({
    message: text,
    color,
    autoClose: 4200,
    withBorder: true,
    radius: "md",
  });
}

const SUCCESS_HINT =
  /\b(saved|connected|created|removed|deleted|updated|matches|copied|requested)\b/i;
const FAILURE_HINT = /\b(could not|failed|error|unavailable|invalid|forbidden|denied)\b/i;

function isSuccessMessage(text) {
  return SUCCESS_HINT.test(text) && !FAILURE_HINT.test(text);
}

/** User-facing confirmation (green) or problem (red). */
export function alertUser(message) {
  const text = String(message);
  notify(text, isSuccessMessage(text) ? "success" : "error");
}

export function alertError(message) {
  notify(String(message), "error");
}
