import { useState } from "react";
import { Stack, Text } from "@mantine/core";
import { useQueryClient } from "@tanstack/react-query";
import { action } from "@/lib/api";
import { formatWhen } from "@/lib/backups";
import { alertError } from "@/lib/notify";
import { AppLink } from "@/components/AppLink";
import { Badge, Button, DataTable, Field, FormStack, Input, Muted, PageSection } from "@/components/geass-ui";

const emptyForm = { bucket: "", region: "", accessKey: "", secretKey: "" };

/** What a found database says about itself, in the words the page uses. */
export function recoveredRow(entry) {
  return {
    key: `${entry.project}/${entry.database}/${entry.environment}`,
    project: entry.projectName || entry.project,
    projectId: entry.project,
    database: entry.database,
    engine: entry.engine,
    points: entry.restorePoints,
    newest: entry.newest ? formatWhen(entry.newest) : "—",
    until: entry.offeredUntil ? formatWhen(entry.offeredUntil) : "—",
    status: entry.status === "already-here" ? { label: "Already here", tone: "neutral" } : { label: "Found", tone: "success" },
  };
}

/**
 * Backups that Geass made live in the bucket, and what they are copies of lived in a cluster that may be gone. Given the bucket and
 * the key that Geass used for it, this finds them, makes the projects again under the names their folders carry, and offers each
 * database to restore like any other. It deletes nothing: the backups may belong to a cluster that is still running.
 */
export function RecoverBackups({ mutable }) {
  const queryClient = useQueryClient();
  const [open, setOpen] = useState(false);
  const [form, setForm] = useState(emptyForm);
  const [busy, setBusy] = useState(false);
  const [report, setReport] = useState(null);
  if (!mutable) return null;

  const set = (field) => (event) => setForm({ ...form, [field]: event.currentTarget.value });
  const find = async (event) => {
    event.preventDefault();
    setBusy(true);
    try {
      const found = await action("/backups/recover", form);
      setReport(found);
      setForm(emptyForm);
      await queryClient.invalidateQueries({ queryKey: ["dashboard", "bootstrap"] });
    } catch (error) {
      alertError(error.message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <PageSection title="Recover databases from a bucket">
      {!open && !report ? (
        <Button variant="outline" onClick={() => setOpen(true)}>Find backups in a bucket</Button>
      ) : null}
      {open && !report ? (
        <FormStack onSubmit={find}>
          <Field label="Bucket">
            <Input value={form.bucket} onChange={set("bucket")} required aria-label="Bucket" spellCheck={false} />
          </Field>
          <Field label="Region">
            <Input value={form.region} onChange={set("region")} aria-label="Region" placeholder="eu-north-1" spellCheck={false} />
          </Field>
          <Field label="AWS access key ID" description="The key Geass used for backups.">
            <Input value={form.accessKey} onChange={set("accessKey")} required aria-label="AWS access key ID" autoComplete="off" spellCheck={false} />
          </Field>
          <Field label="AWS secret access key">
            <Input type="password" value={form.secretKey} onChange={set("secretKey")} required aria-label="AWS secret access key" autoComplete="new-password" spellCheck={false} />
          </Field>
          <Muted size="sm">
            Missing projects are created again. Nothing in the bucket is changed or deleted.
          </Muted>
          <div>
            <Button type="submit" disabled={busy}>Find backups</Button>
            <Button ml="sm" variant="outline" type="button" onClick={() => setOpen(false)}>Cancel</Button>
          </div>
        </FormStack>
      ) : null}

      {report ? (
        <Stack gap="sm">
          <Text size="sm">
            {report.databases.length
              ? `Found backups of ${report.databases.length} ${report.databases.length === 1 ? "database" : "databases"}.`
              : "No backups found."}
            {report.projects?.length ? ` Made again: ${report.projects.join(", ")}.` : ""}
          </Text>
          {report.databases.length ? (
            <DataTable
              columns={[
                { label: "Project", key: "project" },
                { label: "Database", key: "database" },
                { label: "Engine", key: "engine" },
                { label: "Restore points", key: "points" },
                { label: "Newest", key: "newest" },
                { label: "Offered until", key: "until" },
                { label: "", align: "right", render: (row) => <Badge tone={row.status.tone}>{row.status.label}</Badge> },
                {
                  label: "",
                  align: "right",
                  render: (row) => <AppLink href={`/projects/${row.projectId}/settings`}>Open its settings</AppLink>,
                },
              ]}
              rows={report.databases.map(recoveredRow)}
              getRowKey={(row) => row.key}
            />
          ) : null}
          {report.skipped?.length ? (
            <Stack gap={4}>
              <Text size="sm" fw={600}>Left out</Text>
              {report.skipped.map((skipped) => (
                <Muted size="sm" key={skipped.folder}><code>{skipped.folder}</code>: {skipped.reason}</Muted>
              ))}
            </Stack>
          ) : null}
          <Muted size="sm">
            Offered for 30 days. Nothing in the bucket is deleted when that ends.
          </Muted>
          <div>
            <Button variant="outline" onClick={() => { setReport(null); setOpen(false); }}>Done</Button>
          </div>
        </Stack>
      ) : null}
    </PageSection>
  );
}
