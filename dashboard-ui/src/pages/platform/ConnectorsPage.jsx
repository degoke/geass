import { useMemo, useState } from "react";
import { Cloud, CloudCog, Database, GitBranch } from "lucide-react";
import { list } from "@/lib/api";
import { canMutate } from "@/lib/dashboard";
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
  const cloudConnections = useMemo(() => list(data, "cloudConnections"), [data]);
  const githubConnected = Boolean(githubInfo?.hasGitHubApp);
  const cloudflareConnected = Boolean(cloudflareInfo?.connected);
  const cloudflareReady = Boolean(cloudflareInfo?.ready);

  const cloudflareStatus = !cloudflareConnected ? "disconnected" : cloudflareReady ? "connected" : "pending";
  const githubStatus = githubConnected ? "connected" : "disconnected";
  const awsStatus = providerConnectionStatus(cloudConnections, "AWS");
  const planetScaleStatus = providerConnectionStatus(cloudConnections, "PlanetScale");

  const cloudflareDescription = !cloudflareConnected
    ? "Connect Cloudflare to run cloudflared in-cluster and manage tunnel routes and DNS."
    : cloudflareReady
      ? "Tunnel and DNS are managed automatically in your cluster."
      : "Credentials saved — waiting for cloudflared and tunnel setup to finish.";

  return (
    <>
      <SettingsSection title="Connectors" flat>
        <p className="rw-rail-body">
          Connect external services for the workspace. Cloud credentials are scoped per Geass project when you add them.
        </p>
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
          <ConnectorCard
            icon={Cloud}
            name="AWS"
            status={awsStatus}
            description={
              awsStatus === "disconnected"
                ? "Add AWS credentials for S3 buckets and external databases."
                : "External buckets and databases can use your AWS connections."
            }
            actionLabel={awsStatus === "disconnected" ? "Configure" : "Manage"}
            onConfigure={() => setActive("aws")}
          />
          <ConnectorCard
            icon={Database}
            name="PlanetScale"
            status={planetScaleStatus}
            description={
              planetScaleStatus === "disconnected"
                ? "Add PlanetScale credentials for external MySQL databases."
                : "PlanetScale databases are coming soon."
            }
            actionLabel={planetScaleStatus === "disconnected" ? "Configure" : "Manage"}
            onConfigure={() => setActive("planetscale")}
          />
        </div>
      </SettingsSection>

      <SettingsDivider />
      <CloudConnections mutable={canMutate(data)} />

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

      <SettingsSheet
        opened={active === "aws"}
        onClose={() => setActive(null)}
        title="AWS"
        description="Store AWS credentials for project-scoped S3 and database connections."
      >
        <CloudConnectionConnectorPanel data={data} reload={reload} provider="AWS" inSheet />
      </SettingsSheet>

      <SettingsSheet
        opened={active === "planetscale"}
        onClose={() => setActive(null)}
        title="PlanetScale"
        description="Store PlanetScale service tokens for project-scoped external databases."
      >
        <CloudConnectionConnectorPanel data={data} reload={reload} provider="PlanetScale" inSheet />
      </SettingsSheet>
    </>
  );
}
