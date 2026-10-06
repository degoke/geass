import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { action } from "@/lib/api";
import { canMutate, useBootstrap } from "@/lib/dashboard";
import { alertUser } from "@/lib/notify";
import { Button, Field, FormStack, Input } from "@/components/geass-ui";
import { SettingsAlert, SettingsFormActions, SettingsPanel } from "@/layout/SettingsShell";

export function GitHubAppActions({ reload, configured }) {
  const { data } = useBootstrap();
  const queryClient = useQueryClient();
  const [confirm, setConfirm] = useState("");
  if (!canMutate(data)) return null;
  return (
    <SettingsPanel variant="flat">
      <h3 className="rw-settings-section-title">Maintenance</h3>
      <p className="rw-rail-body">Verify webhooks or disconnect the app.</p>
      <FormStack className="rw-rail-form">
        <SettingsFormActions>
          <Button type="button" variant="outline" disabled={!configured} onClick={() => action("/settings/github/test").then(() => alertUser("GitHub App webhook matches this dashboard")).catch((error) => alertUser(error.message))}>
            Test GitHub App
          </Button>
        </SettingsFormActions>
        {configured && (
          <>
            <SettingsAlert tone="danger">
              Removing the GitHub App disconnects repository deploys until you connect again.
            </SettingsAlert>
            <Field label="Confirmation" description='Type "remove-github" to disconnect.'>
              <Input value={confirm} onChange={(event) => setConfirm(event.target.value)} autoComplete="off" placeholder="remove-github" />
            </Field>
            <SettingsFormActions>
              <Button type="button" variant="danger" disabled={confirm !== "remove-github"} onClick={() => action("/settings/github/clear", { confirm }).then(() => { reload(); queryClient.invalidateQueries({ queryKey: ["github-settings"] }); alertUser("GitHub App removed"); }).catch((error) => alertUser(error.message))}>
                Remove GitHub App
              </Button>
            </SettingsFormActions>
          </>
        )}
      </FormStack>
    </SettingsPanel>
  );
}
