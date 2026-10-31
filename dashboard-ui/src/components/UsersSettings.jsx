import { useState } from "react";
import { Group, Menu, Modal, Stack, Text } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import { Copy, MoreVertical, Plus, User } from "lucide-react";
import { action, api } from "@/lib/api";
import { alertError, notify } from "@/lib/notify";
import { Badge, Button, Field, Input, Muted, Select } from "@/components/geass-ui";
import { ManagedArea } from "@/components/ManagedNotice";

const ROLE_LABELS = { admin: "Admin", viewer: "Viewer" };

/**
 * The people who sign in with a password: the users in the Secret geass-dashboard-auth, changed here instead of with kubectl.
 * A password that Geass makes up is shown once, in a dialog, and is not kept anywhere the page can read it again. The server
 * refuses to remove or demote the last admin, and says why.
 *
 * `managed` is set when the platform that runs this Geass keeps sign-in for itself (see lib/managed.js).
 */
export function UsersSettings({ mutable, managed = null }) {
  const users = useQuery({
    queryKey: ["dashboard-users"],
    queryFn: () => api("/api/users"),
    enabled: mutable,
    staleTime: 0,
    refetchOnMount: "always",
  });
  const [adding, setAdding] = useState(null); // null | { username, role, password }
  const [resetting, setResetting] = useState(null); // null | { user, password }
  const [removing, setRemoving] = useState(null);
  const [signingOut, setSigningOut] = useState(false);
  const [shown, setShown] = useState(null); // { username, password, did }: a password that was made up, shown once
  const [busy, setBusy] = useState(false);

  const items = users.data?.items || [];
  const admins = items.filter((user) => user.role === "admin").length;

  const run = async (work, done) => {
    setBusy(true);
    try {
      await work();
      if (done) notify(done, "success");
      await users.refetch();
      return true;
    } catch (error) {
      alertError(error.message);
      return false;
    } finally {
      setBusy(false);
    }
  };

  const add = async (event) => {
    event.preventDefault();
    const form = adding;
    let result;
    const ok = await run(async () => {
      result = await action("/users/create", { username: form.username.trim(), role: form.role, password: form.password || undefined });
    });
    if (!ok) return;
    setAdding(null);
    if (result?.password) setShown({ username: result.username, password: result.password, did: "Added" });
    else notify(`Added ${result?.username || form.username}`, "success");
  };

  const reset = async (event) => {
    event.preventDefault();
    const form = resetting;
    let result;
    const ok = await run(async () => {
      result = await action("/users/password", { username: form.user.username, password: form.password || undefined });
    });
    if (!ok) return;
    setResetting(null);
    // The server ends the sessions of the user whose password changed, and that can be this one: the page then asks for a
    // sign-in, after the new password has been read.
    if (result?.password) setShown({ username: form.user.username, password: result.password, did: "Reset the password of", reloadAfter: form.user.current });
    else if (form.user.current) window.location.reload();
    else notify(`Reset the password of ${form.user.username}`, "success");
  };

  const closeShown = () => {
    const reload = shown?.reloadAfter;
    setShown(null);
    if (reload) window.location.reload();
  };

  const setRole = (user, role) => run(() => action("/users/role", { username: user.username, role }), `${user.username} is now ${role === "admin" ? "an admin" : "a viewer"}`);

  const remove = async () => {
    if (await run(() => action("/users/remove", { username: removing.username }), `Removed ${removing.username}`)) setRemoving(null);
  };

  const signOutEveryone = async () => {
    setBusy(true);
    try {
      await action("/users/sign-out-all");
      // This session ended too: the page asks for a sign-in again.
      window.location.reload();
    } catch (error) {
      alertError(error.message);
      setBusy(false);
    }
  };

  const copy = (text) => {
    navigator.clipboard?.writeText(text).then(
      () => notify("Copied", "success"),
      () => alertError("Could not copy"),
    );
  };

  if (!mutable) return <Muted size="sm" className="rw-settings-empty-inline">Only admins can manage users.</Muted>;

  return (
    <ManagedArea managed={managed}>
      <div className="rw-tokens">
        <Muted size="sm">
          These users sign in with a username and a password. An admin can change everything. A viewer can only look.
          {users.data?.identityProvider ? " An identity provider decides who signs in, so only an admin can use a password." : ""}
        </Muted>
        {users.isLoading ? (
          <Muted size="sm" className="rw-settings-empty-inline">Loading users…</Muted>
        ) : users.isError ? (
          <Muted size="sm" className="rw-settings-empty-inline">{users.error.message}</Muted>
        ) : (
          <div className="rw-rail-stack" data-testid="users-list">
            {items.map((user) => {
              const lastAdmin = user.role === "admin" && admins === 1;
              return (
                <div className="rw-token-row" key={user.username}>
                  <User size={16} aria-hidden="true" className="rw-webhook-icon" />
                  <div className="rw-webhook-main">
                    <div className="rw-webhook-url">
                      <span>{user.username}</span>
                      {user.current ? <span className="rw-vars-kind">You</span> : null}
                    </div>
                    <div className="rw-webhook-meta">{ROLE_LABELS[user.role] || user.role}</div>
                  </div>
                  <Badge tone={user.role === "admin" ? "signal" : "neutral"}>{ROLE_LABELS[user.role] || user.role}</Badge>
                  <Menu position="bottom-end" withinPortal>
                    <Menu.Target>
                      <button type="button" className="rw-icon-btn" aria-label={`Actions for ${user.username}`}>
                        <MoreVertical size={15} />
                      </button>
                    </Menu.Target>
                    <Menu.Dropdown>
                      {user.role === "admin" ? (
                        <Menu.Item disabled={lastAdmin} onClick={() => setRole(user, "viewer")}>
                          {lastAdmin ? "Make viewer (last admin)" : "Make viewer"}
                        </Menu.Item>
                      ) : (
                        <Menu.Item onClick={() => setRole(user, "admin")}>Make admin</Menu.Item>
                      )}
                      <Menu.Item onClick={() => setResetting({ user, password: "" })}>Reset password</Menu.Item>
                      <Menu.Item color="red" disabled={lastAdmin} onClick={() => setRemoving(user)}>
                        {lastAdmin ? "Remove (last admin)" : "Remove"}
                      </Menu.Item>
                    </Menu.Dropdown>
                  </Menu>
                </div>
              );
            })}
            {!items.length ? <Muted size="sm" className="rw-settings-empty-inline">No users yet.</Muted> : null}
          </div>
        )}

        <div className="rw-webhook-actions">
          <Button leftSection={<Plus size={14} />} onClick={() => setAdding({ username: "", role: "viewer", password: "" })}>Add user</Button>
        </div>

        <h3 className="rw-settings-section-title">Sessions</h3>
        <Muted size="sm">
          Ending all sessions signs everyone out, you included, and each person signs in again. API tokens keep working.
        </Muted>
        <div className="rw-webhook-actions">
          <Button variant="outline" onClick={() => setSigningOut(true)}>Sign out everyone</Button>
        </div>
      </div>

      <Modal opened={Boolean(adding)} onClose={() => { if (!busy) setAdding(null); }} title="Add user" centered>
        {adding ? (
          <form onSubmit={add}>
            <Stack gap="sm">
              <Field label="Username" description="Letters, digits, and . _ @ + -">
                <Input
                  required
                  autoFocus
                  maxLength={64}
                  autoComplete="off"
                  value={adding.username}
                  aria-label="Username"
                  onChange={(event) => setAdding({ ...adding, username: event.currentTarget.value })}
                />
              </Field>
              <Field label="Role" description="A viewer can look but not change anything.">
                <Select value={adding.role} aria-label="Role" onChange={(event) => setAdding({ ...adding, role: event.target.value })}>
                  <option value="viewer">Viewer</option>
                  <option value="admin">Admin</option>
                </Select>
              </Field>
              <Field label="Password" description="At least 12 characters. Leave it empty and Geass makes one up and shows it once.">
                <Input
                  type="password"
                  autoComplete="new-password"
                  maxLength={72}
                  value={adding.password}
                  aria-label="Password"
                  placeholder="Make one up for me"
                  onChange={(event) => setAdding({ ...adding, password: event.currentTarget.value })}
                />
              </Field>
              <Group justify="flex-end" gap="sm">
                <Button type="button" variant="outline" disabled={busy} onClick={() => setAdding(null)}>Cancel</Button>
                <Button type="submit" loading={busy}>Add user</Button>
              </Group>
            </Stack>
          </form>
        ) : null}
      </Modal>

      <Modal opened={Boolean(resetting)} onClose={() => { if (!busy) setResetting(null); }} title="Reset password" centered>
        {resetting ? (
          <form onSubmit={reset}>
            <Stack gap="sm">
              <Text size="sm">
                <strong>{resetting.user.username}</strong> is signed out everywhere and signs in with the new password.
                {resetting.user.current ? " That is you: the page asks you to sign in again." : ""}
              </Text>
              <Field label="New password" description="At least 12 characters. Leave it empty and Geass makes one up and shows it once.">
                <Input
                  type="password"
                  autoComplete="new-password"
                  autoFocus
                  maxLength={72}
                  value={resetting.password}
                  aria-label="New password"
                  placeholder="Make one up for me"
                  onChange={(event) => setResetting({ ...resetting, password: event.currentTarget.value })}
                />
              </Field>
              <Group justify="flex-end" gap="sm">
                <Button type="button" variant="outline" disabled={busy} onClick={() => setResetting(null)}>Cancel</Button>
                <Button type="submit" loading={busy}>Reset password</Button>
              </Group>
            </Stack>
          </form>
        ) : null}
      </Modal>

      <Modal
        opened={Boolean(shown)}
        onClose={closeShown}
        title={shown?.did === "Added" ? "User added" : "Password reset"}
        centered
        size="lg"
      >
        <Stack gap="sm">
          <Text size="sm">
            Copy this password for <strong>{shown?.username}</strong> now. It is shown only once.
          </Text>
          <div className="rw-webhook-secret">
            <code data-testid="generated-password">{shown?.password}</code>
            <button type="button" className="rw-icon-btn" aria-label="Copy password" onClick={() => copy(shown.password)}>
              <Copy size={15} />
            </button>
          </div>
          <Group justify="flex-end"><Button onClick={closeShown}>Done</Button></Group>
        </Stack>
      </Modal>

      <Modal opened={Boolean(removing)} onClose={() => { if (!busy) setRemoving(null); }} title="Remove user?" centered>
        <Text size="sm" mb="md">
          <strong>{removing?.username}</strong> is signed out and cannot sign in again.
        </Text>
        <Group justify="flex-end" gap="sm">
          <Button variant="outline" disabled={busy} onClick={() => setRemoving(null)}>Cancel</Button>
          <Button variant="danger" loading={busy} onClick={remove}>Remove user</Button>
        </Group>
      </Modal>

      <Modal opened={signingOut} onClose={() => { if (!busy) setSigningOut(false); }} title="Sign out everyone?" centered>
        <Text size="sm" mb="md">
          Every session ends, yours too. Each person signs in again. API tokens keep working.
          {users.data?.sessionKeyFromEnv ? " The session key is set by an environment variable on the dashboard, so this cannot end the sessions until you change it there." : ""}
        </Text>
        <Group justify="flex-end" gap="sm">
          <Button variant="outline" disabled={busy} onClick={() => setSigningOut(false)}>Cancel</Button>
          <Button variant="danger" loading={busy} onClick={signOutEveryone}>Sign out everyone</Button>
        </Group>
      </Modal>
    </ManagedArea>
  );
}
