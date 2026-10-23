import { useEffect, useState } from "react";
import { Code, Group, Modal, Stack, Text } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import { action, api } from "@/lib/api";
import { alertError, notify } from "@/lib/notify";
import { ssoErrorMessage } from "@/lib/sso";
import { Badge, Button, Field, Input, Muted, Select, Switch, Textarea } from "@/components/geass-ui";
import { SettingsFormActions } from "@/layout/SettingsShell";

const emptyForm = {
  displayName: "",
  issuer: "",
  clientID: "",
  clientSecret: "",
  scopes: "",
  roleClaim: "",
  orgsClaim: "",
  roleMap: "",
  requireClaim: "",
  sessionMinutes: "60",
  refreshMinutes: "",
  staleGraceMinutes: "",
};

const asLines = (map) => Object.entries(map || {}).map(([key, value]) => `${key}=${value}`).join("\n");

/**
 * Who decides who signs in: the users in a Secret (the default), or an identity provider that speaks OpenID Connect,
 * which owns the people, invitations, organizations and teams. The client secret is kept in a Secret and never comes
 * back to the browser. Turning the provider on needs a successful test sign-in first, so a typo cannot lock everyone out.
 */
export function AuthSettings({ mutable }) {
  const settings = useQuery({
    queryKey: ["auth-settings"],
    queryFn: () => api("/api/settings/auth"),
    enabled: mutable,
    staleTime: 0,
    refetchOnMount: "always",
  });
  const saved = settings.data;
  const [form, setForm] = useState(emptyForm);
  const [busy, setBusy] = useState("");
  const [confirming, setConfirming] = useState("");

  // The form is seeded from what is saved, and again only when what it shows has changed (after a save), not on every
  // refetch: flipping the logout-policy switch changes the saved data too, and must not wipe what is being typed.
  const formSource = saved
    ? JSON.stringify([saved.displayName, saved.issuer, saved.clientID, saved.scopes, saved.roleClaim, saved.tenancy?.orgsClaim, saved.roleMap, saved.requireClaim, saved.sessionMinutes, saved.refreshMinutes, saved.staleGraceMinutes])
    : "";
  useEffect(() => {
    if (!saved) return;
    setForm({
      displayName: saved.displayName || "",
      issuer: saved.issuer || "",
      clientID: saved.clientID || "",
      clientSecret: "",
      scopes: (saved.scopes || []).join(" "),
      roleClaim: saved.roleClaim || "",
      orgsClaim: saved.tenancy?.orgsClaim || "",
      roleMap: asLines(saved.roleMap),
      requireClaim: asLines(saved.requireClaim),
      sessionMinutes: String(saved.sessionMinutes || 60),
      refreshMinutes: saved.refreshMinutes ? String(saved.refreshMinutes) : "",
      staleGraceMinutes: saved.refreshMinutes && saved.staleGraceMinutes ? String(saved.staleGraceMinutes) : "",
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [formSource]);

  // Coming back from the provider after a test: say what happened, and tidy the address.
  useEffect(() => {
    const query = new URLSearchParams(window.location.search);
    const failure = query.get("sso_error");
    if (!failure && !query.get("sso_test")) return;
    if (failure) alertError(ssoErrorMessage(failure));
    window.history.replaceState(null, "", window.location.pathname);
    settings.refetch();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  if (!mutable) {
    return <Muted size="sm" className="rw-settings-empty-inline">Only admins can change how people sign in.</Muted>;
  }
  if (settings.isLoading) {
    return <Muted size="sm" className="rw-settings-empty-inline">Loading sign-in settings…</Muted>;
  }
  if (settings.isError) {
    return <Muted size="sm" className="rw-settings-empty-inline">{settings.error.message}</Muted>;
  }

  const external = saved.mode === "external";
  const name = saved.displayName || "the identity provider";
  const set = (field) => (event) => setForm({ ...form, [field]: event.currentTarget.value });
  const keepSecret = Boolean(saved.hasSecret) && form.clientSecret === "";

  const run = async (key, path, values, done) => {
    setBusy(key);
    try {
      await action(path, values);
      await settings.refetch();
      setConfirming("");
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
    const stored = await run("save", "/settings/auth/save", { ...form, keepSecret: keepSecret ? "on" : undefined }, "Saved");
    // A secret that was typed has been stored; it is not kept in the field.
    if (stored) setForm((current) => ({ ...current, clientSecret: "" }));
  };

  const startTest = () => {
    window.location.assign("/auth/oidc/login?purpose=test");
  };

  const test = saved.test;
  // Without a required claim, anyone the provider signs in with a role that is mapped gets in. That is right for a provider
  // whose people are chosen (a company directory) and wrong for one anyone can sign up to.
  const unrestricted = saved.configured && Object.keys(saved.requireClaim || {}).length === 0 && !saved.tenancy?.orgsClaim;

  return (
    <div className="rw-tokens">
      <Group gap="sm" mb="md">
        <Text fw={600} size="sm">Signing in now</Text>
        <Badge tone={external ? "success" : "neutral"}>{external ? `Through ${name}` : "With passwords from the Secret"}</Badge>
      </Group>

      {external ? (
        <Stack gap="xs" mb="md">
          <Muted size="sm">Switch back to passwords to change these settings.</Muted>
          <div>
            <Button type="button" variant="outline" loading={busy === "mode"} disabled={Boolean(busy)} onClick={() => run("mode", "/settings/auth/mode", { mode: "local" }, "Switched to passwords")}>
              Switch back to passwords
            </Button>
          </div>
        </Stack>
      ) : null}

      <form onSubmit={save}>
        <Stack gap="sm">
          <Field label="Name">
            <Input value={form.displayName} disabled={external} aria-label="Provider name" placeholder="Acme" onChange={set("displayName")} />
          </Field>
          <Field label="Issuer URL" description="https, exactly as the provider names itself. For Better Auth, include /api/auth.">
            <Input required value={form.issuer} disabled={external} aria-label="Issuer URL" placeholder="https://auth.example.com/api/auth" onChange={set("issuer")} />
          </Field>
          <Group grow align="flex-start">
            <Field label="Client ID">
              <Input required value={form.clientID} disabled={external} aria-label="Client ID" autoComplete="off" onChange={set("clientID")} />
            </Field>
            <Field label="Client secret" description="Leave empty for a public client.">
              <Input
                type="password"
                value={form.clientSecret}
                disabled={external}
                aria-label="Client secret"
                autoComplete="new-password"
                placeholder={saved.hasSecret ? "Saved. Leave blank to keep it." : ""}
                onChange={set("clientSecret")}
              />
            </Field>
          </Group>
          <Field label="Role claim" description="A dotted path reaches nested claims. Not needed when the organizations claim is set.">
            <Input required={!form.orgsClaim.trim()} value={form.roleClaim} disabled={external} aria-label="Role claim" placeholder="org_role" onChange={set("roleClaim")} />
          </Field>
          <Field label="Organizations claim" description="Optional. Leave empty for a single organization.">
            <Input value={form.orgsClaim} disabled={external || Boolean(saved.tenancy?.enabled)} aria-label="Organizations claim saved with the provider" placeholder="orgs" onChange={set("orgsClaim")} />
          </Field>
          <Field label="Roles" description="One per line, as role=admin or role=viewer. Other roles cannot sign in.">
            <Textarea required rows={3} value={form.roleMap} disabled={external} aria-label="Role mapping" placeholder={"owner=admin\nadmin=admin\nmember=viewer"} onChange={set("roleMap")} />
          </Field>
          <Field label="Only people with" description="Optional. One per line, as claim=value.">
            <Textarea rows={2} value={form.requireClaim} disabled={external} aria-label="Required claims" placeholder="organization_id=org_123" onChange={set("requireClaim")} />
          </Field>
          <Group grow align="flex-start">
            <Field label="Scopes" description="Besides openid. Empty means email and profile.">
              <Input value={form.scopes} disabled={external} aria-label="Scopes" placeholder="email profile" onChange={set("scopes")} />
            </Field>
            <Field label="Session length (minutes)">
              <Input type="number" min={5} max={720} value={form.sessionMinutes} disabled={external} aria-label="Session length in minutes" onChange={set("sessionMinutes")} />
            </Field>
          </Group>
          <Group grow align="flex-start">
            <Field label="Check people again every (minutes)" description="Optional. Needs refresh tokens (offline_access). Empty means never.">
              <Input type="number" min={5} max={1440} value={form.refreshMinutes} disabled={external} aria-label="Check people again every" placeholder="Never" onChange={set("refreshMinutes")} />
            </Field>
            <Field label="Keep a session when the provider is down for (minutes)" description="Empty means 120.">
              <Input type="number" min={5} max={1440} value={form.staleGraceMinutes} disabled={external || !form.refreshMinutes} aria-label="Keep a session when the provider is down for" onChange={set("staleGraceMinutes")} />
            </Field>
          </Group>
          {unrestricted && !external ? (
            <Muted size="sm">
              Nothing restricts who gets in. If anyone can sign up at the provider, add a claim under "Only people with".
            </Muted>
          ) : null}
          {!external ? (
            <SettingsFormActions>
              <Button type="submit" loading={busy === "save"} disabled={Boolean(busy)}>Save</Button>
              {saved.configured ? (
                <Button type="button" variant="outline" disabled={Boolean(busy)} onClick={() => setConfirming("clear")}>Remove</Button>
              ) : null}
            </SettingsFormActions>
          ) : null}
        </Stack>
      </form>

      <Stack gap="xs" mt="lg">
        <Text fw={600} size="sm">Register Geass with the provider</Text>
        <Field label="Redirect URI"><Code block aria-label="Redirect URI">{saved.callbackURL}</Code></Field>
        <Field label="Back-channel logout URI" description="Optional. Must be an https address the provider can reach.">
          <Code block aria-label="Back-channel logout URI">{saved.backchannelLogoutURL}</Code>
        </Field>
        <Switch
          checked={saved.revokeTokensOnLogout !== false}
          disabled={!saved.configured || Boolean(busy)}
          label="Also revoke the tokens and deploy hooks they made"
          description="A CI token made from the account of someone who signs out stops working."
          onChange={(event) => run("policy", "/settings/auth/logout-policy", { revokeTokens: event.currentTarget.checked ? "on" : "off" }, "Saved")}
        />
      </Stack>

      <Stack gap="xs" mt="lg">
        <Text fw={600} size="sm">Test, then turn on</Text>
        <Muted size="sm">Turning the provider on needs a test from the last half hour.</Muted>
        {test ? (
          <Stack gap={4}>
            <Group gap="sm"><Badge tone={test.ok ? "success" : "danger"}>{test.ok ? "Test passed" : "Test failed"}</Badge>
              {test.email ? <Text size="sm">{test.email}{test.role ? ` · ${test.role}` : ""}</Text> : null}
            </Group>
            {!test.ok && test.detail ? <Muted size="sm">{test.detail}</Muted> : null}
            {test.platformAdmin ? <Muted size="sm">The token makes this person a platform administrator.</Muted> : null}
            {(test.orgs || []).length ? (
              <Stack gap={2}>
                {test.orgs.map((org) => (
                  <Text size="sm" key={org.idpID}>
                    {org.idpID} · {org.role} · {org.workspaces} {org.workspaces === 1 ? "workspace" : "workspaces"} ·{" "}
                    {org.name ? (org.suspended ? "set up, suspended" : "set up here") : "not set up here"}
                  </Text>
                ))}
              </Stack>
            ) : null}
          </Stack>
        ) : (
          <Muted size="sm">{saved.configured ? "Not tested yet." : "Save the settings first."}</Muted>
        )}
        <Group gap="sm">
          <Button type="button" variant="outline" disabled={!saved.configured || Boolean(busy)} onClick={startTest}>Test sign-in</Button>
          {!external ? (
            <Button type="button" disabled={!saved.canEnable || Boolean(busy)} onClick={() => setConfirming("enable")}>Use {name} to sign in</Button>
          ) : null}
        </Group>
        {!external && saved.configured && !saved.hasPasswordAdmin ? (
          <Muted size="sm">Keep an admin who signs in with a password: it is the way back in if the provider is down.</Muted>
        ) : null}
      </Stack>

      <TenancySettings saved={saved} busy={busy} run={run} />

      <Modal opened={confirming === "enable"} onClose={() => { if (!busy) setConfirming(""); }} title={`Sign in through ${name}?`} centered>
        <Text size="sm" mb="md">
          Users who are not admins and have only a password can no longer sign in, and their sessions end.
        </Text>
        {unrestricted ? (
          <Text size="sm" mb="md" c="red.4">
            Nothing restricts who gets in. If anyone can sign up at {name}, they get the role it gives them. Add a claim under "Only people with" first.
          </Text>
        ) : null}
        <Group justify="flex-end" gap="sm">
          <Button variant="outline" disabled={Boolean(busy)} onClick={() => setConfirming("")}>Cancel</Button>
          <Button loading={busy === "mode"} onClick={() => run("mode", "/settings/auth/mode", { mode: "external" }, "Turned on")}>Turn on</Button>
        </Group>
      </Modal>

      <Modal opened={confirming === "clear"} onClose={() => { if (!busy) setConfirming(""); }} title="Remove the identity provider?" centered>
        <Text size="sm" mb="md">
          Geass forgets the provider and its client secret, and people sign in with passwords again.
        </Text>
        <Group justify="flex-end" gap="sm">
          <Button variant="outline" disabled={Boolean(busy)} onClick={() => setConfirming("")}>Cancel</Button>
          <Button variant="danger" loading={busy === "clear"} onClick={() => run("clear", "/settings/auth/clear", {}, "Identity provider removed")}>Remove</Button>
        </Group>
      </Modal>
    </div>
  );
}

/**
 * Organizations: how the sign-in token lists a person's organizations, and whether people belong to them. The claims are saved
 * on their own and can only change while organizations are off; turning them on needs a test sign-in that put someone in an
 * organization that is set up, so that a wrong claim cannot send everyone nowhere.
 */
function TenancySettings({ saved, busy, run }) {
  const tenancy = saved.tenancy || {};
  const [form, setForm] = useState({ orgsClaim: "", platformClaim: "", platformValues: "" });
  const [auto, setAuto] = useState({ mode: "off", allow: "", max: "" });
  const source = JSON.stringify([tenancy.orgsClaim, tenancy.platformClaim, tenancy.platformValues]);
  const autoSource = JSON.stringify([tenancy.autoProvision, tenancy.autoProvisionAllow, tenancy.autoProvisionMax]);
  useEffect(() => {
    setAuto({ mode: tenancy.autoProvision || "off", allow: (tenancy.autoProvisionAllow || []).join(" "), max: String(tenancy.autoProvisionMax || "") });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [autoSource]);
  useEffect(() => {
    setForm({ orgsClaim: tenancy.orgsClaim || "", platformClaim: tenancy.platformClaim || "", platformValues: (tenancy.platformValues || []).join(" ") });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [source]);
  if (!saved.configured) return null;
  const on = Boolean(tenancy.enabled);
  const set = (field) => (event) => setForm({ ...form, [field]: event.currentTarget.value });
  const save = (event) => {
    event.preventDefault();
    return run("tenancy", "/settings/auth/tenancy", form, "Saved");
  };
  const saveAuto = (event) => {
    event.preventDefault();
    return run("tenancy-auto", "/settings/auth/tenancy", { autoProvision: auto.mode, autoProvisionAllow: auto.allow, autoProvisionMax: auto.max }, "Saved");
  };

  return (
    <Stack gap="xs" mt="lg">
      <Group gap="sm">
        <Text fw={600} size="sm">Organizations</Text>
        <Badge tone={on ? "success" : "neutral"}>{on ? "On" : "Off"}</Badge>
      </Group>
      <Muted size="sm">Everyone signs in again when organizations are turned on or off.</Muted>
      <form onSubmit={save}>
        <Stack gap="sm">
          <Field label="Organizations claim" description="A list of {id, role, workspaces}, one per organization. A dotted path reaches nested claims.">
            <Input value={form.orgsClaim} disabled={on} aria-label="Organizations claim" placeholder="orgs" onChange={set("orgsClaim")} />
          </Field>
          <Group grow align="flex-start">
            <Field label="Platform administrator claim" description="Optional.">
              <Input value={form.platformClaim} disabled={on} aria-label="Platform administrator claim" placeholder="geass_platform" onChange={set("platformClaim")} />
            </Field>
            <Field label="Values" description="Separate with spaces.">
              <Input value={form.platformValues} disabled={on} aria-label="Platform administrator values" placeholder="operator" onChange={set("platformValues")} />
            </Field>
          </Group>
          {!on ? (
            <SettingsFormActions>
              <Button type="submit" variant="outline" loading={busy === "tenancy"} disabled={Boolean(busy)}>Save how the token names them</Button>
            </SettingsFormActions>
          ) : null}
        </Stack>
      </form>
      <Group gap="sm">
        {on ? (
          <Button type="button" variant="outline" loading={busy === "tenancy-off"} disabled={Boolean(busy)} onClick={() => run("tenancy-off", "/settings/auth/tenancy", { enabled: "off" }, "Organizations turned off")}>
            Turn organizations off
          </Button>
        ) : (
          <Button type="button" loading={busy === "tenancy-on"} disabled={Boolean(busy) || Boolean(tenancy.enableProblem)} onClick={() => run("tenancy-on", "/settings/auth/tenancy", { enabled: "on" }, "Organizations turned on")}>
            Turn organizations on
          </Button>
        )}
      </Group>
      {!on && tenancy.enableProblem ? <Muted size="sm">{tenancy.enableProblem}</Muted> : null}
      <form onSubmit={saveAuto}>
        <Stack gap="sm" mt="md">
          <Text fw={600} size="sm">Organizations made by sign-in</Text>
          <Muted size="sm">Use only with a provider where people you trust make organizations.</Muted>
          <Group grow align="flex-start">
            <Field label="Make organizations">
              <Select value={auto.mode} aria-label="Make organizations" onChange={(event) => setAuto({ ...auto, mode: event.currentTarget.value })}>
                <option value="off">Never</option>
                <option value="pending">Pending approval</option>
                <option value="open">At once, for the ids listed</option>
              </Select>
            </Field>
            <Field label="Most to make" description="Empty means 20.">
              <Input type="number" min="1" max="500" value={auto.max} aria-label="Most organizations to make" disabled={auto.mode === "off"} onChange={(event) => setAuto({ ...auto, max: event.currentTarget.value })} />
            </Field>
          </Group>
          <Field label="Only these ids" description="Separate with spaces. End an id with * to match a prefix. Open needs it.">
            <Input value={auto.allow} aria-label="Ids that may be made" disabled={auto.mode === "off"} placeholder="org_partner_*" onChange={(event) => setAuto({ ...auto, allow: event.currentTarget.value })} />
          </Field>
          <SettingsFormActions>
            <Button type="submit" variant="outline" loading={busy === "tenancy-auto"} disabled={Boolean(busy)}>Save</Button>
          </SettingsFormActions>
        </Stack>
      </form>
      {!on && !tenancy.enableProblem && tenancy.orgsClaim ? (
        <Muted size="sm">Projects without an organization will be seen by platform administrators only.</Muted>
      ) : null}
    </Stack>
  );
}
