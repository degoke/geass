import { useEffect, useState } from "react";
import { Group, Modal, Stack, Text } from "@mantine/core";
import { useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { action, list, resourceName } from "@/lib/api";
import { alertUser } from "@/lib/notify";
import { Button, Field, Select } from "@/components/geass-ui";

const NEW_PROJECT = "__new__";

/** Asks where to put a template, then creates its resources as drafts and opens the project. */
export function TemplateDeployDialog({ template, data, onClose }) {
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const projects = list(data, "projects");
  const [project, setProject] = useState(projects[0] ? resourceName(projects[0]) : NEW_PROJECT);
  const [environment, setEnvironment] = useState("");
  const [busy, setBusy] = useState(false);

  const selected = projects.find((item) => resourceName(item) === project);
  const environments = selected?.spec?.environments || [];

  useEffect(() => {
    setProject(projects[0] ? resourceName(projects[0]) : NEW_PROJECT);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [template?.id]);

  useEffect(() => {
    setEnvironment(environments[0] || "");
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [project, template?.id]);

  const deploy = async () => {
    setBusy(true);
    try {
      let target = project;
      let targetEnvironment = environment;
      if (project === NEW_PROJECT) {
        const created = await action("/projects/create");
        target = created.project;
        targetEnvironment = "production";
      }
      await action(`/templates/${template.id}/deploy`, { project: target, environment: targetEnvironment });
      await queryClient.invalidateQueries({ queryKey: ["dashboard", "bootstrap"] });
      onClose();
      navigate({ to: `/projects/${target}`, search: { environment: targetEnvironment } });
      alertUser(`${template.name} added as drafts. Review them, then deploy.`);
    } catch (error) {
      alertUser(error.message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal opened={Boolean(template)} onClose={() => { if (!busy) onClose(); }} title={template ? `Deploy ${template.name}` : ""} centered>
      <Stack gap="sm">
        <Text size="sm" c="dimmed">
          This adds {template?.resources?.length === 1 ? "one resource" : `${template?.resources?.length || 0} resources`} to your canvas as drafts.
          Nothing runs until you apply the changes.
        </Text>
        <Field label="Project">
          <Select value={project} onChange={(event) => setProject(event.currentTarget.value)} aria-label="Project">
            {projects.map((item) => (
              <option key={resourceName(item)} value={resourceName(item)}>{item.spec?.displayName || resourceName(item)}</option>
            ))}
            <option value={NEW_PROJECT}>New project</option>
          </Select>
        </Field>
        {project !== NEW_PROJECT && environments.length > 1 ? (
          <Field label="Environment">
            <Select value={environment} onChange={(event) => setEnvironment(event.currentTarget.value)} aria-label="Environment">
              {environments.map((env) => <option key={env} value={env}>{env}</option>)}
            </Select>
          </Field>
        ) : null}
        <Group justify="flex-end" gap="sm">
          <Button variant="outline" disabled={busy} onClick={onClose}>Cancel</Button>
          <Button loading={busy} onClick={deploy}>Add to project</Button>
        </Group>
      </Stack>
    </Modal>
  );
}
