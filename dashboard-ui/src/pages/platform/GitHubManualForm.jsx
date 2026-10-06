import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { action } from "@/lib/api";
import { canMutate, useBootstrap } from "@/lib/dashboard";
import { alertUser } from "@/lib/notify";
import { Button, Field, FormStack, Input, Textarea } from "@/components/geass-ui";
import { SettingsFormActions, SettingsPanel } from "@/layout/SettingsShell";

export function GitHubManualForm({ reload }) {
  const { data } = useBootstrap();
  const queryClient = useQueryClient();
  const [values, setValues] = useState({ appID: "", clientID: "", slug: "", clientSecret: "", webhookSecret: "", privateKey: "" });
  return (
    <SettingsPanel variant="flat" className="rw-settings-panel-spaced">
      <h3 className="rw-settings-section-title">Manual credentials</h3>
      <p className="rw-rail-body">Paste values from an existing GitHub App.</p>
      <FormStack className="rw-rail-form" onSubmit={(event) => { event.preventDefault(); action("/settings/github/save", values).then(() => { reload(); queryClient.invalidateQueries({ queryKey: ["github-settings"] }); alertUser("GitHub App credentials saved"); }).catch((error) => alertUser(error.message)); }}>
        <Field label="App ID"><Input required value={values.appID} onChange={(event) => setValues({ ...values, appID: event.target.value })} /></Field>
        <Field label="Client ID"><Input required value={values.clientID} onChange={(event) => setValues({ ...values, clientID: event.target.value })} /></Field>
        <Field label="App slug"><Input required value={values.slug} onChange={(event) => setValues({ ...values, slug: event.target.value })} /></Field>
        <Field label="Client secret"><Input type="password" value={values.clientSecret} onChange={(event) => setValues({ ...values, clientSecret: event.target.value })} /></Field>
        <Field label="Webhook secret"><Input type="password" value={values.webhookSecret} onChange={(event) => setValues({ ...values, webhookSecret: event.target.value })} /></Field>
        <Field label="Private key (PEM)"><Textarea rows={6} value={values.privateKey} onChange={(event) => setValues({ ...values, privateKey: event.target.value })} /></Field>
        <SettingsFormActions>
          <Button type="submit" disabled={!canMutate(data)}>Save GitHub App</Button>
        </SettingsFormActions>
      </FormStack>
    </SettingsPanel>
  );
}
