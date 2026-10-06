import { useEffect, useMemo, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { action } from "@/lib/api";
import { canMutate, useBootstrap } from "@/lib/dashboard";
import { alertUser } from "@/lib/notify";
import { AppLink } from "@/components/AppLink";
import { Button, Field, FormStack, Input, Select } from "@/components/geass-ui";
import { SettingsFormActions, SettingsPanel, SettingsSection } from "@/layout/SettingsShell";

const DEFAULT_SUBDOMAIN = "geass";

function hostFromDashboardURL(url) {
  const raw = String(url || "").trim();
  if (!raw) return "";
  try {
    const host = new URL(raw.includes("://") ? raw : `https://${raw}`).hostname;
    return host.toLowerCase();
  } catch {
    return "";
  }
}

function apexFromDashboardHost(host, subdomain = DEFAULT_SUBDOMAIN) {
  const h = String(host || "").trim().toLowerCase();
  const prefix = `${subdomain}.`;
  if (h.startsWith(prefix)) return h.slice(prefix.length);
  const dot = h.indexOf(".");
  return dot > 0 ? h.slice(dot + 1) : "";
}

function rootFromDashboardURL(url, subdomain = DEFAULT_SUBDOMAIN) {
  return apexFromDashboardHost(hostFromDashboardURL(url), subdomain);
}

function subdomainFromConfig(config) {
  const root = String(config?.spec?.rootDomain || "").trim().toLowerCase();
  const host = hostFromDashboardURL(config?.spec?.dashboardURL);
  if (root && host && host.endsWith(`.${root}`)) {
    const label = host.slice(0, -(root.length + 1));
    if (label) return label;
  }
  return DEFAULT_SUBDOMAIN;
}

function dashboardHost(subdomain, rootDomain) {
  const apex = String(rootDomain || "").trim().toLowerCase();
  const label = String(subdomain || DEFAULT_SUBDOMAIN).trim().toLowerCase() || DEFAULT_SUBDOMAIN;
  return apex ? `${label}.${apex}` : "";
}

function normalizeSubdomainInput(value) {
  const label = String(value || "").trim().toLowerCase();
  if (!label) return DEFAULT_SUBDOMAIN;
  if (!/^[a-z0-9]([a-z0-9-]*[a-z0-9])?$/.test(label) || label.length > 63) return "";
  return label;
}

export function DomainSettingsPage({
  config,
  reload,
  platformSummary,
  cloudflareInfo = {},
  cloudflareLoading = false,
}) {
  const { data } = useBootstrap();
  const queryClient = useQueryClient();
  const cloudflareConnected = Boolean(cloudflareInfo?.connected ?? data?.platform?.hasCloudflare);

  const platformZoneId =
    platformSummary?.cloudflareZoneId || data?.platform?.cloudflareZoneId || "";
  const platformZoneName =
    platformSummary?.cloudflareZoneName || data?.platform?.cloudflareZoneName || "";

  const connectedZoneId =
    cloudflareInfo?.zoneId || platformZoneId || config?.spec?.cloudflareZoneID || "";

  const zones = useMemo(() => {
    const fromApi = cloudflareInfo?.zones || [];
    if (fromApi.length > 0) return fromApi;
    const zoneName =
      cloudflareInfo?.zoneName ||
      platformZoneName ||
      cloudflareInfo?.suggestedRootDomain ||
      config?.spec?.rootDomain ||
      "";
    if (cloudflareConnected && zoneName) {
      return [{ id: connectedZoneId || zoneName, name: zoneName }];
    }
    return [];
  }, [
    cloudflareInfo?.zones,
    cloudflareInfo?.zoneName,
    cloudflareInfo?.suggestedRootDomain,
    cloudflareConnected,
    connectedZoneId,
    config?.spec?.rootDomain,
    platformZoneName,
  ]);

  const defaultZone = useMemo(() => {
    if (!zones.length) return null;
    return zones.find((z) => z.id === connectedZoneId) || zones[0];
  }, [zones, connectedZoneId]);

  const savedRoot = String(config?.spec?.rootDomain || "").trim().toLowerCase();
  const savedURL = String(config?.spec?.dashboardURL || "").trim();
  const savedHost = hostFromDashboardURL(savedURL);

  const [cloudflareZoneId, setCloudflareZoneId] = useState(defaultZone?.id || "");
  const [subdomain, setSubdomain] = useState(() => subdomainFromConfig(config));
  const [manualRoot, setManualRoot] = useState(savedRoot || "");
  const [apexOverride, setApexOverride] = useState("");
  const [useManual, setUseManual] = useState(!cloudflareConnected);
  const [exposure, setExposure] = useState(
    () => config?.spec?.dashboardExposure || "ingress",
  );
  const [saving, setSaving] = useState(false);

  const selectedZone =
    zones.find((z) => z.id === cloudflareZoneId) ||
    zones.find((z) => z.id === connectedZoneId) ||
    defaultZone ||
    zones[0] ||
    null;

  // Zone apex is written to the platform secret when Cloudflare is connected — not re-fetched from Cloudflare here.
  const resolvedApex = useMemo(() => {
    const candidates = [
      selectedZone?.name,
      cloudflareInfo?.zoneName,
      platformZoneName,
      config?.spec?.rootDomain,
    ];
    for (const value of candidates) {
      const text = String(value || "").trim().toLowerCase();
      if (text) return text;
    }
    return "";
  }, [cloudflareInfo?.zoneName, platformZoneName, selectedZone?.name, config?.spec?.rootDomain]);

  const cloudflareRoot = (resolvedApex || apexOverride).trim().toLowerCase();
  const needsApexInput = cloudflareConnected && !useManual && !resolvedApex && !cloudflareLoading;

  const activeRoot = useManual ? manualRoot.trim().toLowerCase() : cloudflareRoot;
  const previewHost = dashboardHost(subdomain, activeRoot);
  const previewURL = previewHost ? `https://${previewHost}` : "";

  const savedExposure = config?.spec?.dashboardExposure || "ingress";

  const isActiveDomain =
    Boolean(savedRoot && savedHost) &&
    savedHost === previewHost &&
    savedRoot === activeRoot &&
    (!cloudflareConnected || useManual || savedExposure === exposure);

  useEffect(() => {
    if (defaultZone?.id) setCloudflareZoneId(defaultZone.id);
  }, [defaultZone?.id]);

  useEffect(() => {
    if (savedRoot) setManualRoot(savedRoot);
  }, [savedRoot]);

  useEffect(() => {
    if (cloudflareConnected && !savedRoot && defaultZone?.name) {
      setUseManual(false);
    }
  }, [cloudflareConnected, savedRoot, defaultZone?.name]);

  useEffect(() => {
    if (config?.spec?.dashboardExposure) {
      setExposure(config.spec.dashboardExposure);
    }
  }, [config?.spec?.dashboardExposure]);

  const persistDomain = async ({ root, zoneId, viaCloudflare, exposureMode = "ingress" }) => {
    const apex = String(root || "").trim().toLowerCase();
    const label = normalizeSubdomainInput(subdomain);
    if (!apex) {
      alertUser("Enter a root domain");
      return;
    }
    if (!label) {
      alertUser("Enter a valid subdomain (letters, numbers, hyphens)");
      return;
    }
    setSaving(true);
    try {
      const payload = {
        domain: apex,
        subdomain: label,
        exposure: viaCloudflare ? exposureMode : "ingress",
        tunnelCNAMETarget: "",
      };
      if (viaCloudflare && zoneId) payload.cloudflareZoneId = zoneId;
      await action("/settings/domain/save", payload);
      reload();
      queryClient.invalidateQueries({ queryKey: ["dashboard", "bootstrap"] });
      queryClient.invalidateQueries({ queryKey: ["cloudflare-settings"] });
      alertUser("Dashboard domain saved");
    } catch (error) {
      alertUser(error.message);
    } finally {
      setSaving(false);
    }
  };

  const quickApply = (event) => {
    event.preventDefault();
    if (!cloudflareRoot) {
      alertUser("Enter your zone apex domain (e.g. example.com)");
      return;
    }
    persistDomain({
      root: cloudflareRoot,
      zoneId: selectedZone?.id || connectedZoneId,
      viaCloudflare: true,
      exposureMode: exposure,
    });
  };

  const saveManual = (event) => {
    event.preventDefault();
    persistDomain({ root: manualRoot, viaCloudflare: false });
  };

  const showZonePicker = zones.length > 0;

  return (
    <SettingsSection title="Domain" flat>
      {cloudflareConnected && !useManual ? (
        <>
          <SettingsPanel className="rw-domain-quick">
            <div className="rw-domain-quick-head">
              <span className="rw-integration-status is-connected">Cloudflare connected</span>
              {cloudflareRoot ? (
                <span className="rw-domain-cf-at">
                  Zone <strong>{cloudflareRoot}</strong>
                </span>
              ) : null}
            </div>
            <p className="rw-domain-dashboard-hero-label">Dashboard URL</p>
            <p className="rw-domain-dashboard-hero-url">
              {cloudflareLoading && !previewURL ? "Loading…" : previewURL || "—"}
            </p>
            {isActiveDomain ? (
              <p className="rw-domain-active-badge">This is your active dashboard domain</p>
            ) : needsApexInput ? (
              <p className="rw-domain-quick-hint">
                We could not read your zone name from Cloudflare. Enter the apex domain below, or{" "}
                <AppLink href="/settings/connectors">reconnect Cloudflare</AppLink>.
              </p>
            ) : (
              <p className="rw-domain-quick-hint">
                One click saves this URL. Geass creates DNS in Cloudflare using your connected
                token—either an A record to cluster ingress or a Cloudflare Tunnel.
              </p>
            )}
            <div className="rw-domain-quick-actions">
              <Button
                type="button"
                disabled={!canMutate(data) || saving || !previewURL || isActiveDomain}
                onClick={quickApply}
              >
                {isActiveDomain ? "Dashboard domain set" : previewURL ? `Use ${previewURL}` : "Set dashboard domain"}
              </Button>
            </div>
          </SettingsPanel>

          <details className="rw-domain-customize" open={needsApexInput}>
            <summary>Customize zone or subdomain</summary>
            <FormStack className="rw-rail-form" onSubmit={quickApply}>
              {needsApexInput || !resolvedApex ? (
                <Field label="Zone apex domain" description="Your Cloudflare zone, e.g. example.com">
                  <Input
                    required
                    value={apexOverride}
                    onChange={(event) => setApexOverride(event.target.value)}
                    placeholder="example.com"
                  />
                </Field>
              ) : null}
              {showZonePicker ? (
                <Field
                  label="Cloudflare zone"
                  description={
                    zones.length > 1
                      ? "Zones your API token can read."
                      : "Only one zone on this token. Reconnect Cloudflare with broader zone access to see more."
                  }
                >
                  <Select
                    required
                    value={cloudflareZoneId || zones[0]?.id || ""}
                    onChange={(event) => setCloudflareZoneId(event.target.value)}
                  >
                    {zones.map((zone) => (
                      <option key={zone.id} value={zone.id}>{zone.name}</option>
                    ))}
                  </Select>
                </Field>
              ) : null}
              <Field
                label="Subdomain"
                description={`Host becomes ${dashboardHost(subdomain, cloudflareRoot || "example.com")}`}
              >
                <Input
                  value={subdomain}
                  onChange={(event) => setSubdomain(event.target.value)}
                  placeholder={DEFAULT_SUBDOMAIN}
                />
              </Field>
              <Field
                label="Publish via"
                description="Both options use the Cloudflare API with your connected token."
              >
                <Select
                  value={exposure}
                  onChange={(event) => setExposure(event.target.value)}
                >
                  <option value="ingress">DNS to cluster (proxied A record)</option>
                  <option value="cloudflare-tunnel">Cloudflare Tunnel</option>
                </Select>
              </Field>
              <SettingsFormActions>
                <Button type="submit" disabled={!canMutate(data) || saving || !previewURL}>
                  Save dashboard domain
                </Button>
              </SettingsFormActions>
            </FormStack>
          </details>

          <p className="rw-rail-body rw-domain-switch-path">
            <button type="button" className="rw-text-link" onClick={() => setUseManual(true)}>
              Use a different domain without Cloudflare
            </button>
          </p>
        </>
      ) : (
        <>
          {!cloudflareConnected ? (
            <SettingsPanel className="rw-domain-connect-cta">
              <p className="rw-settings-subtitle">Connect Cloudflare</p>
              <p className="rw-rail-body">
                Pick a zone from your token and set <strong>geass.yourdomain.com</strong> in one step.
              </p>
              <AppLink href="/settings/connectors" className="rw-domain-connect-btn">
                Connect Cloudflare
              </AppLink>
            </SettingsPanel>
          ) : (
            <p className="rw-rail-body">
              <button type="button" className="rw-text-link" onClick={() => setUseManual(false)}>
                Back to Cloudflare zones
              </button>
            </p>
          )}

          <SettingsPanel>
            <p className="rw-settings-subtitle">Manual domain</p>
            <p className="rw-rail-body rw-domain-manual-lead">
              {cloudflareConnected
                ? "Use any apex domain you control. DNS must point to your cluster ingress."
                : "Or enter your apex domain without Cloudflare."}
            </p>
            {previewURL ? (
              <p className="rw-domain-manual-preview">
                Dashboard URL: <strong>{previewURL}</strong>
              </p>
            ) : null}
            <FormStack className="rw-rail-form" onSubmit={saveManual}>
              <Field label="Root domain" description="Apex only, e.g. example.com">
                <Input
                  required
                  value={manualRoot}
                  onChange={(event) => setManualRoot(event.target.value)}
                  placeholder="example.com"
                />
              </Field>
              <Field label="Subdomain">
                <Input
                  value={subdomain}
                  onChange={(event) => setSubdomain(event.target.value)}
                  placeholder={DEFAULT_SUBDOMAIN}
                />
              </Field>
              <SettingsFormActions>
                <Button type="submit" disabled={!canMutate(data) || saving}>
                  Save dashboard domain
                </Button>
              </SettingsFormActions>
            </FormStack>
          </SettingsPanel>
        </>
      )}
    </SettingsSection>
  );
}
