import { useState } from "react";
import { Group, Modal, Stack, Text } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import { Copy, Webhook } from "lucide-react";
import { action, api } from "@/lib/api";
import { alertUser, alertError } from "@/lib/notify";
import { formatRelativeTime } from "@/lib/services";
import { Button, Muted } from "@/components/geass-ui";
import { SettingsPanel } from "@/layout/SettingsShell";

/**
 * A URL that deploys one service when something calls it with POST: for tools that can call a URL but cannot send a
 * header, such as a registry notifying about a pushed image or a scheduler. The secret is in the URL, so it is shown
 * once, when it is made, and it is good for this service only.
 */
export function DeployHook({ project, environment, app, gitSource }) {
  const queryKey = ["api-tokens", project];
  const tokens = useQuery({
    queryKey,
    queryFn: () => api(`/api/tokens?project=${encodeURIComponent(project)}`),
    staleTime: 0,
    refetchOnMount: "always",
  });
  const hook = (tokens.data?.items || []).find((item) => item.scope === "service" && item.app === app);
  const [created, setCreated] = useState(null); // { token }
  const [deleting, setDeleting] = useState(false);
  const [busy, setBusy] = useState(false);

  const run = async (work) => {
    setBusy(true);
    try {
      return await work();
    } catch (error) {
      alertError(error.message);
      return null;
    } finally {
      setBusy(false);
    }
  };

  const create = async () => {
    const result = await run(() => action("/tokens/create", {
      name: `Deploy hook for ${app}`, scope: "service", project, environment, app, expiresInDays: 0,
    }));
    if (result) {
      setCreated(result);
      await tokens.refetch();
    }
  };

  const remove = async () => {
    const done = await run(() => action("/tokens/revoke", { id: hook.id }));
    if (done) {
      setDeleting(false);
      await tokens.refetch();
      alertUser("Deleted deploy hook");
    }
  };

  const copy = (text) => {
    navigator.clipboard?.writeText(text).then(() => alertUser("Copied"), () => alertUser("Could not copy"));
  };

  const url = created ? `${window.location.origin}/hooks/deploy/${created.token}` : "";

  return (
    <SettingsPanel>
      <Stack gap="xs">
        <Muted size="sm">
          {gitSource
            ? "Calling the URL rebuilds this service from its branch."
            : "Calling the URL deploys this service's pending changes."}
          {" "}Anyone who has the URL can deploy this service, so keep it secret.
        </Muted>
        {tokens.isLoading ? (
          <Muted size="sm">Loading…</Muted>
        ) : hook ? (
          <>
            <Group gap="xs" wrap="nowrap">
              <Webhook size={15} aria-hidden="true" />
              <Text size="sm">
                A deploy hook exists. Created {formatRelativeTime(hook.createdAt)}
                {hook.createdBy ? ` by ${hook.createdBy}` : ""} ·{" "}
                {hook.lastUsedAt ? `last called ${formatRelativeTime(hook.lastUsedAt)}` : "never called"}
              </Text>
            </Group>
            <Group gap="sm">
              <Button type="button" variant="outline" disabled={busy} onClick={() => setDeleting(true)}>Delete hook</Button>
            </Group>
            <Muted size="sm">The URL is shown only once, when the hook is made.</Muted>
          </>
        ) : (
          <Group gap="sm">
            <Button type="button" leftSection={<Webhook size={14} />} loading={busy} onClick={create}>Create deploy hook</Button>
          </Group>
        )}
      </Stack>

      <Modal opened={Boolean(created)} onClose={() => setCreated(null)} title="Your deploy hook" centered size="lg">
        <Stack gap="sm">
          <Text size="sm">Copy this URL now. It is shown only once.</Text>
          <div className="rw-webhook-secret">
            <code>{url}</code>
            <button type="button" className="rw-icon-btn" aria-label="Copy deploy hook URL" onClick={() => copy(url)}>
              <Copy size={15} />
            </button>
          </div>
          <Text size="xs" c="dimmed">Call it with POST:</Text>
          <pre className="rw-token-usage">{`curl -X POST ${url}`}</pre>
          <Group justify="flex-end"><Button onClick={() => setCreated(null)}>Done</Button></Group>
        </Stack>
      </Modal>

      <Modal opened={deleting} onClose={() => { if (!busy) setDeleting(false); }} title="Delete this deploy hook?" centered>
        <Text size="sm" mb="md">Anything that calls the current URL stops working immediately.</Text>
        <Group justify="flex-end" gap="sm">
          <Button variant="outline" disabled={busy} onClick={() => setDeleting(false)}>Cancel</Button>
          <Button variant="danger" loading={busy} onClick={remove}>Delete hook</Button>
        </Group>
      </Modal>
    </SettingsPanel>
  );
}
