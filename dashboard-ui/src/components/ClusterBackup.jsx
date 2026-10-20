import { useState } from "react";
import { Stack, Text } from "@mantine/core";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { action, api } from "@/lib/api";
import { formatWhen } from "@/lib/backups";
import { DOCS_URL } from "@/lib/platform-nav";
import { alertError, notify } from "@/lib/notify";
import { Badge, Button, Field, FormStack, Input, KeyValueTable, Muted, PageSection, Switch } from "@/components/geass-ui";
import { CodePanel } from "@/components/CodePanel";

/** How copies of the cluster's state are doing, in the words and tones the rest of the page uses. */
export const BACKUP_STATES = {
  off: { label: "Off", tone: "neutral" },
  waiting: { label: "Waiting for the first copy", tone: "warning" },
  current: { label: "Copies are arriving", tone: "success" },
  failing: { label: "Copies are not arriving", tone: "danger" },
};

const INSTALLER = "curl -sfL https://github.com/degoke/geass/releases/latest/download/install.sh | sudo sh -s --";

const emptyForm = { bucket: "", region: "", endpoint: "", folder: "", insecure: false, accessKey: "", secretKey: "" };

/** The rows of the status table: where the copies go and the newest one. Only people who can change the setting see where. */
export function backupRows(settings) {
  if (!settings?.configured) return [];
  const rows = [];
  if (settings.bucket) rows.push({ label: "Bucket", value: settings.endpoint ? `${settings.bucket} on ${settings.endpoint}` : settings.bucket });
  if (settings.folder || settings.bucket) rows.push({ label: "Folder", value: settings.folder || "geass" });
  rows.push({ label: "Newest copy", value: settings.lastCopyTime ? formatWhen(settings.lastCopyTime) : "None yet" });
  if (settings.copies) rows.push({ label: "Copies in the bucket", value: String(settings.copies) });
  return rows;
}

/**
 * A copy of the cluster's own state (every project, service, secret and the records of your database backups) in a bucket
 * of your own, so that it can be rebuilt on a new machine if the control plane's disk is lost. K3s is told once, with the
 * installer, to send them; this says where, and takes effect with no restart. The access key is stored in a Secret and
 * never comes back to the browser.
 */
export function ClusterBackup({ mutable }) {
  const queryClient = useQueryClient();
  const settings = useQuery({
    queryKey: ["etcd-snapshots"],
    queryFn: () => api("/api/settings/etcd-snapshots"),
    staleTime: 0,
    refetchOnMount: "always",
    refetchInterval: 60_000,
  });
  const [editing, setEditing] = useState(false);
  const [form, setForm] = useState(emptyForm);
  const [busy, setBusy] = useState("");
  const [confirming, setConfirming] = useState(false);

  if (settings.isLoading) return null;
  if (settings.isError) {
    return (
      <PageSection title="Cluster backup">
        <Muted size="sm">{settings.error.message}</Muted>
      </PageSection>
    );
  }
  const saved = settings.data;
  const state = BACKUP_STATES[saved.state] || BACKUP_STATES.off;
  const set = (field) => (event) => setForm({ ...form, [field]: event.currentTarget.value });
  const keepKey = saved.configured && saved.hasKey && form.accessKey === "" && form.secretKey === "";

  const open = () => {
    setForm(saved.configured
      ? { ...emptyForm, bucket: saved.bucket || "", region: saved.region || "", endpoint: saved.endpoint || "", folder: saved.folder || "", insecure: Boolean(saved.insecure) }
      : emptyForm);
    setEditing(true);
  };

  const run = async (key, path, values, done) => {
    setBusy(key);
    try {
      await action(path, values);
      await queryClient.invalidateQueries({ queryKey: ["etcd-snapshots"] });
      if (done) notify(done, "success");
      return true;
    } catch (error) {
      alertError(error.message);
      return false;
    } finally {
      setBusy("");
    }
  };

  const save = async (event) => {
    event.preventDefault();
    const stored = await run("save", "/settings/etcd-snapshots/save", {
      ...form,
      insecure: form.insecure ? "on" : undefined,
      keepKey: keepKey ? "on" : undefined,
    }, "Saved. K3s on the control plane sends the first copy at its next snapshot, at 00:00 or 12:00 server time.");
    if (stored) {
      setEditing(false);
      setForm(emptyForm);
    }
  };

  const turnOff = async () => {
    if (await run("clear", "/settings/etcd-snapshots/clear", {}, "The cluster no longer names a bucket.")) setConfirming(false);
  };

  return (
    <PageSection
      title="Cluster backup"
      description="A copy of the cluster's own state in a bucket of your own, so that it can be rebuilt on a new machine if the control plane's disk is lost."
      actions={<Badge tone={state.tone}>{state.label}</Badge>}
    >
      {saved.message ? <Muted size="sm">{saved.message}</Muted> : (
        <Muted size="sm">
          Off. K3s keeps its own snapshots on the same disk as the cluster, so they are lost with it. Copies in a bucket of your own are what a rebuild starts from.
        </Muted>
      )}
      {backupRows(saved).length ? <KeyValueTable items={backupRows(saved)} /> : null}

      {mutable && !editing ? (
        <Stack gap="xs" mt="md">
          <div>
            <Button onClick={open}>{saved.configured ? "Change bucket" : "Set up"}</Button>
            {saved.configured ? (
              confirming ? (
                <>
                  <Button ml="sm" variant="danger" disabled={busy === "clear"} onClick={turnOff}>Stop naming this bucket</Button>
                  <Button ml="sm" variant="outline" onClick={() => setConfirming(false)}>Keep it</Button>
                </>
              ) : (
                <Button ml="sm" variant="outline" onClick={() => setConfirming(true)}>Turn off</Button>
              )
            ) : null}
          </div>
          {confirming ? (
            <Muted size="sm">
              This takes the bucket and its key out of the cluster. Copies already in the bucket stay there. To stop K3s on the control plane sending them
              too, run the installer on it with <code>etcd-snapshots disable</code>.
            </Muted>
          ) : null}
        </Stack>
      ) : null}

      {mutable && editing ? (
        <FormStack onSubmit={save}>
          <Field label="Bucket" description="One made for this alone, with encryption on and public access blocked: a copy holds every secret in the cluster.">
            <Input value={form.bucket} onChange={set("bucket")} required aria-label="Bucket" placeholder="acme-geass-cluster-state" spellCheck={false} />
          </Field>
          <Field label="Region" description="The bucket's region. Leave empty for a server that has no regions.">
            <Input value={form.region} onChange={set("region")} aria-label="Region" placeholder="eu-north-1" spellCheck={false} />
          </Field>
          <Field label="Server (not AWS)" description="The address of an S3-compatible server, with no https:// in front. Leave empty for AWS S3.">
            <Input
              value={form.endpoint}
              // The switch below is only there while a server is named: with none the copies go to AWS, which is never plain HTTP.
              onChange={(event) => setForm({ ...form, endpoint: event.currentTarget.value, insecure: event.currentTarget.value ? form.insecure : false })}
              aria-label="Server"
              placeholder="minio.example.com:9000"
              spellCheck={false}
            />
          </Field>
          {form.endpoint ? (
            <Switch
              checked={form.insecure}
              onChange={(event) => setForm({ ...form, insecure: event.currentTarget.checked })}
              label="Send over plain HTTP (only on a network that is closed to everyone else)"
            />
          ) : null}
          <Field label="Folder" description="Where in the bucket the copies go. geass when left empty.">
            <Input value={form.folder} onChange={set("folder")} aria-label="Folder" placeholder="geass" spellCheck={false} />
          </Field>
          <Field label="Access key ID" description={saved.configured && saved.hasKey ? "A key is stored. Leave both fields empty to keep it." : "The key of a user that can use this bucket and nothing else."}>
            <Input value={form.accessKey} onChange={set("accessKey")} aria-label="Access key ID" autoComplete="off" spellCheck={false} />
          </Field>
          <Field label="Secret access key">
            <Input type="password" value={form.secretKey} onChange={set("secretKey")} aria-label="Secret access key" autoComplete="new-password" spellCheck={false} />
          </Field>
          <div>
            <Button type="submit" disabled={busy === "save"}>Save</Button>
            <Button ml="sm" variant="outline" type="button" onClick={() => setEditing(false)}>Cancel</Button>
          </div>
        </FormStack>
      ) : null}

      {mutable ? (
        <Stack gap="xs" mt="md">
          <Text size="sm" fw={600}>What this needs</Text>
          <Muted size="sm">
            K3s on the control plane has to be told to send copies, once. If you did not answer yes when you installed, run this on the control plane. It
            restarts K3s, which takes the API server away for a minute and does not stop what is running:
          </Muted>
          <CodePanel code={`${INSTALLER} etcd-snapshots enable`} />
          <Muted size="sm">
            A user for the bucket that can do only this (an AWS IAM policy; other servers have their own way to say the same):
          </Muted>
          <CodePanel code={saved.policy} language="json" />
          <Muted size="sm">
            <strong>Keep the cluster&apos;s server token somewhere that is not that machine.</strong> A copy cannot be restored without it, and it is on the disk
            the copies are for: <code>sudo cat /var/lib/rancher/k3s/server/token</code>. How to rebuild a cluster from a copy is in{" "}
            <a href={`${DOCS_URL}operations/disaster-recovery/`} target="_blank" rel="noreferrer">the disaster recovery guide</a>.
          </Muted>
        </Stack>
      ) : null}
    </PageSection>
  );
}
