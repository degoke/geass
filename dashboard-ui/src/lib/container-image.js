/** Matches internal/dashboard validImageReference (registry-qualified ref, no whitespace). */
const IMAGE_REF_PATTERN = /^[A-Za-z0-9][A-Za-z0-9._/@:-]*$/;

export function validateContainerImageReference(image) {
  const value = String(image || "").trim();
  if (!value) {
    return { ok: false, message: "Enter a container image reference." };
  }
  if (/\s/.test(value)) {
    return { ok: false, message: "Image reference cannot contain spaces." };
  }
  if (!IMAGE_REF_PATTERN.test(value)) {
    return {
      ok: false,
      message: "Use a registry-qualified reference such as nginx:alpine or ghcr.io/org/app:tag.",
    };
  }
  return { ok: true, value };
}
