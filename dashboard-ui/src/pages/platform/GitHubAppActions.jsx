import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { action } from "@/lib/api";
import { canMutate, useBootstrap } from "@/lib/dashboard";
import { alertUser, alertError, notify } from "@/lib/notify";
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
      <FormStack className="rw-rail-form">
        <SettingsFormActions>
          <Button type="button" variant="outline" disabled={!configured} onClick={() => action("/settings/github/test").then((result) => {
            // The webhook is right but the app lacks permissions: a long message that says how to approve them, so it stays up.
            if (result?.warning) notify(result.warning, "error", { autoClose: 30000 });
            else alertUser("Webhook matches");
          }).catch((error) => alertError(error.message))}>
            Test GitHub App
          </Button>
        </SettingsFormActions>
        {configured && (
          <>
            <SettingsAlert tone="danger">
              Removing the GitHub App stops repository deploys.
            </SettingsAlert>
            <Field label="Confirmation" description='Type "remove-github".'>
              <Input value={confirm} onChange={(event) => setConfirm(event.target.value)} autoComplete="off" placeholder="remove-github" />
            </Field>
            <SettingsFormActions>
              <Button type="button" variant="danger" disabled={confirm !== "remove-github"} onClick={() => action("/settings/github/clear", { confirm }).then(() => { reload(); queryClient.invalidateQueries({ queryKey: ["github-settings"] }); alertUser("GitHub App removed"); }).catch((error) => alertError(error.message))}>
                Remove GitHub App
              </Button>
            </SettingsFormActions>
          </>
        )}
      </FormStack>
    </SettingsPanel>
  );
}
