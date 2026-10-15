import { useMemo, useState } from "react";
import { Group, Menu, Modal, Stack, Text } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import { Hash, Mail, MoreVertical, Plus } from "lucide-react";
import { action, api, resourceName } from "@/lib/api";
import { alertError, notify } from "@/lib/notify";
import { formatRelativeTime } from "@/lib/services";
import { Badge, Button, Checkbox, Field, Input, Muted, Select, Switch, Textarea } from "@/components/geass-ui";

const SECURITY_SHORT = { starttls: "STARTTLS", tls: "TLS", none: "no encryption" };

const SECURITY = [
  { value: "starttls", label: "STARTTLS (port 587)" },
  { value: "tls", label: "TLS from the start (port 465 only)" },
  { value: "none", label: "None (no password can be used)" },
];

const emptySMTP = { host: "", port: "587", security: "starttls", username: "", password: "", from: "" };

const blankChannel = (type, kinds) => ({
  type,
  name: "",
  url: "",
  recipients: "",
  events: kinds.map((kind) => kind.id),
  projects: [],
  environments: [],
  notifyResolved: true,
  disabled: false,
});

const toggle = (list, value, on) => (on ? [...list.filter((item) => item !== value), value] : list.filter((item) => item !== value));

function describeScope(channel, projectNames) {
  const parts = [];
  parts.push(channel.projects.length ? channel.projects.map((name) => projectNames[name] || name).join(", ") : "all projects");
  if (channel.environments.length) parts.push(channel.environments.join(", "));
  return parts.join(" · ");
}

function describeEvents(channel, kinds) {
  if (!channel.events.length || channel.events.length === kinds.length) return "all alerts";
  return channel.events.map((id) => kinds.find((kind) => kind.id === id)?.label || id).join(", ");
}

function severityBadge(alert) {
  return <Badge tone={alert.severity === "critical" ? "danger" : "warning"}>{alert.severity === "critical" ? "Critical" : "Warning"}</Badge>;
}

/** Where a firing alert is: the project and environment, or the cluster. */
function whereIs(alert) {
  return [alert.projectName || alert.project, alert.environment].filter(Boolean).join(" · ") || "Cluster";
}

const INTEGRATIONS = { slack: "Slack", email: "email" };

/**
 * Where Geass tells people that something went wrong: a mail server for email, and channels that post to Slack or write
 * to a list of addresses. Geass does not send the alerts itself: the cluster's Alertmanager does, which is part of the
 * monitoring add-on. The Slack address and the mail password are secrets: they are stored in Secrets and never come back
 * to the browser. A channel can be limited to some kinds of alert, some projects and some environments.
 */
export function AlertSettings({ mutable, projects = [] }) {
  const settings = useQuery({
    queryKey: ["alert-settings"],
    queryFn: () => api("/api/settings/alerts"),
    enabled: mutable,
    staleTime: 0,
    refetchOnMount: "always",
    refetchInterval: 30000,
  });
  const [editing, setEditing] = useState(null);
  const [smtp, setSMTP] = useState(null);
  const [removing, setRemoving] = useState(null);
  const [removingSMTP, setRemovingSMTP] = useState(false);
  const [busy, setBusy] = useState("");

  const saved = settings.data;
  const kinds = saved?.kinds || [];
  const projectNames = useMemo(() => Object.fromEntries(projects.map((project) => [resourceName(project), project.spec?.displayName || resourceName(project)])), [projects]);
  const environmentNames = useMemo(() => {
    const names = new Set(["production"]);
    projects.forEach((project) => {
      const pullRequests = new Set((project.status?.pullRequests || []).map((pullRequest) => pullRequest.environment));
      (project.spec?.environments || []).forEach((name) => {
        if (!pullRequests.has(name)) names.add(name);
      });
    });
    return [...names].sort();
  }, [projects]);

  if (!mutable) {
    return <Muted size="sm" className="rw-settings-empty-inline">Only admins can see and change alert settings.</Muted>;
  }
  if (settings.isLoading) return <Muted size="sm" className="rw-settings-empty-inline">Loading alert settings…</Muted>;
  if (settings.isError) return <Muted size="sm" className="rw-settings-empty-inline">{settings.error.message}</Muted>;

  const run = async (key, path, values, done) => {
    setBusy(key);
    try {
      const result = await action(path, values);
      await settings.refetch();
      if (done) notify(done, "success");
      // Saved, but Alertmanager has not been told yet: the page shows it, and it is not a failure to try again.
      if (result?.warning) notify(result.warning, "neutral", { autoClose: 9000 });
      return result || true;
    } catch (error) {
      alertError(error.message);
      // What was typed is saved even when Alertmanager could not be told yet, so the page shows it.
      await settings.refetch();
      return null;
    } finally {
      setBusy("");
    }
  };

  const channelValues = (channel) => ({
    ...(channel.id ? { id: channel.id } : {}),
    name: channel.name,
    type: channel.type,
    url: channel.url,
    recipients: channel.recipients,
    event: channel.events,
    project: channel.projects,
    environment: channel.environments,
    ...(channel.notifyResolved ? { notifyResolved: "on" } : {}),
    ...(channel.disabled ? { disabled: "on" } : {}),
  });

  const saveChannel = async (event) => {
    event.preventDefault();
    if (!editing.events.length) {
      alertError("Choose at least one kind of alert.");
      return;
    }
    const result = await run("channel", "/settings/alerts/channels/save", channelValues(editing), editing.id ? "Channel saved" : "Channel added");
    if (result) setEditing(null);
  };

  // A test goes through Alertmanager, the way a real alert does, so it can take up to a minute: Alertmanager has to have
  // loaded the channel first, and then says whether it sent the message.
  const testChannel = async (channel) => {
    setBusy(`test-${channel.id}`);
    notify(`Sending a test to ${channel.name} through Alertmanager…`);
    try {
      const result = await action("/settings/alerts/channels/test", { id: channel.id });
      if (result.ok) notify(`Test sent to ${channel.name}`, "success");
      else alertError(result.error || `The test to ${channel.name} did not go through`);
    } catch (error) {
      alertError(error.message);
    } finally {
      setBusy("");
    }
  };

  const toggleChannel = (channel) =>
    run("channel", "/settings/alerts/channels/save", channelValues({
      ...channel,
      url: "",
      recipients: channel.recipients.join("\n"),
      events: channel.events,
      projects: channel.projects,
      environments: channel.environments,
      disabled: !channel.disabled,
    }), channel.disabled ? "Channel resumed" : "Channel paused");

  const editChannel = (channel) =>
    setEditing({
      id: channel.id,
      type: channel.type,
      name: channel.name,
      url: "",
      hasSecret: channel.hasSecret,
      target: channel.target,
      recipients: channel.recipients.join("\n"),
      events: channel.events.length ? channel.events : kinds.map((kind) => kind.id),
      projects: channel.projects,
      environments: channel.environments,
      notifyResolved: channel.notifyResolved,
      disabled: channel.disabled,
    });

  const remove = async () => {
    const result = await run("remove", "/settings/alerts/channels/delete", { id: removing.id }, "Channel deleted");
    if (result) setRemoving(null);
  };

  const saveSMTP = async (event) => {
    event.preventDefault();
    const keep = Boolean(saved.smtp?.hasPassword) && smtp.password === "" && smtp.username === saved.smtp.username;
    const result = await run("smtp", "/settings/alerts/smtp/save", { ...smtp, ...(keep ? { keepPassword: "on" } : {}) }, "Mail server saved");
    if (result) setSMTP(null);
  };

  const testSMTP = async () => {
    setBusy("smtp-test");
    try {
      const result = await action("/settings/alerts/smtp/test");
      if (result.ok) notify(saved.smtp?.username ? "Connected to the mail server and signed in" : "Connected to the mail server", "success");
      else alertError(result.error || "Could not reach the mail server");
    } catch (error) {
      alertError(error.message);
    } finally {
      setBusy("");
    }
  };

  const removeSMTP = async () => {
    const result = await run("smtp-remove", "/settings/alerts/smtp/clear", {}, "Mail server removed");
    if (result) setRemovingSMTP(false);
  };

  const openSMTP = () =>
    setSMTP(saved.smtp
      ? { host: saved.smtp.host, port: String(saved.smtp.port), security: saved.smtp.security, username: saved.smtp.username, password: "", from: saved.smtp.from }
      : emptySMTP);
  const field = (setter, current, name) => (event) => setter({ ...current, [name]: event.currentTarget.value });
  // Alertmanager connects encrypted from the first byte only on port 465, so the two go together.
  const chooseSecurity = (event) => {
    const security = event.currentTarget.value;
    const port = security === "tls" && smtp.port === "587" ? "465" : security === "starttls" && smtp.port === "465" ? "587" : smtp.port;
    setSMTP({ ...smtp, security, port, ...(security === "none" ? { username: "", password: "" } : {}) });
  };
  const needsMailServer = !saved.smtp && saved.channels.some((channel) => channel.type === "email");
  const failedDeliveries = Object.entries(saved.deliveries || {}).filter(([, delivery]) => delivery.failed > 0);

  return (
    <div className="rw-webhooks rw-alerts">
      <p className="rw-rail-body">
        Geass tells you when a build or a deploy fails, a service is down, a database&apos;s backups are failing, a pod keeps
        crashing, storage is filling, or a node stops responding. Add a Slack channel or a list of email addresses, and choose which
        alerts each one hears. Alerts that belong to the cluster itself (nodes, certificates, upgrades) go to channels that are not
        limited to projects.
      </p>

      <Stack gap="xs">
        <Group gap="sm">
          <Text fw={600} size="sm">Alerting</Text>
          <Badge tone={saved.listening ? "success" : "neutral"}>{saved.listening ? "On" : "Off"}</Badge>
          {!saved.listening ? <Muted size="sm">Add a channel to turn it on.</Muted> : null}
        </Group>
        {!saved.alertmanager.ready ? (
          <Muted size="sm" c="red.4">
            Alertmanager cannot be reached, so nothing can be sent. It is part of the monitoring add-on, which has to be on for alerts.
            {saved.alertmanager.error ? ` (${saved.alertmanager.error})` : ""}
          </Muted>
        ) : null}
        {failedDeliveries.map(([integration, delivery]) => (
          <Muted size="sm" c="red.4" key={integration}>
            Alertmanager has failed to send {delivery.failed} of {delivery.sent} requests to {INTEGRATIONS[integration] || integration} since it started. Check
            the webhook URL or the mail server, and that the monitoring namespace can reach it.
          </Muted>
        ))}
      </Stack>

      <Group justify="space-between" align="center">
        <Text fw={600} size="sm">Channels</Text>
        <Group gap="sm">
          <Button leftSection={<Plus size={14} />} onClick={() => setEditing(blankChannel("slack", kinds))}>Slack channel</Button>
          <Button variant="outline" leftSection={<Plus size={14} />} onClick={() => setEditing(blankChannel("email", kinds))}>Email</Button>
        </Group>
      </Group>

      {saved.channels.length ? (
        <div className="rw-rail-stack">
          {saved.channels.map((channel) => {
            const Icon = channel.type === "slack" ? Hash : Mail;
            return (
              <div className="rw-webhook-row" key={channel.id}>
                <Icon size={16} aria-hidden="true" className="rw-webhook-icon" />
                <div className="rw-webhook-main">
                  <div className="rw-webhook-url">
                    <span>{channel.name}</span>
                    {channel.disabled ? <span className="rw-vars-kind">Paused</span> : null}
                    {channel.type === "slack" && !channel.hasSecret ? <Badge tone="danger">No webhook URL</Badge> : null}
                  </div>
                  <div className="rw-webhook-meta">
                    {channel.type === "email" ? channel.recipients.join(", ") : channel.target || "Slack"}
                    {" · "}{describeEvents(channel, kinds)}{" · "}{describeScope(channel, projectNames)}
                  </div>
                </div>
                <Menu position="bottom-end" withinPortal>
                  <Menu.Target>
                    <button type="button" className="rw-icon-btn" aria-label={`Actions for ${channel.name}`}>
                      <MoreVertical size={15} />
                    </button>
                  </Menu.Target>
                  <Menu.Dropdown>
                    <Menu.Item disabled={Boolean(busy)} onClick={() => testChannel(channel)}>Send test</Menu.Item>
                    <Menu.Item onClick={() => editChannel(channel)}>Edit</Menu.Item>
                    <Menu.Item disabled={Boolean(busy)} onClick={() => toggleChannel(channel)}>{channel.disabled ? "Resume" : "Pause"}</Menu.Item>
                    <Menu.Item color="red" onClick={() => setRemoving(channel)}>Delete</Menu.Item>
                  </Menu.Dropdown>
                </Menu>
              </div>
            );
          })}
        </div>
      ) : (
        <Muted size="sm" className="rw-settings-empty-inline">No channels yet.</Muted>
      )}

      <Stack gap="xs">
        <Group justify="space-between" align="center">
          <Text fw={600} size="sm">Mail server</Text>
          {saved.smtp ? (
            <Group gap="sm">
              <Button variant="outline" loading={busy === "smtp-test"} disabled={Boolean(busy)} onClick={testSMTP}>Test connection</Button>
              <Button variant="outline" disabled={Boolean(busy)} onClick={openSMTP}>Edit</Button>
              <Button variant="outline" disabled={Boolean(busy)} onClick={() => setRemovingSMTP(true)}>Remove</Button>
            </Group>
          ) : (
            <Button variant="outline" onClick={openSMTP}>Set up mail server</Button>
          )}
        </Group>
        {saved.smtp ? (
          <Muted size="sm">
            {saved.smtp.host}:{saved.smtp.port} ({SECURITY_SHORT[saved.smtp.security] || saved.smtp.security})
            {saved.smtp.username ? ` · signs in as ${saved.smtp.username}` : " · no sign-in"} · from {saved.smtp.from}
          </Muted>
        ) : (
          <Muted size="sm">Email channels send through a mail server of your own: SES, Postmark, Mailgun, your company&apos;s, or any SMTP server.</Muted>
        )}
        {needsMailServer ? <Muted size="sm" c="red.4">An email channel cannot send until a mail server is set up.</Muted> : null}
        {saved.privateNetworks ? null : (
          <Muted size="xs">Mail servers and Slack addresses on private networks are refused unless the operator sets GEASS_WEBHOOKS_ALLOW_PRIVATE_NETWORKS=true.</Muted>
        )}
      </Stack>

      <Stack gap="xs">
        <Text fw={600} size="sm">Firing now</Text>
        {saved.firing.length ? (
          <div className="rw-rail-stack">
            {saved.firing.map((alert) => (
              <div className="rw-webhook-row" key={`${alert.kind}-${alert.resource}-${alert.project}-${alert.environment}-${alert.summary}`}>
                <div className="rw-webhook-main">
                  <div className="rw-webhook-url"><span>{alert.title}</span>{severityBadge(alert)}</div>
                  <div className="rw-webhook-meta">{whereIs(alert)} · started {formatRelativeTime(alert.since)}</div>
                  {alert.summary ? <div className="rw-webhook-meta">{alert.summary}</div> : null}
                </div>
              </div>
            ))}
          </div>
        ) : (
          <Muted size="sm" className="rw-settings-empty-inline">
            {saved.alertmanager.ready ? "Nothing is firing." : "Alertmanager cannot be reached, so what is firing is not known."}
          </Muted>
        )}
        <Muted size="xs">
          Adding the first channel announces what is already firing. A problem that is still there is mentioned again every twelve hours, and
          one that has ended is announced as resolved a few minutes after it does.
        </Muted>
      </Stack>

      <Modal
        opened={Boolean(editing)}
        onClose={() => { if (busy !== "channel") setEditing(null); }}
        title={editing ? `${editing.id ? "Edit" : "New"} ${editing.type === "slack" ? "Slack channel" : "email channel"}` : ""}
        centered
        size="lg"
      >
        {editing ? (
          <form onSubmit={saveChannel} className="rw-alert-form">
            <Stack gap="sm">
              <Field label="Name" description="What this channel is called in the dashboard.">
                <Input required autoFocus value={editing.name} maxLength={60} aria-label="Channel name" placeholder={editing.type === "slack" ? "#ops-alerts" : "On call"} onChange={field(setEditing, editing, "name")} />
              </Field>
              {editing.type === "slack" ? (
                <Field
                  label="Slack incoming webhook URL"
                  description={editing.id
                    ? "Leave blank to keep the saved one. It is a secret: anyone who has it can post to the channel."
                    : "Create one under Slack apps, Incoming Webhooks. It works with anything that takes Slack's webhook JSON, such as Mattermost. It is stored as a secret."}
                >
                  <Input
                    type="password"
                    required={!editing.id}
                    value={editing.url}
                    autoComplete="off"
                    aria-label="Slack webhook URL"
                    placeholder={editing.id ? `Saved for ${editing.target || "this channel"}` : "https://hooks.slack.com/services/…"}
                    onChange={field(setEditing, editing, "url")}
                  />
                </Field>
              ) : (
                <Field label="Send to" description="Email addresses, separated by commas, spaces or lines. Needs the mail server below.">
                  <Textarea required rows={3} value={editing.recipients} aria-label="Recipients" placeholder={"ops@example.com\noncall@example.com"} onChange={field(setEditing, editing, "recipients")} />
                </Field>
              )}
              <Field label="Alerts this channel hears">
                <Stack gap={6}>
                  {kinds.map((kind) => (
                    <Checkbox
                      key={kind.id}
                      label={kind.label}
                      description={kind.description}
                      checked={editing.events.includes(kind.id)}
                      onChange={(change) => setEditing({ ...editing, events: toggle(editing.events, kind.id, change.currentTarget.checked) })}
                    />
                  ))}
                </Stack>
              </Field>
              <Field label="Projects" description="Leave all unchecked for every project. Only a channel that is not limited to projects hears about the cluster itself: nodes, certificates and upgrades.">
                <Stack gap={6} className="rw-alert-checks">
                  {projects.length ? projects.map((project) => (
                    <Checkbox
                      key={resourceName(project)}
                      label={projectNames[resourceName(project)]}
                      checked={editing.projects.includes(resourceName(project))}
                      onChange={(change) => setEditing({ ...editing, projects: toggle(editing.projects, resourceName(project), change.currentTarget.checked) })}
                    />
                  )) : <Muted size="sm">There are no projects yet.</Muted>}
                </Stack>
              </Field>
              <Field label="Environments" description="Leave all unchecked for every environment. For example, check production only to keep staging out of a pager.">
                <Group gap="md">
                  {environmentNames.map((name) => (
                    <Checkbox
                      key={name}
                      label={name}
                      checked={editing.environments.includes(name)}
                      onChange={(change) => setEditing({ ...editing, environments: toggle(editing.environments, name, change.currentTarget.checked) })}
                    />
                  ))}
                </Group>
              </Field>
              <Switch
                label="Also tell me when a problem is resolved"
                checked={editing.notifyResolved}
                onChange={(event) => setEditing({ ...editing, notifyResolved: event.currentTarget.checked })}
              />
              {editing.id ? (
                <Switch label="Paused" checked={editing.disabled} onChange={(event) => setEditing({ ...editing, disabled: event.currentTarget.checked })} />
              ) : null}
              <Group justify="flex-end" gap="sm">
                <Button type="button" variant="outline" disabled={busy === "channel"} onClick={() => setEditing(null)}>Cancel</Button>
                <Button type="submit" loading={busy === "channel"}>{editing.id ? "Save" : "Add channel"}</Button>
              </Group>
            </Stack>
          </form>
        ) : null}
      </Modal>

      <Modal opened={Boolean(smtp)} onClose={() => { if (busy !== "smtp") setSMTP(null); }} title="Mail server" centered>
        {smtp ? (
          <form onSubmit={saveSMTP} className="rw-alert-form">
            <Stack gap="sm">
              <Group grow align="flex-start">
                <Field label="Server">
                  <Input required autoFocus value={smtp.host} aria-label="Mail server" placeholder="smtp.example.com" onChange={field(setSMTP, smtp, "host")} />
                </Field>
                <Field label="Port">
                  <Input required type="number" min={1} max={65535} value={smtp.port} aria-label="Port" onChange={field(setSMTP, smtp, "port")} />
                </Field>
              </Group>
              <Field label="Security" description="Alertmanager sends the mail. It connects encrypted from the first byte only on port 465, and it will not send a password over a connection that is not encrypted.">
                <Select value={smtp.security} aria-label="Security" onChange={chooseSecurity}>
                  {SECURITY.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}
                </Select>
              </Field>
              <Group grow align="flex-start">
                <Field label="Username" description="Leave it empty for a server that does not ask.">
                  <Input value={smtp.username} disabled={smtp.security === "none"} autoComplete="off" aria-label="Username" onChange={field(setSMTP, smtp, "username")} />
                </Field>
                <Field label="Password">
                  <Input
                    type="password"
                    value={smtp.password}
                    disabled={smtp.security === "none"}
                    autoComplete="new-password"
                    aria-label="Password"
                    placeholder={saved.smtp?.hasPassword ? "Saved. Leave blank to keep it." : ""}
                    onChange={field(setSMTP, smtp, "password")}
                  />
                </Field>
              </Group>
              <Field label="From" description="The address alerts come from, such as alerts@example.com or Geass <alerts@example.com>.">
                <Input required value={smtp.from} aria-label="From address" placeholder="Geass <alerts@example.com>" onChange={field(setSMTP, smtp, "from")} />
              </Field>
              <Group justify="flex-end" gap="sm">
                <Button type="button" variant="outline" disabled={busy === "smtp"} onClick={() => setSMTP(null)}>Cancel</Button>
                <Button type="submit" loading={busy === "smtp"}>Save</Button>
              </Group>
            </Stack>
          </form>
        ) : null}
      </Modal>

      <Modal opened={Boolean(removing)} onClose={() => { if (!busy) setRemoving(null); }} title="Delete channel?" centered>
        <Text size="sm" mb="md">
          Geass stops sending alerts to <strong>{removing?.name}</strong>
          {removing?.type === "slack" ? ", and forgets its webhook URL" : ""}. Alerts that already went out stay in Slack or in the inbox.
        </Text>
        <Group justify="flex-end" gap="sm">
          <Button variant="outline" disabled={Boolean(busy)} onClick={() => setRemoving(null)}>Cancel</Button>
          <Button variant="danger" loading={busy === "remove"} onClick={remove}>Delete channel</Button>
        </Group>
      </Modal>

      <Modal opened={removingSMTP} onClose={() => { if (!busy) setRemovingSMTP(false); }} title="Remove the mail server?" centered>
        <Text size="sm" mb="md">
          Email channels keep their addresses but cannot send until a mail server is set up again. Its password is deleted.
        </Text>
        <Group justify="flex-end" gap="sm">
          <Button variant="outline" disabled={Boolean(busy)} onClick={() => setRemovingSMTP(false)}>Cancel</Button>
          <Button variant="danger" loading={busy === "smtp-remove"} onClick={removeSMTP}>Remove</Button>
        </Group>
      </Modal>
    </div>
  );
}
