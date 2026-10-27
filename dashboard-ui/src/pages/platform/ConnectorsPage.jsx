import { useMemo, useState } from "react";
import { Cloud, CloudCog, Database, GitBranch } from "lucide-react";
import { list } from "@/lib/api";
import { canMutate } from "@/lib/dashboard";
import { isDataConnection } from "@/lib/compute";
import { Button } from "@/components/geass-ui";
import { CloudConnections } from "@/components/compute/CloudConnections";
import { SettingsDivider, SettingsSection, SettingsSheet } from "@/layout/SettingsShell";
import { GitHubConnectorPanel } from "@/pages/platform/GitHubConnectorPanel";
import { CloudflareConnectorPanel } from "@/pages/platform/CloudflareConnectorPanel";
import { CloudConnectionConnectorPanel } from "@/pages/platform/CloudConnectionConnectorPanel";

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

function providerConnectionStatus(connections, provider) {
  const scoped = connections.filter((item) => item.spec?.provider === provider);
  if (!scoped.length) return "disconnected";
  if (scoped.every((item) => item.status?.available)) return "connected";
  return "pending";
}

export function ConnectorsPage({ data, reload, githubInfo, cloudflareInfo }) {
  const [active, setActive] = useState(null);
  // The connections that hold buckets or databases: a project's or an organization's, or a cluster's that also creates servers.
  const projectConnections = useMemo(() => list(data, "cloudConnections").filter((item) => isDataConnection(item.spec?.purposes)), [data]);
  const githubConnected = Boolean(githubInfo?.hasGitHubApp);
  const cloudflareConnected = Boolean(cloudflareInfo?.connected);
  const cloudflareReady = Boolean(cloudflareInfo?.ready);

  const cloudflareStatus = !cloudflareConnected ? "disconnected" : cloudflareReady ? "connected" : "pending";
  const githubStatus = githubConnected ? "connected" : "disconnected";
  const awsStatus = providerConnectionStatus(projectConnections, "AWS");
  const planetScaleStatus = providerConnectionStatus(projectConnections, "PlanetScale");

  return (
    <>
      <SettingsSection title="Code and domains" flat>
        <div className="rw-settings-connectors-grid">
          <ConnectorCard
            icon={GitBranch}
            name="GitHub"
            status={githubStatus}
            description={
              githubInfo?.external
                ? `GitHub App for repository deploys, run by ${githubInfo.external.provider}.`
                : "GitHub App for repository deploys."
            }
            actionLabel="Configure"
            onConfigure={() => setActive("github")}
          />
          <ConnectorCard
            icon={CloudCog}
            name="Cloudflare"
            status={cloudflareStatus}
            description={cloudflareConnected && !cloudflareReady ? "Setting up the tunnel." : "Tunnel routes and DNS."}
            actionLabel={cloudflareConnected ? "Manage" : "Configure"}
            onConfigure={() => setActive("cloudflare")}
          />
        </div>
      </SettingsSection>

      <SettingsDivider />
      <CloudConnections mutable={canMutate(data)} />

      <SettingsDivider />
      <SettingsSection title="Storage and databases" flat>
        <div className="rw-settings-connectors-grid">
          <ConnectorCard
            icon={Cloud}
            name="AWS"
            status={awsStatus}
            description="S3 buckets, backups and RDS databases."
            actionLabel={awsStatus === "disconnected" ? "Configure" : "Manage"}
            onConfigure={() => setActive("aws")}
          />
          <ConnectorCard
            icon={Database}
            name="PlanetScale"
            status={planetScaleStatus}
            description="External MySQL databases."
            actionLabel={planetScaleStatus === "disconnected" ? "Configure" : "Manage"}
            onConfigure={() => setActive("planetscale")}
          />
        </div>
      </SettingsSection>

      <SettingsSheet opened={active === "github"} onClose={() => setActive(null)} title="GitHub">
        <GitHubConnectorPanel info={githubInfo || {}} reload={reload} inSheet />
      </SettingsSheet>

      <SettingsSheet opened={active === "cloudflare"} onClose={() => setActive(null)} title="Cloudflare">
        <CloudflareConnectorPanel reload={reload} info={cloudflareInfo || {}} inSheet />
      </SettingsSheet>

      <SettingsSheet opened={active === "aws"} onClose={() => setActive(null)} title="AWS">
        <CloudConnectionConnectorPanel data={data} reload={reload} provider="AWS" inSheet />
      </SettingsSheet>

      <SettingsSheet opened={active === "planetscale"} onClose={() => setActive(null)} title="PlanetScale">
        <CloudConnectionConnectorPanel data={data} reload={reload} provider="PlanetScale" inSheet />
      </SettingsSheet>
    </>
  );
}
