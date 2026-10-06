import { useState } from "react";
import { CloudCog, GitBranch } from "lucide-react";
import { Button } from "@/components/geass-ui";
import { SettingsSection, SettingsSheet } from "@/layout/SettingsShell";
import { GitHubConnectorPanel } from "@/pages/platform/GitHubConnectorPanel";
import { CloudflareConnectorPanel } from "@/pages/platform/CloudflareConnectorPanel";

const STATUS_LABEL = {
  connected: "Connected",
  pending: "Pending",
  disconnected: "Not connected",
};

function ConnectorCard({ icon: Icon, name, description, actionLabel, onConfigure, status = "disconnected" }) {
  return (
    <div className={`rw-settings-integration-card is-${status}`} data-connection-status={status}>
      <div className="rw-settings-integration-icon" aria-hidden="true">
        <Icon size={22} />
      </div>
      <div className="rw-settings-integration-body">
        <div className="rw-settings-integration-heading">
          <h3>{name}</h3>
          <span className={`rw-integration-status is-${status}`}>{STATUS_LABEL[status]}</span>
        </div>
        <p>{description}</p>
        <Button type="button" variant="outline" onClick={onConfigure} aria-label={`${actionLabel} ${name}`}>
          {actionLabel}
        </Button>
      </div>
    </div>
  );
}

export function ConnectorsPage({ reload, githubInfo, cloudflareInfo }) {
  const [active, setActive] = useState(null);
  const githubConnected = Boolean(githubInfo?.hasGitHubApp);
  const cloudflareConnected = Boolean(cloudflareInfo?.connected);
  const cloudflareReady = Boolean(cloudflareInfo?.ready);

  const cloudflareStatus = !cloudflareConnected ? "disconnected" : cloudflareReady ? "connected" : "pending";
  const githubStatus = githubConnected ? "connected" : "disconnected";

  const cloudflareDescription = !cloudflareConnected
    ? "Connect Cloudflare to run cloudflared in-cluster and manage tunnel routes and DNS."
    : cloudflareReady
      ? "Tunnel and DNS are managed automatically in your cluster."
      : "Credentials saved — waiting for cloudflared and tunnel setup to finish.";

  return (
    <>
      <SettingsSection title="Connectors" flat>
        <p className="rw-rail-body">Connect external services once for the whole workspace.</p>
        <div className="rw-settings-connectors-grid">
          <ConnectorCard
            icon={GitBranch}
            name="GitHub"
            status={githubStatus}
            description={
              githubConnected
                ? "Repository deploys and webhooks use your GitHub App."
                : "Register a GitHub App for repository deploys."
            }
            actionLabel="Configure"
            onConfigure={() => setActive("github")}
          />
          <ConnectorCard
            icon={CloudCog}
            name="Cloudflare"
            status={cloudflareStatus}
            description={cloudflareDescription}
            actionLabel={cloudflareConnected ? "Manage" : "Configure"}
            onConfigure={() => setActive("cloudflare")}
          />
        </div>
      </SettingsSection>

      <SettingsSheet
        opened={active === "github"}
        onClose={() => setActive(null)}
        title="GitHub"
        description="Register or update the GitHub App used for repository deploys."
      >
        <GitHubConnectorPanel info={githubInfo || {}} reload={reload} inSheet />
      </SettingsSheet>

      <SettingsSheet
        opened={active === "cloudflare"}
        onClose={() => setActive(null)}
        title="Cloudflare"
        description={
          cloudflareConnected
            ? cloudflareReady
              ? "Tunnel and DNS are active for your Geass domain."
              : "Finishing tunnel setup in the cluster."
            : "Connect Cloudflare to run cloudflared and manage DNS."
        }
      >
        <CloudflareConnectorPanel reload={reload} info={cloudflareInfo || {}} inSheet />
      </SettingsSheet>
    </>
  );
}
