import { useEffect, useMemo, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { action, api } from "@/lib/api";
import { canMutate, useBootstrap } from "@/lib/dashboard";
import { alertUser, alertError } from "@/lib/notify";
import { AppLink } from "@/components/AppLink";
import { ManagedArea } from "@/components/ManagedNotice";
import { managedFor } from "@/lib/managed";
import { Badge, Button, Field, FormStack, Input, Muted, Select } from "@/components/geass-ui";
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

/** What the Let's Encrypt issuer is doing, in the words and tones the page uses. */
export const CERTIFICATE_STATES = {
  missing: { label: "Not set", tone: "warning" },
  waiting: { label: "Waiting", tone: "warning" },
  ready: { label: "Ready", tone: "success" },
  failing: { label: "Not working", tone: "danger" },
};

/**
 * The contact address of the cluster's Let's Encrypt account. Without it Geass creates no certificate issuer, so no
 * domain gets an HTTPS certificate, the dashboard's included. The installer sets it from GEASS_ACME_EMAIL.
 */
export function CertificateEmailPanel({ mutable }) {
  const queryClient = useQueryClient();
  const settings = useQuery({
    queryKey: ["certificate-settings"],
    queryFn: () => api("/api/settings/certificates"),
    staleTime: 0,
    refetchOnMount: "always",
    refetchInterval: (query) => (query.state.data?.state === "waiting" ? 5_000 : 60_000),
  });
  const [email, setEmail] = useState(null);
  const [saving, setSaving] = useState(false);

  if (settings.isLoading || settings.isError) return null;
  const saved = settings.data || {};
  const state = CERTIFICATE_STATES[saved.state] || CERTIFICATE_STATES.missing;
  const value = email ?? saved.email ?? "";

  const save = async (event) => {
    event.preventDefault();
    setSaving(true);
    try {
      await action("/settings/domain/acme-email", { email: value.trim() });
      await queryClient.invalidateQueries({ queryKey: ["certificate-settings"] });
      setEmail(null);
      alertUser("Email saved");
    } catch (error) {
      alertError(error.message);
    } finally {
      setSaving(false);
    }
  };

  return (
    <SettingsPanel>
      <div className="rw-domain-quick-head">
        <p className="rw-settings-subtitle">Certificate email</p>
        <Badge tone={state.tone}>{state.label}</Badge>
      </div>
      {saved.message ? <Muted size="sm">{saved.message}</Muted> : null}
      <FormStack className="rw-rail-form" onSubmit={save}>
        <Field label="Email">
          <Input
            type="email"
            required
            value={value}
            onChange={(event) => setEmail(event.target.value)}
            placeholder="you@yourdomain.com"
            disabled={!mutable}
          />
        </Field>
        <SettingsFormActions>
          <Button type="submit" disabled={!mutable || saving || !value.trim() || value.trim() === saved.email}>
            Save email
          </Button>
        </SettingsFormActions>
      </FormStack>
    </SettingsPanel>
  );
}

export function DomainSettingsPage({
  config,
  reload,
  platformSummary,
  cloudflareInfo = {},
  cloudflareLoading = false,
}) {
  const { data } = useBootstrap();
  // The platform that runs this Geass keeps the dashboard's domain: it is shown as it is, and cannot be changed.
  const managed = managedFor(data, "dashboardDomain");
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
      alertUser("Domain saved");
    } catch (error) {
      alertError(error.message);
    } finally {
      setSaving(false);
    }
  };

  const quickApply = (event) => {
    event.preventDefault();
    if (!cloudflareRoot) {
      alertUser("Enter the zone apex domain");
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
      <ManagedArea managed={managed}>
      <CertificateEmailPanel mutable={canMutate(data)} />
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
                Could not read the zone name from Cloudflare. Enter the apex domain below, or{" "}
                <AppLink href="/settings/connectors">reconnect Cloudflare</AppLink>.
              </p>
            ) : null}
            {!isActiveDomain ? (
              <div className="rw-domain-quick-actions">
                <Button
                  type="button"
                  disabled={!canMutate(data) || saving || !previewURL}
                  onClick={quickApply}
                >
                  {previewURL ? `Use ${previewURL}` : "Set dashboard domain"}
                </Button>
              </div>
            ) : null}
          </SettingsPanel>

          <details className="rw-domain-customize" open={needsApexInput}>
            <summary>Customize zone or subdomain</summary>
            <FormStack className="rw-rail-form" onSubmit={quickApply}>
              {needsApexInput || !resolvedApex ? (
                <Field label="Zone apex domain">
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
                  description={zones.length > 1 ? undefined : "Only one zone on this token."}
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
              <Field label="Subdomain">
                <Input
                  value={subdomain}
                  onChange={(event) => setSubdomain(event.target.value)}
                  placeholder={DEFAULT_SUBDOMAIN}
                />
              </Field>
              <Field label="Publish via">
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
            <p className="rw-rail-body rw-domain-manual-lead">DNS must point to your cluster ingress.</p>
            {previewURL ? (
              <p className="rw-domain-manual-preview">
                Dashboard URL: <strong>{previewURL}</strong>
              </p>
            ) : null}
            <FormStack className="rw-rail-form" onSubmit={saveManual}>
              <Field label="Root domain" description="Apex only.">
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
      </ManagedArea>
    </SettingsSection>
  );
}
