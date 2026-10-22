import { useState } from "react";
import { Group, Stack, Text } from "@mantine/core";
import { action } from "@/lib/api";
import { alertError, notify } from "@/lib/notify";
import { publicOrigin } from "@/lib/transport";
import { ssoErrorMessage } from "@/lib/sso";
import { Badge, Button, Field, Input, Muted, Textarea } from "@/components/geass-ui";

const asLines = (map) => Object.entries(map || {}).map(([key, value]) => `${key}=${value}`).join("\n");

/**
 * An organization's own identity provider, set up by the platform. The people of the organization sign in through it from
 * a link that names the organization; it maps one claim to a role, and cannot make anyone an administrator of the platform
 * or put them in another organization. It is tested, as someone who maps to admin, before it is turned on, and saving it
 * again turns it off. The client secret is kept in a Secret and never comes back.
 */
export function OrganizationIdP({ org, refetch }) {
  const idp = org.idp;
  const [form, setForm] = useState(null);
  const [busy, setBusy] = useState("");
  const base = `/orgs/${org.name}/idp`;
  const origin = publicOrigin();

  const run = async (key, path, values, done) => {
    setBusy(key);
    try {
      await action(path, values);
      await refetch();
      if (done) notify(done, "success");
      return true;
    } catch (error) {
      alertError(error.message);
      return false;
    } finally {
      setBusy("");
    }
  };
  const edit = () =>
    setForm({
      displayName: idp?.displayName || "",
      issuer: idp?.issuer || "",
      clientID: idp?.clientID || "",
      clientSecret: "",
      scopes: (idp?.scopes || []).join(" "),
      roleClaim: idp?.roleClaim || "",
      roleMap: asLines(idp?.roleMap) || "owner=admin\nmember=viewer",
      requireClaim: asLines(idp?.requireClaim),
    });
  const set = (field) => (event) => setForm({ ...form, [field]: event.currentTarget.value });
  const save = async (event) => {
    event.preventDefault();
    const keep = idp?.hasSecret && form.clientSecret === "";
    if (await run("save", `${base}/save`, { ...form, ...(keep ? { keepSecret: "on" } : {}) }, "Saved. Test the sign-in before turning it on.")) setForm(null);
  };

  const test = idp?.test;
  const loginURL = idp ? `${origin}${idp.loginPath}` : "";
  const callbackURL = idp ? `${origin}${idp.callbackPath}` : "";

  return (
    <Stack gap="xs">
      <Group gap="sm">
        <Text fw={600} size="sm">Sign-in provider of its own</Text>
        <Badge tone={idp?.enabled ? "success" : "neutral"}>{idp ? (idp.enabled ? "On" : "Saved, not on") : "None"}</Badge>
      </Group>
      <Muted size="sm">
        The organization&apos;s people can sign in through a provider of their own (their Okta, their Entra ID) instead of the platform&apos;s. They work in this
        organization, in all of it, with the role one claim of their token maps to, and nowhere else.
      </Muted>
      {idp ? (
        <Stack gap={4}>
          <Muted size="sm">{idp.displayName || "Provider"}: {idp.issuer} (client {idp.clientID}{idp.hasSecret ? ", with a saved secret" : ""})</Muted>
          <Muted size="xs">Tell the provider to send people back to: <code>{callbackURL}</code></Muted>
          <Muted size="xs">Where the organization&apos;s people sign in: <code>{loginURL}</code></Muted>
          {test ? (
            <Muted size="sm" c={test.ok ? undefined : "red.4"}>
              {test.ok ? `Test sign-in worked as ${test.email} (${test.role}).` : `Test sign-in failed: ${test.detail || ssoErrorMessage(test.reason)}`}
            </Muted>
          ) : idp.enabled ? null : (
            <Muted size="sm">Not tested yet.</Muted>
          )}
        </Stack>
      ) : null}
      {form ? (
        <form onSubmit={save}>
          <Stack gap="sm">
            <Field label="Name" description="What the sign-in calls the provider.">
              <Input value={form.displayName} aria-label="Provider name" placeholder="Acme SSO" onChange={set("displayName")} />
            </Field>
            <Field label="Issuer" description="The provider's issuer URL, as its discovery document names it. It must be https.">
              <Input required value={form.issuer} aria-label="Issuer" placeholder="https://acme.okta.com" onChange={set("issuer")} />
            </Field>
            <Group grow align="flex-start">
              <Field label="Client ID">
                <Input required value={form.clientID} aria-label="Client ID" onChange={set("clientID")} />
              </Field>
              <Field label="Client secret" description={idp?.hasSecret ? "Leave empty to keep the saved one." : "Leave empty for a public client."}>
                <Input type="password" value={form.clientSecret} autoComplete="new-password" aria-label="Client secret" onChange={set("clientSecret")} />
              </Field>
            </Group>
            <Field label="Role claim" description="The claim of the ID token that holds the person's role.">
              <Input required value={form.roleClaim} aria-label="Role claim" placeholder="role" onChange={set("roleClaim")} />
            </Field>
            <Field label="Role mapping" description="One per line as value=role, where the role is admin or viewer. A value that is not here is not let in.">
              <Textarea required rows={3} value={form.roleMap} aria-label="Role mapping" onChange={set("roleMap")} />
            </Field>
            <Field label="Only people with" description="Optional, one per line as claim=value.">
              <Textarea rows={2} value={form.requireClaim} aria-label="Required claims" onChange={set("requireClaim")} />
            </Field>
            <Field label="Scopes" description="Asked for besides openid. Empty means email and profile.">
              <Input value={form.scopes} aria-label="Scopes" onChange={set("scopes")} />
            </Field>
            <Group justify="flex-end" gap="sm">
              <Button type="button" variant="outline" disabled={Boolean(busy)} onClick={() => setForm(null)}>Cancel</Button>
              <Button type="submit" loading={busy === "save"}>Save</Button>
            </Group>
          </Stack>
        </form>
      ) : (
        <Group gap="sm">
          <Button variant="outline" disabled={Boolean(busy)} onClick={edit}>{idp ? "Change" : "Set up a provider"}</Button>
          {idp ? (
            <>
              <Button variant="outline" component="a" href={`${idp.loginPath}&purpose=test`}>Test sign-in</Button>
              <Button
                variant="outline"
                loading={busy === "enable"}
                disabled={Boolean(busy) || (!idp.enabled && !idp.canEnable)}
                onClick={() => run("enable", `${base}/enable`, { enabled: idp.enabled ? "off" : "on" }, idp.enabled ? "Turned off" : "Turned on")}
              >
                {idp.enabled ? "Turn off" : "Turn on"}
              </Button>
              <Button variant="outline" loading={busy === "clear"} disabled={Boolean(busy)} onClick={() => run("clear", `${base}/clear`, {}, "Removed")}>Remove</Button>
            </>
          ) : null}
        </Group>
      )}
    </Stack>
  );
}
