import { useState } from "react";
import { Group, Stack, Text } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import { action, api } from "@/lib/api";
import { alertError, notify } from "@/lib/notify";
import { Badge, Button, Field, Input, Muted } from "@/components/geass-ui";

/**
 * Where an organization's builds push their images. Without a registry of its own, its builds use the platform's, with the
 * platform's login in their namespace; with one, they push there with a login that is the organization's and is kept in a
 * Secret, never sent back to the browser.
 */
export function OrgRegistrySettings({ mutable }) {
  const registry = useQuery({ queryKey: ["org-registry"], queryFn: () => api("/api/registry"), enabled: mutable, staleTime: 0, refetchOnMount: "always" });
  const [form, setForm] = useState(null);
  const [busy, setBusy] = useState("");

  if (!mutable) return <Muted size="sm" className="rw-settings-empty-inline">Only organization administrators can manage the registry.</Muted>;
  if (registry.isLoading) return <Muted size="sm" className="rw-settings-empty-inline">Loading…</Muted>;
  if (registry.isError) return <Muted size="sm" className="rw-settings-empty-inline">{registry.error.message}</Muted>;
  const saved = registry.data;

  const run = async (key, path, values, done) => {
    setBusy(key);
    try {
      await action(path, values);
      await registry.refetch();
      notify(done, "success");
      return true;
    } catch (error) {
      alertError(error.message);
      return false;
    } finally {
      setBusy("");
    }
  };
  const edit = () => setForm({ repository: saved.repository || "", username: "", password: "" });
  const save = async (event) => {
    event.preventDefault();
    const keep = saved.hasLogin && form.username === "" && form.password === "";
    if (await run("save", "/registry/save", { repository: form.repository.trim(), username: form.username.trim(), password: form.password, ...(keep ? { keepExisting: "on" } : {}) }, "Registry saved")) setForm(null);
  };

  return (
    <div className="rw-webhooks">
      <Group gap="sm">
        <Text fw={600} size="sm">Registry</Text>
        <Badge tone={saved.configured ? "success" : "neutral"}>{saved.configured ? "Your own" : saved.platformRepository ? "The platform's" : "None"}</Badge>
      </Group>
      {saved.configured ? <Muted size="sm">Images are pushed to {saved.repository}{saved.hasLogin ? ", with a saved login." : ", without a login."}</Muted> : null}
      {!saved.configured && !saved.platformRepository ? <Muted size="sm">No registry set, so builds do not run.</Muted> : null}
      {form ? (
        <form onSubmit={save}>
          <Stack gap="sm">
            <Field label="Repository" description="Must be reachable from the internet.">
              <Input required autoFocus value={form.repository} aria-label="Repository" placeholder="ghcr.io/acme/builds" onChange={(event) => setForm({ ...form, repository: event.currentTarget.value })} />
            </Field>
            <Group grow align="flex-start">
              <Field label="Username">
                <Input value={form.username} autoComplete="off" aria-label="Username" onChange={(event) => setForm({ ...form, username: event.currentTarget.value })} />
              </Field>
              <Field label="Password or token" description={saved.hasLogin ? "Leave both empty to keep the saved login." : "Leave both empty for a registry that needs no login."}>
                <Input type="password" value={form.password} autoComplete="new-password" aria-label="Password" onChange={(event) => setForm({ ...form, password: event.currentTarget.value })} />
              </Field>
            </Group>
            <Group justify="flex-end" gap="sm">
              <Button type="button" variant="outline" disabled={Boolean(busy)} onClick={() => setForm(null)}>Cancel</Button>
              <Button type="submit" loading={busy === "save"}>Save</Button>
            </Group>
          </Stack>
        </form>
      ) : (
        <Group gap="sm">
          <Button onClick={edit}>{saved.configured ? "Change registry" : "Set a registry"}</Button>
          {saved.configured ? <Button variant="outline" loading={busy === "clear"} disabled={Boolean(busy)} onClick={() => run("clear", "/registry/clear", {}, "Registry removed")}>Use the platform&apos;s</Button> : null}
        </Group>
      )}
    </div>
  );
}
