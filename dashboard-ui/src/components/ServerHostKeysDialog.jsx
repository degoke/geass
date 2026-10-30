import { useEffect, useState } from "react";
import { Modal, Stack } from "@mantine/core";
import { action } from "@/lib/api";
import { alertUser, alertError } from "@/lib/notify";
import { Button, Field, Muted, Textarea } from "@/components/geass-ui";

/**
 * Replaces the host keys Geass accepts from a server you added. A server that was rebuilt answers with a new key, and
 * Geass refuses it until the new key is listed here. Only the lines for this server change.
 */
export function ServerHostKeysDialog({ server, onClose, onSaved }) {
  const [keys, setKeys] = useState("");
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    setKeys("");
  }, [server]);

  const submit = async (event) => {
    event.preventDefault();
    setBusy(true);
    try {
      const result = await action(`/servers/${encodeURIComponent(server.name)}/host-keys`, { knownHosts: keys.trim() });
      alertUser(result?.failed ? "Host keys updated. Click Retry to join the server again." : "Host keys updated");
      await onSaved?.();
      onClose();
    } catch (error) {
      alertError(error.message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal opened={Boolean(server)} onClose={() => { if (!busy) onClose(); }} title={server ? `Host keys of ${server.name}` : ""} centered>
      {server ? (
        <form onSubmit={submit}>
          <Stack gap="sm">
            <Field
              label="Host keys"
              description={`Run ssh-keyscan -t ed25519 ${server.address || "ADDRESS"} on your computer, and paste what it prints.`}
            >
              <Textarea
                value={keys}
                onChange={(event) => setKeys(event.currentTarget.value)}
                rows={4}
                placeholder={`${server.address || "198.51.100.7"} ssh-ed25519 AAAA…`}
                aria-label="Host keys"
                spellCheck={false}
              />
            </Field>
            <Muted size="xs">Check the fingerprint on the server before you trust the key. Only the lines for this server change.</Muted>
            <Button type="submit" loading={busy} disabled={!keys.trim()}>Save host keys</Button>
          </Stack>
        </form>
      ) : null}
    </Modal>
  );
}
