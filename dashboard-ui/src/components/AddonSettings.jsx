import { useState } from "react";
import { Group, Stack, Text } from "@mantine/core";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { action, api } from "@/lib/api";
import { alertError, notify } from "@/lib/notify";
import { Badge, Button, Field, Input, Muted, PageSection, Select } from "@/components/geass-ui";

export const ADDON_STATES = {
  Off: { label: "Off", tone: "neutral" },
  Installing: { label: "Installing", tone: "warning" },
  Ready: { label: "Ready", tone: "success" },
  Failed: { label: "Failed", tone: "danger" },
};

export const ADDON_COPY = {
  serverless: { what: "Stops a service when nobody uses it and starts it on the next request." },
  longhorn: {
    what: "Database volumes with copies on more than one node.",
    on: "Every node needs the iSCSI initiator.",
    stays: "Cannot be turned off once it is on.",
  },
  monitoring: { what: "Metrics for services, databases, and nodes.", off: "Removes Prometheus and its metrics history." },
  logging: { what: "Logs that outlast the pods that wrote them.", off: "Removes the log store and its logs." },
};

export function addonsAreMoving(settings) {
  if (!settings) return false;
  return Boolean(settings.retryPending) || (settings.addons || []).some((addon) => addon.state === "Installing");
}

export function AddonSettings({ mutable }) {
  const queryClient = useQueryClient();
  const query = useQuery({
    queryKey: ["addons"],
    queryFn: () => api("/api/settings/addons"),
    staleTime: 0,
    refetchOnMount: "always",
    refetchInterval: (state) => (addonsAreMoving(state.state.data) ? 4_000 : 30_000),
  });
  const [busy, setBusy] = useState("");
  const [confirming, setConfirming] = useState("");

  if (query.isLoading) return null;
  if (query.isError) {
    return (
      <PageSection title="Add-ons">
        <Muted size="sm">{query.error.message}</Muted>
      </PageSection>
    );
  }
  const { addons = [], retryPending = "" } = query.data;

  const run = async (key, path, values, done) => {
    setBusy(key);
    try {
      await action(path, values);
      await queryClient.invalidateQueries({ queryKey: ["addons"] });
      if (done) notify(done, "success");
      return true;
    } catch (error) {
      alertError(error.message);
      return false;
    } finally {
      setBusy("");
    }
  };

  const setEnabled = async (addon, enabled) => {
    const done = await run(`${addon.name}:enabled`, `/settings/addons/${addon.name}/save`, { enabled: String(enabled) }, enabled ? `${addon.title} is turning on` : `${addon.title} is off`);
    if (done) setConfirming("");
  };

  return (
    <Stack gap="lg">
      {addons.map((addon) => (
        <AddonCard
          key={addon.name}
          addon={addon}
          mutable={mutable}
          busy={busy}
          retryPending={retryPending}
          confirming={confirming === addon.name}
          onAsk={() => setConfirming(addon.name)}
          onCancel={() => setConfirming("")}
          onToggle={(enabled) => setEnabled(addon, enabled)}
          onRetry={() => run(`${addon.name}:retry`, `/settings/addons/${addon.name}/retry`, {}, `Retrying ${addon.title}`)}
          onOption={(values, done) => run(`${addon.name}:option`, `/settings/addons/${addon.name}/save`, values, done)}
        />
      ))}
    </Stack>
  );
}

function AddonCard({ addon, mutable, busy, retryPending, confirming, onAsk, onCancel, onToggle, onRetry, onOption }) {
  const state = ADDON_STATES[addon.state] || ADDON_STATES.Off;
  const copy = ADDON_COPY[addon.name] || {};
  const working = busy.startsWith(`${addon.name}:`);
  const asksFirst = Boolean(addon.enabled ? copy.off : copy.on);
  const stays = addon.enabled && !addon.canDisable;
  const warning = addon.enabled ? copy.off : copy.on;

  return (
    <div data-addon={addon.name}>
      <PageSection title={addon.title} actions={<Badge tone={state.tone}>{state.label}</Badge>}>
        <Stack gap="xs">
          <Muted size="sm">{copy.what}</Muted>
          {addon.message ? (
            <Text size="sm" c={addon.state === "Failed" ? "red" : "dimmed"} role={addon.state === "Failed" ? "alert" : undefined}>
              {addon.message}
            </Text>
          ) : null}

          {mutable ? (
            <>
              <Group gap="sm" mt="xs">
                {stays ? (
                  <Muted size="sm">{copy.stays}</Muted>
                ) : confirming ? (
                  <>
                    <Button variant={addon.enabled ? "danger" : "default"} disabled={working} onClick={() => onToggle(!addon.enabled)}>
                      {addon.enabled ? `Turn off ${addon.title}` : `Turn on ${addon.title}`}
                    </Button>
                    <Button variant="outline" onClick={onCancel}>Cancel</Button>
                  </>
                ) : addon.enabled ? (
                  <Button variant="outline" disabled={working} onClick={asksFirst ? onAsk : () => onToggle(false)}>Turn off</Button>
                ) : (
                  <Button disabled={working} onClick={asksFirst ? onAsk : () => onToggle(true)}>Turn on</Button>
                )}
                {addon.canRetry ? (
                  <Button variant="outline" disabled={working || Boolean(retryPending)} onClick={onRetry}>
                    {retryPending === addon.name ? "Retrying" : "Retry install"}
                  </Button>
                ) : null}
              </Group>
              {confirming && warning ? <Muted size="sm">{warning}</Muted> : null}
              {addon.enabled ? <AddonOptions addon={addon} busy={working} onOption={onOption} /> : null}
            </>
          ) : null}
        </Stack>
      </PageSection>
    </div>
  );
}

function AddonOptions({ addon, busy, onOption }) {
  const [profile, setProfile] = useState(addon.profile || "");
  const [days, setDays] = useState(String(addon.retentionDays || ""));
  if (addon.name === "monitoring") {
    const changed = profile && profile !== addon.profile;
    return (
      <Group align="flex-end" gap="sm" mt="xs">
        <Field label="Footprint">
          <Select value={profile} onChange={(event) => setProfile(event.currentTarget.value)} aria-label="Footprint">
            {(addon.profiles || []).map((item) => <option key={item} value={item}>{item}</option>)}
          </Select>
        </Field>
        <Button variant="outline" disabled={busy || !changed} onClick={() => onOption({ profile }, "Footprint saved")}>Save</Button>
      </Group>
    );
  }
  if (addon.name === "logging") {
    const changed = days !== "" && Number(days) !== addon.retentionDays;
    return (
      <Group align="flex-end" gap="sm" mt="xs">
        <Field label="Keep logs for (days)">
          <Input type="number" min={1} max={90} value={days} onChange={(event) => setDays(event.currentTarget.value)} aria-label="Keep logs for (days)" />
        </Field>
        <Button variant="outline" disabled={busy || !changed} onClick={() => onOption({ retentionDays: days }, "Retention saved")}>Save</Button>
      </Group>
    );
  }
  return null;
}
