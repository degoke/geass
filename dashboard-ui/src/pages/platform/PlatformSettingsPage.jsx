import { useQuery } from "@tanstack/react-query";
import { api, list } from "@/lib/api";
import { ApiTokens } from "@/components/ApiTokens";
import { AlertSettings } from "@/components/AlertSettings";
import { AuthSettings } from "@/components/AuthSettings";
import { OrgRegistrySettings } from "@/components/OrgRegistrySettings";
import { AppLink } from "@/components/AppLink";
import { Button, KeyValueTable } from "@/components/geass-ui";
import { SETTINGS_LINKS } from "@/lib/platform-nav";
import { canMutate, isPlatformSession } from "@/lib/dashboard";
import { useDashboardOptions, visibleSettingsLinks } from "@/lib/options";
import { SettingsDivider, SettingsPanel, SettingsSection } from "@/layout/SettingsShell";
import { DomainSettingsPage } from "@/pages/platform/DomainSettingsPage";
import { ClusterCapacityPage } from "@/pages/platform/ClusterCapacityPage";
import { ObjectStorageSettingsPage } from "@/pages/platform/ObjectStorageSettingsPage";
import { SharedDatabasesPage } from "@/pages/platform/SharedDatabasesPage";
import { ConnectorsPage } from "@/pages/platform/ConnectorsPage";
import { OrganizationsPage } from "@/pages/platform/OrganizationsPage";

function apexFromDashboardHost(host, subdomain = "geass") {
  const h = String(host || "").trim().toLowerCase();
  const prefix = `${subdomain}.`;
  if (h.startsWith(prefix)) return h.slice(prefix.length);
  const dot = h.indexOf(".");
  return dot > 0 ? h.slice(dot + 1) : "";
}

function cloudflareInfoFromBootstrap(data, apiPayload) {
  const config = data?.platformConfig?.items?.[0];
  const zoneId = apiPayload?.zoneId || data?.platform?.cloudflareZoneId || config?.spec?.cloudflareZoneID || "";
  const fromDashboardHost = apexFromDashboardHost(apiPayload?.dashboardHost);
  const zoneName =
    apiPayload?.zoneName ||
    apiPayload?.suggestedRootDomain ||
    data?.platform?.cloudflareZoneName ||
    config?.spec?.rootDomain ||
    fromDashboardHost ||
    "";
  const resolvedName = apiPayload?.zoneName || data?.platform?.cloudflareZoneName || zoneName;
  return {
    ...(apiPayload || {}),
    connected: apiPayload?.connected ?? Boolean(data?.platform?.hasCloudflare),
    ready: apiPayload?.ready ?? Boolean(data?.platform?.cloudflareReady),
    zoneId,
    zoneName: resolvedName,
    suggestedRootDomain:
      apiPayload?.suggestedRootDomain ||
      config?.spec?.rootDomain ||
      data?.platform?.cloudflareZoneName ||
      resolvedName,
  };
}

export function PlatformSettingsPage({ data, page, reload }) {
  const { hidden } = useDashboardOptions();
  const config = data?.platformConfig?.items?.[0];
  const connectorsPage = page === "connectors";
  const github = useQuery({ queryKey: ["github-settings"], queryFn: () => api("/api/settings/github"), enabled: connectorsPage });
  const cloudflare = useQuery({
    queryKey: ["cloudflare-settings"],
    queryFn: () => api("/api/settings/cloudflare"),
    enabled: connectorsPage || page === "domain",
    staleTime: 0,
    refetchOnMount: "always",
  });
  // Everything but the tokens is the platform's: an organization's administrators are not offered it, and would be refused.
  if (page !== "tokens" && page !== "alerts" && page !== "registry" && !isPlatformSession(data)) {
    return (
      <SettingsSection title="Settings" flat>
        <p className="rw-rail-body">These settings belong to the platform's administrators. As an administrator of an organization you manage its API tokens and its alerts here.</p>
      </SettingsSection>
    );
  }
  if (page === "organizations") return <OrganizationsPage data={data} />;
  if (page === "domain") {
    return (
      <DomainSettingsPage
        config={config}
        reload={reload}
        platformSummary={data?.platform}
        cloudflareLoading={cloudflare.isLoading || cloudflare.isFetching}
        cloudflareInfo={cloudflareInfoFromBootstrap(data, cloudflare.data)}
      />
    );
  }
  if (connectorsPage) {
    return (
      <ConnectorsPage
        data={data}
        reload={reload}
        githubInfo={github.data || {}}
        cloudflareInfo={cloudflareInfoFromBootstrap(data, cloudflare.data)}
      />
    );
  }
  if (page === "tokens") {
    return (
      <SettingsSection title="API tokens" flat>
        <ApiTokens scope="workspace" projects={list(data, "projects")} mutable={canMutate(data)} data={data} />
      </SettingsSection>
    );
  }
  if (page === "auth") {
    return (
      <SettingsSection title="Sign-in" flat>
        <AuthSettings mutable={canMutate(data)} />
      </SettingsSection>
    );
  }
  if (page === "alerts") {
    return (
      <SettingsSection title="Alerts" flat>
        <AlertSettings mutable={canMutate(data)} projects={list(data, "projects")} org={!isPlatformSession(data)} />
      </SettingsSection>
    );
  }
  if (page === "registry") {
    return (
      <SettingsSection title="Registry" flat>
        <OrgRegistrySettings mutable={canMutate(data) && !isPlatformSession(data)} />
      </SettingsSection>
    );
  }
  if (page === "ha" || page === "cluster") return <ClusterCapacityPage data={data} reload={reload} embedded />;
  if (page === "object") return <ObjectStorageSettingsPage data={data} reload={reload} embedded />;
  if (page === "databases") return <SharedDatabasesPage data={data} reload={reload} embedded />;
  return (
    <>
      <SettingsSection title="General settings" flat>
        <p className="rw-rail-body">Workspace overview and links to platform configuration.</p>
        <SettingsPanel>
          <KeyValueTable
            items={[
              { label: "Clusters", value: String(list(data, "clusters").length) },
              { label: "Dashboard URL", value: config?.spec?.dashboardURL || "Not configured" },
              { label: "HA nodes", value: String(data?.platform?.healthyNodes ?? 0) },
            ]}
          />
        </SettingsPanel>
      </SettingsSection>
      <SettingsDivider />
      <SettingsSection title="Platform" flat>
        <SettingsPanel className="rw-settings-link-list">
          {visibleSettingsLinks(SETTINGS_LINKS, hidden).filter((item) => item.id !== "general" && item.id !== "usage").map((item) => (
            <AppLink key={item.id} href={item.to} className="rw-settings-platform-link">
              {item.label}
            </AppLink>
          ))}
        </SettingsPanel>
      </SettingsSection>
    </>
  );
}
