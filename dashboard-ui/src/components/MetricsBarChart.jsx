import { Group, Progress, Stack, Text } from "@mantine/core";

export function MetricsBarChart({ metrics, caption }) {
  if (!metrics?.length) {
    return <Text size="xs" c="dimmed">No usage metrics yet.</Text>;
  }
  const values = metrics.map((metric) => {
    const numeric = Number.parseFloat(String(metric.value).replace(/[^0-9.]/g, ""));
    return Number.isFinite(numeric) ? numeric : 0;
  });
  const max = Math.max(...values, 1);
  return (
    <Stack gap="xs">
      {metrics.map((metric, index) => (
        <Group key={metric.title} gap="sm" wrap="nowrap" align="center">
          <Text size="xs" c="dimmed" w={140} truncate>{metric.title}</Text>
          <Progress value={Math.max(4, (values[index] / max) * 100)} color="signal" size="sm" style={{ flex: 1 }} />
        </Group>
      ))}
      {caption ? <Text size="xs" c="dimmed">{caption}</Text> : null}
    </Stack>
  );
}
