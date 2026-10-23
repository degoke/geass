import { AppLink } from "@/components/AppLink";
import { useHidden } from "@/lib/options";
import { Button } from "@/components/geass-ui";
import { SettingsPanel, SettingsSection } from "@/layout/SettingsShell";
import { GitHubManualForm } from "@/pages/platform/GitHubManualForm";
import { GitHubAppActions } from "@/pages/platform/GitHubAppActions";
import { GitHubExternalPanel } from "@/pages/platform/GitHubExternalPanel";

export function GitHubConnectorPanel({ info, reload, inSheet = false }) {
  const hideDomain = useHidden("domain");
  const body = info.external ? (
        <GitHubExternalPanel external={info.external} />
      ) : !info.hasDashboardURL ? (
        <>
          <p className="rw-rail-body">
            Connect Geass to GitHub for repository deploys. {hideDomain ? "The GitHub App needs a public host, which is set for you." : "Set a public host before registering the GitHub App."}
          </p>
          {hideDomain ? null : (
            <SettingsPanel variant="flat">
              <AppLink href="/settings/domain"><Button>Configure domain</Button></AppLink>
            </SettingsPanel>
          )}
        </>
      ) : (
        <>
          {info.manifestAction && (
            <SettingsPanel className="rw-settings-panel-spaced">
              <h3 className="rw-settings-subtitle">Create on GitHub</h3>
              <p className="rw-settings-section-desc">Register an app with the correct callback and webhook URLs.</p>
              <form method="post" action={info.manifestAction} className="rw-form">
                <input type="hidden" name="manifest" value={info.manifest || ""} />
                <div className="rw-form-actions">
                  <Button type="submit">Create GitHub App on GitHub</Button>
                </div>
              </form>
            </SettingsPanel>
          )}
          <GitHubManualForm reload={reload} />
          <GitHubAppActions reload={reload} configured={Boolean(info.hasGitHubApp)} />
        </>
      );

  if (inSheet) {
    return <div className="rw-settings-sheet-panel">{body}</div>;
  }
  return (
    <SettingsSection title="GitHub" flat>
      {body}
    </SettingsSection>
  );
}
