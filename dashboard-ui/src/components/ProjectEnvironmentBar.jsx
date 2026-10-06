import { Link } from "@tanstack/react-router";
import { Button, Group, Menu } from "@mantine/core";
import { ChevronDown, Plus } from "lucide-react";
import { resourceName } from "@/lib/api";

export function EnvironmentMenu({ project, environment, onChange }) {
  const envs = project?.spec?.environments || [];
  const current = environment || envs[0] || "";
  if (!current) return null;
  return (
    <Menu position="bottom-start">
      <Menu.Target>
        <button type="button" className="rw-crumb">
          {current}
          <ChevronDown size={14} />
        </button>
      </Menu.Target>
      <Menu.Dropdown>
        {envs.map((env) => (
          <Menu.Item key={env} onClick={() => onChange?.(env)}>{env}</Menu.Item>
        ))}
      </Menu.Dropdown>
    </Menu>
  );
}

export function ProjectEnvironmentBar({ project, environment, onChange, locked = false, trailing }) {
  const envs = project?.spec?.environments || [];
  const slug = project ? resourceName(project) : "";

  return (
    <Group justify="space-between" wrap="wrap">
      <Group gap="sm">
        {!locked ? <EnvironmentMenu project={project} environment={environment} onChange={onChange} /> : null}
        {locked && environment ? <Button variant="default" color="gray">{environment}</Button> : null}
        {slug && !locked ? (
          <Button
            variant="subtle"
            color="gray"
            component={Link}
            to={`/projects/${slug}/settings`}
            search={environment ? { environment } : undefined}
            leftSection={<Plus size={14} />}
          >
            Add environment
          </Button>
        ) : null}
      </Group>
      {trailing}
    </Group>
  );
}
