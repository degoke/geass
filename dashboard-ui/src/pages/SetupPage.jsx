import { useRef, useState } from "react";
import { Alert, Button, PasswordInput, Stack, TextInput, Title } from "@mantine/core";
import { action } from "@/lib/api";
import { alertError } from "@/lib/notify";
import { LoginPanel } from "@/components/geass-ui";

// The token is after the # of the link. It is read once and taken out of the address, so that it is not left in the history.
function takeSetupToken() {
  const token = window.location.hash.replace(/^#/, "");
  if (token) window.history.replaceState(null, "", `${window.location.pathname}${window.location.search}`);
  return token;
}

export function SetupPage({ onSuccess }) {
  const token = useRef(null);
  if (token.current === null) token.current = takeSetupToken();
  const [username, setUsername] = useState("admin");
  const [password, setPassword] = useState("");
  const [pending, setPending] = useState(false);

  const submit = (event) => {
    event.preventDefault();
    setPending(true);
    action("/setup", { token: token.current, username, password })
      .then(() => onSuccess())
      .catch((error) => alertError(error.message))
      .finally(() => setPending(false));
  };

  return (
    <LoginPanel>
      <Stack gap="xs" align="center" ta="center">
        <Title order={2}>Create your admin</Title>
      </Stack>
      {!token.current ? (
        <Alert color="yellow" variant="light">Open the setup link that the installer printed.</Alert>
      ) : (
        <form onSubmit={submit}>
          <Stack>
            <TextInput label="Username" required value={username} onChange={(event) => setUsername(event.target.value)} autoComplete="username" />
            <PasswordInput label="Password" description="At least 12 characters" required minLength={12} autoFocus value={password} onChange={(event) => setPassword(event.target.value)} autoComplete="new-password" />
            <Button type="submit" loading={pending} fullWidth>Create admin</Button>
          </Stack>
        </form>
      )}
    </LoginPanel>
  );
}
