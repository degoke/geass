import { useEffect, useState } from "react";
import { Code, Group, Modal, Stack, Text } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import { action, api } from "@/lib/api";
import { alertError, notify } from "@/lib/notify";
import { ssoErrorMessage } from "@/lib/sso";
import { Badge, Button, Field, Input, Muted, Switch, Textarea } from "@/components/geass-ui";
import { SettingsFormActions } from "@/layout/SettingsShell";

const emptyForm = {
  displayName: "",
  issuer: "",
  clientID: "",
  clientSecret: "",
  scopes: "",
  roleClaim: "",
  roleMap: "",
  requireClaim: "",
  sessionMinutes: "60",
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
    ? JSON.stringify([saved.displayName, saved.issuer, saved.clientID, saved.scopes, saved.roleClaim, saved.roleMap, saved.requireClaim, saved.sessionMinutes])
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
      roleMap: asLines(saved.roleMap),
      requireClaim: asLines(saved.requireClaim),
      sessionMinutes: String(saved.sessionMinutes || 60),
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
    const stored = await run("save", "/settings/auth/save", { ...form, keepSecret: keepSecret ? "on" : undefined }, "Identity provider saved. Test it before turning it on.");
    // A secret that was typed has been stored; it is not kept in the field.
    if (stored) setForm((current) => ({ ...current, clientSecret: "" }));
  };

  const startTest = () => {
    window.location.assign("/auth/oidc/login?purpose=test");
  };

  const test = saved.test;
  // Without a required claim, anyone the provider signs in with a role that is mapped gets in. That is right for a provider
  // whose people are chosen (a company directory) and wrong for one anyone can sign up to.
  const unrestricted = saved.configured && Object.keys(saved.requireClaim || {}).length === 0;

  return (
    <div className="rw-tokens">
      <p className="rw-rail-body">
        By default the people who can sign in are the users in a Secret, with a password each. To invite people by email, sign them in with
        a code or a link, and keep organizations and teams, connect an identity provider that speaks OpenID Connect (Better Auth, Keycloak,
        Authentik, Zitadel, Auth0, Okta and others). It owns the people; Geass only reads their role from the token it signs.
      </p>

      <Group gap="sm" mb="md">
        <Text fw={600} size="sm">Signing in now</Text>
        <Badge tone={external ? "success" : "neutral"}>{external ? `Through ${name}` : "With passwords from the Secret"}</Badge>
      </Group>

      {external ? (
        <Stack gap="xs" mb="md">
          <Muted size="sm">
            People are signing in through {name}, with the role it gives them (an admin or a viewer). A user in the Secret who has a password and is
            an admin can still sign in, in case the provider is down. To change the provider's settings, switch back to passwords first.
          </Muted>
          <div>
            <Button type="button" variant="outline" loading={busy === "mode"} disabled={Boolean(busy)} onClick={() => run("mode", "/settings/auth/mode", { mode: "local" }, "People sign in with passwords again.")}>
              Switch back to passwords
            </Button>
          </div>
        </Stack>
      ) : null}

      <form onSubmit={save}>
        <Stack gap="sm">
          <Field label="Name" description="What the sign-in button calls it: Sign in with …">
            <Input value={form.displayName} disabled={external} aria-label="Provider name" placeholder="Acme" onChange={set("displayName")} />
          </Field>
          <Field label="Issuer URL" description="https, exactly as the provider's discovery document names itself. For Better Auth that includes /api/auth.">
            <Input required value={form.issuer} disabled={external} aria-label="Issuer URL" placeholder="https://auth.example.com/api/auth" onChange={set("issuer")} />
          </Field>
          <Group grow align="flex-start">
            <Field label="Client ID">
              <Input required value={form.clientID} disabled={external} aria-label="Client ID" autoComplete="off" onChange={set("clientID")} />
            </Field>
            <Field label="Client secret" description="Leave it empty for a public client that uses PKCE alone.">
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
          <Field label="Role claim" description="The claim of the ID token that holds the person's role, such as org_role. A dotted path reaches into nested claims.">
            <Input required value={form.roleClaim} disabled={external} aria-label="Role claim" placeholder="org_role" onChange={set("roleClaim")} />
          </Field>
          <Field label="Roles" description="One per line: the provider's role, then =admin or =viewer. Someone whose roles are none of these cannot sign in.">
            <Textarea required rows={3} value={form.roleMap} disabled={external} aria-label="Role mapping" placeholder={"owner=admin\nadmin=admin\nmember=viewer"} onChange={set("roleMap")} />
          </Field>
          <Field label="Only people with" description="Optional, one per line as claim=value. For example organization_id=org_123 lets in only the members of that organization.">
            <Textarea rows={2} value={form.requireClaim} disabled={external} aria-label="Required claims" placeholder="organization_id=org_123" onChange={set("requireClaim")} />
          </Field>
          <Group grow align="flex-start">
            <Field label="Scopes" description="Asked for besides openid. Empty means email and profile.">
              <Input value={form.scopes} disabled={external} aria-label="Scopes" placeholder="email profile" onChange={set("scopes")} />
            </Field>
            <Field label="Session length (minutes)" description="Then the person goes back to the provider, which is how a changed role or a removed person is noticed.">
              <Input type="number" min={5} max={720} value={form.sessionMinutes} disabled={external} aria-label="Session length in minutes" onChange={set("sessionMinutes")} />
            </Field>
          </Group>
          {unrestricted && !external ? (
            <Muted size="sm">
              Nothing restricts who gets in: anyone the provider signs in with one of these roles becomes that role here. If anyone can sign up at
              the provider, or create their own organization there, add a claim under "Only people with" (for example organization_slug=acme).
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
        <Muted size="sm">Add these when you create the client there. The provider only sends people back to an address it was given.</Muted>
        <Field label="Redirect URI"><Code block aria-label="Redirect URI">{saved.callbackURL}</Code></Field>
        <Field label="Back-channel logout URI" description="Optional. The provider calls it when someone signs out or is removed, and Geass ends their sessions and, with the switch below on, revokes the tokens they made. Providers need it to be an https address they can reach.">
          <Code block aria-label="Back-channel logout URI">{saved.backchannelLogoutURL}</Code>
        </Field>
        <Switch
          checked={saved.revokeTokensOnLogout !== false}
          disabled={!saved.configured || Boolean(busy)}
          label="Also revoke the tokens and deploy hooks they made"
          description="When someone signs out at the provider or is removed there, their sessions here always end. With this on, so do the API tokens and deploy hooks they made. The provider cannot say which of the two happened, so a CI token made from the account of someone who signs out stops working: make those from an account that does not, or turn this off and revoke tokens by hand when someone leaves."
          onChange={(event) => run("policy", "/settings/auth/logout-policy", { revokeTokens: event.currentTarget.checked ? "on" : "off" }, "Saved")}
        />
      </Stack>

      <Stack gap="xs" mt="lg">
        <Text fw={600} size="sm">Test, then turn on</Text>
        <Muted size="sm">
          The test signs you in through the provider, as someone who would be an admin here, and shows what it found. It does not sign anyone in, and
          it is of the settings as they are saved. Turning the provider on needs a test from the last half hour.
        </Muted>
        {test ? (
          <Stack gap={4}>
            <Group gap="sm"><Badge tone={test.ok ? "success" : "danger"}>{test.ok ? "Test passed" : "Test failed"}</Badge>
              {test.email ? <Text size="sm">{test.email}{test.role ? ` · ${test.role}` : ""}</Text> : null}
            </Group>
            {!test.ok && test.detail ? <Muted size="sm">{test.detail}</Muted> : null}
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
          <Muted size="sm">Keep an admin who signs in with a password first: it is the way back in when the provider is down.</Muted>
        ) : null}
      </Stack>

      <Modal opened={confirming === "enable"} onClose={() => { if (!busy) setConfirming(""); }} title={`Sign in through ${name}?`} centered>
        <Text size="sm" mb="md">
          From now on people sign in through {name}, with the role it gives them. Users who are not admins and have only a password can no longer sign in,
          and their sessions end. An admin with a password still can. You can switch back at any time.
        </Text>
        {unrestricted ? (
          <Text size="sm" mb="md" c="red.4">
            Nothing restricts who gets in. If anyone can sign up at {name}, anyone who does becomes whichever role it gives them. Add a claim under "Only people with" first.
          </Text>
        ) : null}
        <Group justify="flex-end" gap="sm">
          <Button variant="outline" disabled={Boolean(busy)} onClick={() => setConfirming("")}>Cancel</Button>
          <Button loading={busy === "mode"} onClick={() => run("mode", "/settings/auth/mode", { mode: "external" }, `People now sign in through ${name}.`)}>Turn on</Button>
        </Group>
      </Modal>

      <Modal opened={confirming === "clear"} onClose={() => { if (!busy) setConfirming(""); }} title="Remove the identity provider?" centered>
        <Text size="sm" mb="md">
          Geass forgets the provider and its client secret, and people sign in with passwords from the Secret again. Nothing changes at the provider.
        </Text>
        <Group justify="flex-end" gap="sm">
          <Button variant="outline" disabled={Boolean(busy)} onClick={() => setConfirming("")}>Cancel</Button>
          <Button variant="danger" loading={busy === "clear"} onClick={() => run("clear", "/settings/auth/clear", {}, "Identity provider removed")}>Remove</Button>
        </Group>
      </Modal>
    </div>
  );
}
