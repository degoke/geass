import { Group, Text } from "@mantine/core";
import { Check } from "lucide-react";

export function Status({ value }) {
  const ready = value === "True" || value === "Ready" || value === "healthy" || value === true;
  const failed = value === "False" || value === "Failed" || value === "Error";
  const draft = value === "Draft";
  const color = ready ? "#59c77a" : failed ? "#e66a6a" : "#a3a3ab";
  const label = ready ? "Ready" : failed ? "Failed" : draft ? "Draft" : "Pending";
  return (
    <Group gap={6} wrap="nowrap">
      {ready ? <Check size={14} color={color} strokeWidth={2.5} /> : (
        <span style={{ width: 8, height: 8, borderRadius: 99, background: color, display: "inline-block" }} />
      )}
      <Text size="sm" c={color} fw={500}>{label}</Text>
    </Group>
  );
}
