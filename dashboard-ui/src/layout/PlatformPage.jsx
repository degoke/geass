import { Stack, Text, Title } from "@mantine/core";

export function PlatformPage({ title, description, actions, children }) {
  return (
    <Stack gap="lg">
      <GroupHeader title={title} description={description} actions={actions} />
      {children}
    </Stack>
  );
}

function GroupHeader({ title, description, actions }) {
  return (
    <Stack gap={4}>
      <Title order={2}>{title}</Title>
      {description ? <Text size="sm" c="dimmed">{description}</Text> : null}
      {actions}
    </Stack>
  );
}
