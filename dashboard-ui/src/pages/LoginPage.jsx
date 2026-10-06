import { useState } from "react";
import { Button, PasswordInput, Stack, Text, TextInput, Title } from "@mantine/core";
import { action } from "@/lib/api";
import { alertUser } from "@/lib/notify";
import { LoginPanel } from "@/components/geass-ui";

export function LoginPage({ onSuccess }) {
  const [username, setUsername] = useState("admin");
  const [password, setPassword] = useState("");
  const [pending, setPending] = useState(false);
  const submit = (event) => {
    event.preventDefault();
    setPending(true);
    action("/login", { username, password }).then(() => onSuccess()).catch((error) => alertUser(error.message)).finally(() => setPending(false));
  };
  return (
    <LoginPanel>
      <Stack gap="xs" align="center" ta="center">
        <Title order={2}>Sign in</Title>
        <Text size="sm" c="dimmed">Use your dashboard username and password.</Text>
      </Stack>
      <form onSubmit={submit}>
        <Stack>
          <TextInput label="Username" required autoFocus value={username} onChange={(event) => setUsername(event.target.value)} autoComplete="username" />
          <PasswordInput label="Password" required value={password} onChange={(event) => setPassword(event.target.value)} autoComplete="current-password" />
          <Button type="submit" loading={pending} fullWidth>Continue</Button>
        </Stack>
      </form>
    </LoginPanel>
  );
}
