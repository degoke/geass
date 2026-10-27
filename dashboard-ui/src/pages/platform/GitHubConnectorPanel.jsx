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
          <p className="rw-rail-body">{hideDomain ? "The public host is already set." : "Set a public host first."}</p>
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
