import { useMemo, useState } from "react";
import { Group, Menu, Modal, Stack, Text } from "@mantine/core";
import { Copy, MoreVertical, Plus, Webhook as WebhookIcon } from "lucide-react";
import { action, resourceName } from "@/lib/api";
import { alertUser, alertError } from "@/lib/notify";
import { formatRelativeTime } from "@/lib/services";
import { Button, Checkbox, Field, Input, Muted, Switch } from "@/components/geass-ui";

export const WEBHOOK_EVENTS = [
  { id: "deployment.started", label: "Deployment started" },
  { id: "deployment.succeeded", label: "Deployment succeeded" },
  { id: "deployment.failed", label: "Deployment failed" },
];

function hostOf(url) {
  try {
    return new URL(url).host;
  } catch {
    return url;
  }
}

function eventSummary(events) {
  if (!events?.length) return "All events";
  return events.map((id) => WEBHOOK_EVENTS.find((event) => event.id === id)?.label || id).join(", ");
}

function deliveryLabel(delivery) {
  if (!delivery) return "No deliveries yet";
  const when = formatRelativeTime(delivery.time);
  if (delivery.error) return `${delivery.statusCode ? `HTTP ${delivery.statusCode}` : "Failed"} · ${when}`;
  return `HTTP ${delivery.statusCode} · ${when}`;
}

/** Webhook management for a project: create, edit, pause, test, rotate the signing secret, delete. */
export function ProjectWebhooks({ project, mutable, reload }) {
  const name = resourceName(project);
  const hooks = project.spec?.webhooks || [];
  const deliveries = project.status?.webhookDeliveries || [];
  const [editing, setEditing] = useState(null); // null | { id?, url, events, disabled }
  const [secret, setSecret] = useState(null); // { id, value }
  const [removing, setRemoving] = useState(null);
  const [busy, setBusy] = useState(false);
  const [showDeliveries, setShowDeliveries] = useState(false);

  const lastByHook = useMemo(() => {
    const out = {};
    for (const delivery of deliveries) if (!out[delivery.webhookId]) out[delivery.webhookId] = delivery;
    return out;
  }, [deliveries]);

  const run = async (path, values, message) => {
    setBusy(true);
    try {
      const result = await action(`/projects/${name}/webhooks/${path}`, values);
      await reload?.();
      if (message) alertUser(message);
      return result;
    } catch (error) {
      alertError(error.message);
      return null;
    } finally {
      setBusy(false);
    }
  };

  const save = async (event) => {
    event.preventDefault();
    const values = {
      url: editing.url,
      event: editing.events,
      ...(editing.disabled ? { disabled: "on" } : {}),
      ...(editing.id ? { id: editing.id } : {}),
    };
    const result = await run("save", values, editing.id ? "Webhook updated" : null);
    if (!result) return;
    setEditing(null);
    if (result.secret) setSecret({ id: result.id, value: result.secret });
  };

  const sendTest = async (hook) => {
    const result = await run("test", { id: hook.id });
    if (!result) return;
    const { delivery } = result;
    alertUser(result.ok ? `Test delivered (HTTP ${delivery.statusCode})` : `Test failed: ${delivery.error || "no response"}`);
  };

  const rotate = async (hook) => {
    const result = await run("rotate", { id: hook.id });
    if (result?.secret) setSecret({ id: hook.id, value: result.secret });
  };

  const toggle = (hook) => run("save", {
    id: hook.id,
    url: hook.url,
    event: hook.events || [],
    ...(hook.disabled ? {} : { disabled: "on" }),
  }, hook.disabled ? "Webhook enabled" : "Webhook paused");

  const remove = async () => {
    const result = await run("delete", { id: removing.id }, "Webhook deleted");
    if (result) setRemoving(null);
  };

  const copySecret = () => {
    navigator.clipboard?.writeText(secret.value).then(
      () => alertUser("Signing secret copied"),
      () => alertUser("Could not copy"),
    );
  };

  const toggleEvent = (id, checked) => {
    const next = checked ? [...editing.events, id] : editing.events.filter((event) => event !== id);
    setEditing({ ...editing, events: next });
  };

  return (
    <div className="rw-webhooks">
      {hooks.length ? (
        <div className="rw-rail-stack">
          {hooks.map((hook) => (
            <div className="rw-webhook-row" key={hook.id}>
              <WebhookIcon size={16} aria-hidden="true" className="rw-webhook-icon" />
              <div className="rw-webhook-main">
                <div className="rw-webhook-url">
                  <span title={hook.url}>{hostOf(hook.url)}</span>
                  {hook.disabled ? <span className="rw-vars-kind">Paused</span> : null}
                </div>
                <div className="rw-webhook-meta">
                  {eventSummary(hook.events)} · {deliveryLabel(lastByHook[hook.id])}
                </div>
              </div>
              {mutable ? (
                <Menu position="bottom-end" withinPortal>
                  <Menu.Target>
                    <button type="button" className="rw-icon-btn" aria-label={`Actions for ${hostOf(hook.url)}`}>
                      <MoreVertical size={15} />
                    </button>
                  </Menu.Target>
                  <Menu.Dropdown>
                    <Menu.Item onClick={() => sendTest(hook)}>Send test</Menu.Item>
                    <Menu.Item onClick={() => setEditing({ id: hook.id, url: hook.url, events: hook.events || [], disabled: Boolean(hook.disabled) })}>Edit</Menu.Item>
                    <Menu.Item onClick={() => toggle(hook)}>{hook.disabled ? "Enable" : "Pause"}</Menu.Item>
                    <Menu.Item onClick={() => rotate(hook)}>Rotate signing secret</Menu.Item>
                    <Menu.Item color="red" onClick={() => setRemoving(hook)}>Delete</Menu.Item>
                  </Menu.Dropdown>
                </Menu>
              ) : null}
            </div>
          ))}
        </div>
      ) : (
        <Muted size="sm" className="rw-settings-empty-inline">No webhooks yet.</Muted>
      )}

      {mutable ? (
        <div className="rw-webhook-actions">
          <Button leftSection={<Plus size={14} />} onClick={() => setEditing({ url: "", events: [], disabled: false })}>New webhook</Button>
          {deliveries.length ? (
            <button type="button" className="rw-settings-text-btn" onClick={() => setShowDeliveries((open) => !open)}>
              {showDeliveries ? "Hide recent deliveries" : `Recent deliveries (${deliveries.length})`}
            </button>
          ) : null}
        </div>
      ) : null}

      {showDeliveries ? (
        <table className="rw-webhook-log">
          <thead>
            <tr><th>Time</th><th>Event</th><th>Webhook</th><th>Result</th></tr>
          </thead>
          <tbody>
            {deliveries.map((delivery) => {
              const hook = hooks.find((entry) => entry.id === delivery.webhookId);
              return (
                <tr key={delivery.deliveryId} className={delivery.error ? "is-failed" : ""}>
                  <td>{formatRelativeTime(delivery.time)}</td>
                  <td>{delivery.event}</td>
                  <td>{hook ? hostOf(hook.url) : "deleted webhook"}</td>
                  <td>
                    {delivery.error ? delivery.error : `HTTP ${delivery.statusCode}`}
                    {delivery.attempts > 1 ? ` · ${delivery.attempts} attempts` : ""}
                    {` · ${delivery.durationMillis} ms`}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      ) : null}

      <Modal opened={Boolean(editing)} onClose={() => { if (!busy) setEditing(null); }} title={editing?.id ? "Edit webhook" : "New webhook"} centered>
        {editing ? (
          <form onSubmit={save}>
            <Stack gap="sm">
              <Field label="Payload URL" description="Receives a JSON POST. Private network addresses are not allowed.">
                <Input
                  required
                  autoFocus
                  type="url"
                  value={editing.url}
                  placeholder="https://example.com/hooks/geass"
                  aria-label="Payload URL"
                  onChange={(event) => setEditing({ ...editing, url: event.currentTarget.value })}
                />
              </Field>
              <Field label="Events" description="Leave all unchecked to receive every event.">
                <Stack gap={6}>
                  {WEBHOOK_EVENTS.map((event) => (
                    <Checkbox
                      key={event.id}
                      label={event.label}
                      checked={editing.events.includes(event.id)}
                      onChange={(change) => toggleEvent(event.id, change.currentTarget.checked)}
                    />
                  ))}
                </Stack>
              </Field>
              {editing.id ? (
                <Switch label="Paused" checked={editing.disabled} onChange={(event) => setEditing({ ...editing, disabled: event.currentTarget.checked })} />
              ) : null}
              <Group justify="flex-end" gap="sm">
                <Button type="button" variant="outline" disabled={busy} onClick={() => setEditing(null)}>Cancel</Button>
                <Button type="submit" loading={busy}>{editing.id ? "Save" : "Create webhook"}</Button>
              </Group>
            </Stack>
          </form>
        ) : null}
      </Modal>

      <Modal opened={Boolean(secret)} onClose={() => setSecret(null)} title="Signing secret" centered size="lg">
        <Stack gap="sm">
          <Text size="sm">
            Copy this secret now. It is shown only once.
          </Text>
          <div className="rw-webhook-secret">
            <code>{secret?.value}</code>
            <button type="button" className="rw-icon-btn" aria-label="Copy signing secret" onClick={copySecret}><Copy size={15} /></button>
          </div>
          <Text size="xs" c="dimmed">
            <code>X-Geass-Signature: t=&lt;timestamp&gt;,v1=&lt;hex&gt;</code>, where v1 is the HMAC-SHA256 of{" "}
            <code>&lt;timestamp&gt;.&lt;raw body&gt;</code>.
          </Text>
          <Group justify="flex-end"><Button onClick={() => setSecret(null)}>Done</Button></Group>
        </Stack>
      </Modal>

      <Modal opened={Boolean(removing)} onClose={() => { if (!busy) setRemoving(null); }} title="Delete webhook?" centered>
        <Text size="sm" mb="md">
          Geass will stop sending events to <strong>{removing ? hostOf(removing.url) : ""}</strong>.
        </Text>
        <Group justify="flex-end" gap="sm">
          <Button variant="outline" disabled={busy} onClick={() => setRemoving(null)}>Cancel</Button>
          <Button variant="danger" loading={busy} onClick={remove}>Delete webhook</Button>
        </Group>
      </Modal>
    </div>
  );
}
