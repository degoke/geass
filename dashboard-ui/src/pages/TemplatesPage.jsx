import { useState } from "react";
import { Group, Modal, Text } from "@mantine/core";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Box, Database, SquareFunction, Trash2 } from "lucide-react";
import { action, api } from "@/lib/api";
import { canMutate } from "@/lib/dashboard";
import { alertUser } from "@/lib/notify";
import { Button, Empty, Muted } from "@/components/geass-ui";
import { TemplateDeployDialog } from "@/components/TemplateDeployDialog";

const KIND_ICON = { service: Box, function: SquareFunction, database: Database };

function resourceSummary(resource) {
  if (resource.kind === "database") return resource.engine;
  if (resource.kind === "function") return `Function · ${resource.runtime}`;
  return resource.image || resource.repository || "Service";
}

function TemplateCard({ template, mutable, onDeploy, onDelete }) {
  return (
    <article className="rw-template-card">
      <header>
        <h3>{template.name}</h3>
        {template.description ? <p>{template.description}</p> : null}
      </header>
      <ul className="rw-template-resources">
        {template.resources.map((resource) => {
          const Icon = KIND_ICON[resource.kind] || Box;
          return (
            <li key={resource.key}>
              <Icon size={14} aria-hidden />
              <span className="rw-template-resource-name">{resource.name || resource.key}</span>
              <span className="rw-template-resource-detail">{resourceSummary(resource)}</span>
            </li>
          );
        })}
      </ul>
      <footer>
        <Button size="sm" disabled={!mutable} onClick={() => onDeploy(template)}>Deploy</Button>
        {!template.builtin && mutable ? (
          <button type="button" className="rw-icon-btn" aria-label={`Delete ${template.name}`} onClick={() => onDelete(template)}>
            <Trash2 size={15} />
          </button>
        ) : null}
      </footer>
    </article>
  );
}

export function TemplatesPage({ data }) {
  const queryClient = useQueryClient();
  const mutable = canMutate(data);
  const templates = useQuery({ queryKey: ["templates"], queryFn: () => api("/api/templates"), enabled: mutable });
  const [deploying, setDeploying] = useState(null);
  const [deleting, setDeleting] = useState(null);
  const [busy, setBusy] = useState(false);

  const items = templates.data?.items || [];
  const builtin = items.filter((template) => template.builtin);
  const saved = items.filter((template) => !template.builtin);

  const remove = () => {
    setBusy(true);
    action(`/templates/${deleting.id}/delete`)
      .then(() => queryClient.invalidateQueries({ queryKey: ["templates"] }))
      .then(() => { setDeleting(null); alertUser("Template deleted"); })
      .catch((error) => alertUser(error.message))
      .finally(() => setBusy(false));
  };

  return (
    <div className="rw-templates-page">
      <h1>Templates</h1>
      <p className="rw-templates-lead">Start from a ready-made set of services and databases. Templates are added as drafts, so you review everything before it runs.</p>
      {!mutable ? (
        <Muted size="sm">Your viewer role cannot browse templates.</Muted>
      ) : templates.isPending ? (
        <Muted size="sm">Loading templates…</Muted>
      ) : templates.isError ? (
        <Muted size="sm">{templates.error.message}</Muted>
      ) : (
        <>
          <section>
            <h2>Starters</h2>
            <div className="rw-template-grid">
              {builtin.map((template) => (
                <TemplateCard key={template.id} template={template} mutable={mutable} onDeploy={setDeploying} />
              ))}
            </div>
          </section>
          <section>
            <h2>Your templates</h2>
            {saved.length ? (
              <div className="rw-template-grid">
                {saved.map((template) => (
                  <TemplateCard key={template.id} template={template} mutable={mutable} onDeploy={setDeploying} onDelete={setDeleting} />
                ))}
              </div>
            ) : (
              <Empty
                title="No saved templates yet"
                description="Open a project's settings and choose Generate Template from Project to save its services and databases here."
              />
            )}
          </section>
        </>
      )}
      <TemplateDeployDialog template={deploying} data={data} onClose={() => setDeploying(null)} />
      <Modal opened={Boolean(deleting)} onClose={() => { if (!busy) setDeleting(null); }} title="Delete template?" centered>
        <Text size="sm" mb="md">
          <strong>{deleting?.name}</strong> will be removed. Projects that already used it are not affected.
        </Text>
        <Group justify="flex-end" gap="sm">
          <Button variant="outline" disabled={busy} onClick={() => setDeleting(null)}>Cancel</Button>
          <Button variant="danger" loading={busy} onClick={remove}>Delete</Button>
        </Group>
      </Modal>
    </div>
  );
}
