import { Group, Text } from "@mantine/core";

/** A state is a shape and a colour, never colour alone: round is fine, square is broken, a ring is in progress. */
export function Status({ value }) {
  const ready = value === "True" || value === "Ready" || value === "healthy" || value === true;
  const failed = value === "False" || value === "Failed" || value === "Error";
  const draft = value === "Draft";
  const deleting = value === "Deleting";
  const unavailable = value === "Unavailable";
  const settingUp = value === "Setting up" || value === "Pending";
  const color = ready
    ? "var(--geass-healthy)"
    : failed
      ? "var(--geass-critical)"
      : unavailable || deleting
        ? "var(--geass-warning)"
        : "var(--geass-text-secondary)";
  const textColor = ready ? "var(--geass-signal-text)" : color;
  const label = ready
    ? "Ready"
    : failed
      ? "Failed"
      : draft
        ? "Draft"
        : deleting
          ? "Deleting"
          : unavailable
            ? "Unavailable"
            : settingUp
              ? "Setting up"
              : "Pending";
  const inProgress = !ready && !failed && !deleting && !unavailable;
  const mark = {
    width: 8,
    height: 8,
    flex: "none",
    display: "inline-block",
    borderRadius: failed ? 0 : 99,
    background: inProgress ? "transparent" : color,
    boxShadow: inProgress ? `inset 0 0 0 1.5px ${color}` : "none",
  };
  return (
    <Group gap={8} wrap="nowrap">
      <span style={mark} aria-hidden="true" />
      <Text size="sm" c={textColor} fw={500}>{label}</Text>
    </Group>
  );
}
