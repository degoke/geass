import { Group, Stack, Text, UnstyledButton } from "@mantine/core";
import { Link } from "@tanstack/react-router";
import { Status } from "@/components/Status";
import { resourceStatus } from "@/lib/services";
import { resourceName } from "@/lib/api";

export function ResourceCompactRow({ href, item, kind }) {
  const name = resourceName(item);
  const status = resourceStatus(item);
  return (
    <UnstyledButton component={Link} to={href} w="100%" className="geass-resource-compact">
      <Group justify="space-between" align="center" wrap="nowrap" gap="sm">
        <Stack gap={2} style={{ minWidth: 0, textAlign: "left" }}>
          <Text size="sm" fw={500} truncate>{name}</Text>
          <Text size="xs" className="geass-muted" truncate>{kind}</Text>
        </Stack>
        <Status value={status} />
      </Group>
    </UnstyledButton>
  );
}
