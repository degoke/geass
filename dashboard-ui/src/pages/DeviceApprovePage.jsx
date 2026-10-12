import { useEffect, useState } from "react";
import { Stack, Text } from "@mantine/core";
import { action, api } from "@/lib/api";
import { canMutate } from "@/lib/dashboard";
import { alertUser } from "@/lib/notify";
import { PlatformPage } from "@/layout/PlatformPage";
import { Button, Empty, Field, Input, Muted, Select } from "@/components/geass-ui";

const ACCESS = [
  { value: "observer", label: "Read only, including logs (recommended)" },
  { value: "viewer", label: "Read only, without logs" },
  { value: "admin", label: "Full access" },
];

const EXPIRY = [
  { value: "7", label: "7 days" },
  { value: "30", label: "30 days" },
  { value: "90", label: "90 days" },
];

/**
 * Where a person approves a command-line sign-in. The command line shows a code and sends them here; they
 * check the code is the one on their screen and approve it, and the command line is handed an API token.
 */
export function DeviceApprovePage({ data, code: initialCode }) {
  const [code, setCode] = useState(initialCode || "");
  const [request, setRequest] = useState(null);
  const [missing, setMissing] = useState(false);
  const [role, setRole] = useState("observer");
  const [expires, setExpires] = useState("30");
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState("");
  const mutable = canMutate(data);

  useEffect(() => {
    setRequest(null);
    setMissing(false);
    const wanted = code.trim();
    if (wanted.replace(/[-\s]/g, "").length < 8) return undefined;
    let cancelled = false;
    api(`/api/v1/auth/device/${encodeURIComponent(wanted)}`)
      .then((found) => { if (!cancelled) setRequest(found); })
      .catch(() => { if (!cancelled) setMissing(true); });
    return () => { cancelled = true; };
  }, [code]);

  const decide = async (deny) => {
    setBusy(true);
    try {
      await action("/auth/device/approve", { userCode: code, role, expiresInDays: expires, deny: deny ? "on" : undefined });
      setResult(deny ? "Refused. The command line was told no." : "Approved. You can close this page: the command line is signed in.");
      setRequest(null);
    } catch (error) {
      alertUser(error.message);
    } finally {
      setBusy(false);
    }
  };

  if (!mutable) {
    return (
      <Empty title="Only an admin can approve this" description="Sign in as an admin to approve a command-line sign-in." />
    );
  }

  return (
    <PlatformPage title="Sign in the command line" description="Approve the code your command line is showing, and it will be handed an API token. Only approve a code you started yourself.">
      <Stack gap="md" maw={520}>
        <Field label="Code" description="The code shown by the command line, such as BCDF-GHJK.">
          <Input value={code} onChange={(event) => { setCode(event.currentTarget.value); setResult(""); }} placeholder="BCDF-GHJK" aria-label="Code" autoFocus />
        </Field>
        {missing ? <Muted size="sm">No sign-in request has that code. It may have expired: start again from the command line.</Muted> : null}
        {result ? <Text size="sm" fw={500}>{result}</Text> : null}
        {request ? (
          <Stack gap="sm">
            <Text size="sm">
              <strong>{request.clientName}</strong> is asking for access. Check that the code on your command line is
              <strong> {request.userCode}</strong> before you approve.
            </Text>
            <Field label="Access">
              <Select value={role} onChange={(event) => setRole(event.target.value)} aria-label="Access">
                {ACCESS.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}
              </Select>
            </Field>
            <Field label="Expires">
              <Select value={expires} onChange={(event) => setExpires(event.target.value)} aria-label="Expires">
                {EXPIRY.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}
              </Select>
            </Field>
            <Stack gap="xs">
              <Button onClick={() => decide(false)} loading={busy}>Approve</Button>
              <Button variant="outline" onClick={() => decide(true)} disabled={busy}>Refuse</Button>
            </Stack>
          </Stack>
        ) : null}
      </Stack>
    </PlatformPage>
  );
}
