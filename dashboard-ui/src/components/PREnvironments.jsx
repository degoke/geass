import { useEffect, useState } from "react";
import { Group, Modal, Text } from "@mantine/core";
import { Link } from "@tanstack/react-router";
import { ExternalLink, GitPullRequest } from "lucide-react";
import { action, resourceName } from "@/lib/api";
import { alertUser, alertError } from "@/lib/notify";
import { formatRelativeTime } from "@/lib/services";
import { Button, Field, Input, Muted, Select, Switch } from "@/components/geass-ui";

/** Settings for pull request environments and the list of the ones that are open now. */
export function PREnvironments({ project, data, mutable, reload }) {
  const name = resourceName(project);
  const config = project.spec?.prEnvironments;
  const open = project.status?.pullRequests || [];
  const prEnvironments = new Set(open.map((entry) => entry.environment));
  const baseChoices = (project.spec?.environments || []).filter((env) => !prEnvironments.has(env));
  const githubLinked = Boolean(project.spec?.githubConnectionRef?.name);
  const githubReady = Boolean(data?.platform?.hasGitHubApp);

  const [enabled, setEnabled] = useState(Boolean(config?.enabled));
  const [base, setBase] = useState(config?.baseEnvironment || baseChoices[0] || "");
  const [maxOpen, setMaxOpen] = useState(String(config?.maxOpen || 5));
  const [saving, setSaving] = useState(false);
  const [closing, setClosing] = useState(null);

  useEffect(() => {
    setEnabled(Boolean(config?.enabled));
    setBase(config?.baseEnvironment || baseChoices[0] || "");
    setMaxOpen(String(config?.maxOpen || 5));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [config?.enabled, config?.baseEnvironment, config?.maxOpen]);

  const save = (event) => {
    event.preventDefault();
    setSaving(true);
    action(`/projects/${name}/pr-environments/save`, { ...(enabled ? { enabled: "on" } : {}), baseEnvironment: base, maxOpen })
      .then(() => { reload(); alertUser("Saved"); })
      .catch((error) => alertError(error.message))
      .finally(() => setSaving(false));
  };

  const close = () => {
    setSaving(true);
    action(`/projects/${name}/pr-environments/close`, { number: String(closing.number), repository: closing.repository })
      .then(() => { setClosing(null); reload(); alertUser(`Environment ${closing.environment} deleted`); })
      .catch((error) => alertError(error.message))
      .finally(() => setSaving(false));
  };

  return (
    <div className="rw-pr">
      <h3 className="rw-settings-subtitle">PR Environments</h3>
      <p className="rw-rail-body">
        Deleted when the pull request closes or merges. Forks are ignored.
      </p>
      {!githubLinked ? (
        <p className="rw-rail-hint">
          Link this project to GitHub first (<Link to={`/projects/${name}/settings`} className="rw-settings-text-btn">Integrations</Link>).
          {!githubReady ? " GitHub is not set up yet." : ""}
        </p>
      ) : null}
      <form className="rw-rail-form" onSubmit={save}>
        <Switch label="Create an environment for each pull request" checked={enabled} disabled={!mutable} onChange={(event) => setEnabled(event.currentTarget.checked)} />
        <Field label="Copy this environment" description="Buckets and external databases are not copied.">
          <Select value={base} onChange={(event) => setBase(event.currentTarget.value)} disabled={!mutable} aria-label="Base environment">
            {baseChoices.map((env) => <option key={env} value={env}>{env}</option>)}
          </Select>
        </Field>
        <Field label="Open at once">
          <Input type="number" min="1" max="25" value={maxOpen} disabled={!mutable} onChange={(event) => setMaxOpen(event.currentTarget.value)} aria-label="Maximum open PR environments" />
        </Field>
        <div className="rw-form-actions">
          <Button type="submit" loading={saving} disabled={!mutable}>Save</Button>
        </div>
      </form>
      <p className="rw-rail-hint">
        Needs the GitHub App subscribed to the <strong>Pull request</strong> event.
      </p>

      <h3 className="rw-settings-subtitle">Open pull requests</h3>
      {open.length ? (
        <div className="rw-rail-stack">
          {open.map((entry) => (
            <div className="rw-rail-list-row" key={`${entry.repository}#${entry.number}`}>
              <GitPullRequest size={16} aria-hidden="true" />
              <span className="rw-rail-list-title">#{entry.number}</span>
              <span className="rw-pr-meta">{entry.repository} · {entry.branch} · {formatRelativeTime(entry.openedAt)}</span>
              <span className="rw-list-row-end rw-pr-actions">
                <Link to={`/projects/${name}`} search={{ environment: entry.environment }} className="rw-settings-text-btn">{entry.environment}</Link>
                {entry.url ? (
                  <a href={entry.url} target="_blank" rel="noreferrer" className="rw-settings-text-btn" aria-label={`Open pull request ${entry.number} on GitHub`}>
                    <ExternalLink size={13} />
                  </a>
                ) : null}
                {mutable ? (
                  <button type="button" className="rw-settings-text-btn is-danger" onClick={() => setClosing(entry)}>Close</button>
                ) : null}
              </span>
            </div>
          ))}
        </div>
      ) : (
        <Muted size="sm" className="rw-settings-empty-inline">No open pull request environments.</Muted>
      )}

      <Modal opened={Boolean(closing)} onClose={() => { if (!saving) setClosing(null); }} title="Close pull request environment?" centered>
        <Text size="sm" mb="md">
          This deletes the <strong>{closing?.environment}</strong> environment and everything running in it.
        </Text>
        <Group justify="flex-end" gap="sm">
          <Button variant="outline" disabled={saving} onClick={() => setClosing(null)}>Cancel</Button>
          <Button variant="danger" loading={saving} onClick={close}>Delete environment</Button>
        </Group>
      </Modal>
    </div>
  );
}
