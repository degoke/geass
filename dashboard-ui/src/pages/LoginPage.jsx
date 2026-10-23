import { useEffect, useRef, useState } from "react";
import { Alert, Button, Loader, PasswordInput, Stack, TextInput, Title } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import { action, api } from "@/lib/api";
import { alertError } from "@/lib/notify";
import { rememberedSSO, rememberSSO, shouldAutoRedirect, ssoErrorMessage, ssoLoginURL } from "@/lib/sso";
import { LoginPanel } from "@/components/geass-ui";

// Where an error from signing in through the identity provider was sent: read once, then taken out of the address.
function takeSSOError() {
  const query = new URLSearchParams(window.location.search);
  const code = query.get("sso_error");
  if (!code) return "";
  query.delete("sso_error");
  const rest = query.toString();
  window.history.replaceState(null, "", `${window.location.pathname}${rest ? `?${rest}` : ""}`);
  return code;
}

export function LoginPage({ onSuccess }) {
  // The page asks whether the identity provider decides who signs in: when it does, its button comes first, and a password
  // is for the admin who keeps one for when it is down.
  const session = useQuery({ queryKey: ["login-options"], queryFn: () => api("/api/session"), staleTime: 0 });
  const sso = session.data?.sso;
  // What the provider sent the person back with is read from the address once, in an effect (reading it takes it out of the
  // address), and kept in a ref so that where effects run twice (React StrictMode) the second run does not find it gone.
  // null means not read yet, "" that there was nothing.
  const taken = useRef(null);
  const [failure, setFailure] = useState(null);
  const [passwordOpen, setPasswordOpen] = useState(false);
  useEffect(() => {
    if (taken.current === null) taken.current = takeSSOError();
    setFailure(taken.current);
  }, []);

  // Someone who signed in this way last time goes straight to the provider, unless it just sent them back with a problem.
  useEffect(() => {
    if (sso && failure === "" && rememberedSSO() && shouldAutoRedirect()) window.location.assign(ssoLoginURL());
  }, [sso, failure]);

  // Until it is known whether the provider decides, nothing that could be the wrong form is shown.
  if (session.isLoading || failure === null) {
    return (
      <LoginPanel>
        <Stack align="center" py="md"><Loader size="sm" aria-label="Loading" /></Stack>
      </LoginPanel>
    );
  }

  if (!sso) {
    return (
      <LoginPanel>
        <Stack gap="xs" align="center" ta="center">
          <Title order={2}>Sign in</Title>
        </Stack>
        <PasswordForm onSuccess={onSuccess} />
      </LoginPanel>
    );
  }
  return (
    <LoginPanel>
      <Stack gap="xs" align="center" ta="center">
        <Title order={2}>Sign in</Title>
      </Stack>
      <Stack>
        {failure ? <Alert color="red" variant="light">{ssoErrorMessage(failure)}</Alert> : null}
        <Button
          fullWidth
          onClick={() => {
            rememberSSO(true);
            window.location.assign(ssoLoginURL());
          }}
        >
          Sign in with {sso.name}
        </Button>
        {passwordOpen ? (
          <PasswordForm onSuccess={onSuccess} />
        ) : (
          <Button variant="subtle" size="compact-sm" fullWidth onClick={() => { rememberSSO(false); setPasswordOpen(true); }}>
            Use an admin password instead
          </Button>
        )}
      </Stack>
    </LoginPanel>
  );
}

function PasswordForm({ onSuccess }) {
  const [username, setUsername] = useState("admin");
  const [password, setPassword] = useState("");
  const [pending, setPending] = useState(false);
  const submit = (event) => {
    event.preventDefault();
    setPending(true);
    action("/login", { username, password }).then(() => onSuccess()).catch((error) => alertError(error.message)).finally(() => setPending(false));
  };
  return (
    <form onSubmit={submit}>
      <Stack>
        <TextInput label="Username" required autoFocus value={username} onChange={(event) => setUsername(event.target.value)} autoComplete="username" />
        <PasswordInput label="Password" required value={password} onChange={(event) => setPassword(event.target.value)} autoComplete="current-password" />
        <Button type="submit" loading={pending} fullWidth>Continue</Button>
      </Stack>
    </form>
  );
}
