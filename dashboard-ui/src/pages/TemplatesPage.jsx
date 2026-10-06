import { Stack, Text, Title } from "@mantine/core";
import { Button } from "@/components/geass-ui";
import { Link } from "@tanstack/react-router";

export function TemplatesPage() {
  return (
    <div>
      <Title order={1} mb="md">Templates</Title>
      <Stack gap="sm" maw={520}>
        <Text size="sm" c="dimmed">
          Starter layouts for common stacks will live here. For now, create a project and add resources from the canvas.
        </Text>
        <Button component={Link} to="/projects">Go to projects</Button>
      </Stack>
    </div>
  );
}
