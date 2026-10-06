import { useEffect, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { action } from "@/lib/api";
import { canMutate, useBootstrap } from "@/lib/dashboard";
import { alertUser } from "@/lib/notify";
import { Button, Field, FormStack, Input, KeyValueTable, Select } from "@/components/geass-ui";
import { SettingsDivider, SettingsFormActions, SettingsPanel } from "@/layout/SettingsShell";

const CF_CREATE_TOKEN_URL = "https://dash.cloudflare.com/profile/api-tokens";

function zoneAccountId(zone) {
  return zone?.account?.id || zone?.accountId || "";
}

function resolveCloudflareSelection({ accounts = [], zones = [], accountId = "", zoneId = "" }) {
  let nextZoneId = zoneId || (zones.length === 1 ? zones[0].id : "");
  let nextAccountId = accountId || accounts[0]?.id || "";
  const zone = zones.find((entry) => entry.id === nextZoneId) || (zones.length === 1 ? zones[0] : null);
  if (!nextAccountId && zone) {
    nextAccountId = zoneAccountId(zone);
  }
  return { accountId: nextAccountId, zoneId: nextZoneId, zones };
}

function shortId(value) {
  const text = String(value || "").trim();
  if (text.length <= 14) return text;
  return `${text.slice(0, 8)}…${text.slice(-4)}`;
}

export function CloudflareConnectorPanel({ reload, info, inSheet = false }) {
  const { data } = useBootstrap();
  const queryClient = useQueryClient();
  const connected = Boolean(info?.connected);
  const ready = Boolean(info?.ready);
  const [values, setValues] = useState({
    apiToken: "",
    accountId: info?.accountId || "",
    zoneId: info?.zoneId || "",
  });
  const [zones, setZones] = useState([]);
  const [saving, setSaving] = useState(false);
  const [refreshing, setRefreshing] = useState(false);
  const [confirm, setConfirm] = useState("");
  const [editing, setEditing] = useState(false);

  const zoneDomain =
    info?.zoneName ||
    data?.platformConfig?.items?.[0]?.spec?.rootDomain ||
    zones.find((z) => z.id === (info?.zoneId || values.zoneId))?.name ||
    "";

  useEffect(() => {
    if (info?.accountId) setValues((prev) => ({ ...prev, accountId: info.accountId }));
    if (info?.zoneId) setValues((prev) => ({ ...prev, zoneId: info.zoneId }));
  }, [info?.accountId, info?.zoneId]);

  useEffect(() => {
    if (info?.zones?.length) setZones(info.zones);
  }, [info?.zones]);

  useEffect(() => {
    if (!connected) setEditing(false);
  }, [connected]);

  const discover = async (token) => {
    const result = await action("/settings/cloudflare/discover", { apiToken: token });
    const nextZones = result.zones || [];
    const nextAccounts = result.accounts || [];
    setZones(nextZones);
    return resolveCloudflareSelection({
      accounts: nextAccounts,
      zones: nextZones,
      accountId: values.accountId,
      zoneId: values.zoneId,
    });
  };

  const submit = async (event) => {
    event.preventDefault();
    const token = values.apiToken.trim();
    if (!connected && !token) {
      alertUser("Paste your Cloudflare API token");
      return;
    }
    if (connected && !token && !editing) {
      return;
    }
    setSaving(true);
    try {
      let accountId = values.accountId;
      let zoneId = values.zoneId;
      let zonesForSave = zones;
      if (token) {
        const found = await discover(token);
        zonesForSave = found.zones || [];
        setZones(zonesForSave);
        ({ accountId, zoneId } = resolveCloudflareSelection({
          zones: zonesForSave,
          accountId: values.accountId || found.accountId,
          zoneId: values.zoneId || found.zoneId,
        }));
        if (!zoneId && found.zones.length > 1) {
          alertUser("Choose a DNS zone");
          setSaving(false);
          return;
        }
        if (!zoneId && found.zones.length === 0) {
          alertUser("No zones on this token — add Zone read permission or enter zone ID");
          setSaving(false);
          return;
        }
      }
      if (!accountId || !zoneId) {
        alertUser("Account and zone are required — pick a DNS zone if more than one is listed");
        setSaving(false);
        return;
      }
      const zoneName =
        zonesForSave.find((z) => z.id === zoneId)?.name ||
        zones.find((z) => z.id === zoneId)?.name ||
        (zoneId === info?.zoneId ? info?.zoneName : "") ||
        "";
      if (!zoneName) {
        alertUser("DNS zone name is missing — pick a zone from the list or reconnect");
        setSaving(false);
        return;
      }
      const payload = { accountId, zoneId, zoneName };
      if (zonesForSave.length > 0) {
        payload.zonesJson = JSON.stringify(
          zonesForSave.map((zone) => ({ id: zone.id, name: zone.name })),
        );
      }
      if (token) payload.apiToken = token;
      else if (connected) payload.keepExisting = "true";
      await action("/settings/cloudflare/save", payload);
      reload();
      queryClient.invalidateQueries({ queryKey: ["cloudflare-settings"] });
      queryClient.invalidateQueries({ queryKey: ["dashboard", "bootstrap"] });
      setValues((prev) => ({ ...prev, apiToken: "" }));
      setEditing(false);
      alertUser(connected ? "Cloudflare updated" : "Cloudflare connected");
    } catch (error) {
      alertUser(error.message);
    } finally {
      setSaving(false);
    }
  };

  const showZoneSelect = zones.length > 0;
  const connectionStatus = !connected ? "disconnected" : ready ? "connected" : "pending";

  const refreshConnection = async () => {
    setRefreshing(true);
    try {
      const result = await action("/settings/cloudflare/refresh", {});
      reload();
      queryClient.invalidateQueries({ queryKey: ["cloudflare-settings"] });
      queryClient.invalidateQueries({ queryKey: ["dashboard", "bootstrap"] });
      if (result?.warning) {
        alertUser(result.warning);
        return;
      }
      const count = result?.zones;
      alertUser(
        typeof count === "number" && count > 0
          ? `Cloudflare refreshed — ${count} zone${count === 1 ? "" : "s"} available`
          : "Cloudflare connection refreshed",
      );
    } catch (error) {
      alertUser(error.message);
    } finally {
      setRefreshing(false);
    }
  };

  const summaryItems = [{ label: "Status", value: ready ? "Connected — tunnel active" : "Pending — finishing setup" }];
  if (zoneDomain) {
    summaryItems.push({ label: "DNS zone", value: zoneDomain });
  }
  if (info?.zoneId) {
    summaryItems.push({ label: "Zone ID", value: info.zoneId });
  }
  if (info?.tunnelId) {
    summaryItems.push({ label: "Tunnel ID", value: info.tunnelId });
  }

  const setupForm = (
    <FormStack className="rw-rail-form rw-cf-form" onSubmit={submit}>
      <Field
        label="API token"
        description={
          connected
            ? "Paste a new token only when rotating credentials."
            : "Copy it from Cloudflare right after you create the token."
        }
      >
        <Input
          type="password"
          value={values.apiToken}
          onChange={(event) => setValues({ ...values, apiToken: event.target.value })}
          autoComplete="off"
          placeholder="Paste token from Cloudflare"
        />
      </Field>

      {!connected && (
        <div className="rw-form-actions rw-cf-form-lead">
          <Button type="button" variant="outline" component="a" href={CF_CREATE_TOKEN_URL} target="_blank" rel="noopener noreferrer">
            Open Cloudflare to create token
          </Button>
        </div>
      )}

      {showZoneSelect ? (
        <Field label="DNS zone">
          <Select
            required
            value={values.zoneId}
            onChange={(event) => {
              const nextZoneId = event.target.value;
              const zone = zones.find((entry) => entry.id === nextZoneId);
              setValues({
                ...values,
                zoneId: nextZoneId,
                accountId: zoneAccountId(zone) || values.accountId,
              });
            }}
          >
            <option value="">Select zone</option>
            {zones.map((zone) => (
              <option key={zone.id} value={zone.id}>{zone.name}</option>
            ))}
          </Select>
        </Field>
      ) : connected && editing && values.zoneId ? (
        <Field label="DNS zone" description="Enter a new token above to change zone.">
          <Input readOnly value={zoneDomain || shortId(values.zoneId)} />
        </Field>
      ) : null}

      <SettingsFormActions>
        {connected && editing ? (
          <>
            <Button type="button" variant="outline" onClick={() => { setEditing(false); setValues((prev) => ({ ...prev, apiToken: "" })); }}>
              Cancel
            </Button>
            <Button type="submit" disabled={!canMutate(data) || saving || !values.apiToken.trim()}>
              {saving ? "Saving…" : "Save changes"}
            </Button>
          </>
        ) : (
          <Button type="submit" disabled={!canMutate(data) || saving}>
            {saving ? "Connecting…" : "Connect"}
          </Button>
        )}
      </SettingsFormActions>
    </FormStack>
  );

  return (
    <div className={inSheet ? "rw-settings-sheet-panel" : undefined}>
      {connected ? (
        <>
          <div className={`rw-cf-connected-banner is-${connectionStatus}`}>
            <span className={`rw-integration-status is-${connectionStatus}`}>
              {ready ? "Connected" : "Pending"}
            </span>
            <p className="rw-cf-connected-lead">
              {ready
                ? "Geass runs cloudflared in your cluster and manages tunnel routes and DNS for this zone."
                : "Credentials are saved. The controller is creating the tunnel and DNS records."}
            </p>
          </div>

          <SettingsPanel className="rw-cf-summary">
            <KeyValueTable items={summaryItems} />
          </SettingsPanel>

          {!editing ? (
            <div className="rw-cf-actions">
              <Button
                type="button"
                variant="outline"
                disabled={!canMutate(data) || refreshing}
                onClick={refreshConnection}
              >
                {refreshing ? "Refreshing…" : "Refresh zones"}
              </Button>
              <Button type="button" variant="outline" disabled={!canMutate(data)} onClick={() => setEditing(true)}>
                Update API token
              </Button>
              <Button
                type="button"
                variant="outline"
                component="a"
                href={CF_CREATE_TOKEN_URL}
                target="_blank"
                rel="noopener noreferrer"
              >
                Open Cloudflare dashboard
              </Button>
            </div>
          ) : (
            <div className="rw-cf-update-block">
              <h3 className="rw-settings-subtitle">Update credentials</h3>
              <p className="rw-settings-section-desc">Paste a new API token with the same permissions. We will re-discover your zone.</p>
              {setupForm}
            </div>
          )}
        </>
      ) : (
        <>
          <p className="rw-cf-intro">
            Connect Cloudflare so Geass can run cloudflared in the cluster and manage DNS for your domain.
          </p>
          <ol className="rw-cf-steps">
            <li>
              Create a custom API token (not the Global API Key): Account → Cloudflare Tunnel → Edit; Zone → DNS → Edit; Zone → Zone
              → Read — all for the domain you use with Geass.
            </li>
            <li>Paste the token below and click Connect — we pick your DNS zone automatically when possible.</li>
          </ol>
          {setupForm}
        </>
      )}

      {connected && canMutate(data) && (
        <>
          <SettingsDivider />
          <SettingsPanel className="rw-cf-disconnect">
            <h3 className="rw-settings-subtitle">Disconnect Cloudflare</h3>
            <p className="rw-settings-section-desc">
              Stops the in-cluster cloudflared deployment and automatic DNS updates. Your Cloudflare account and tunnel are not deleted.
            </p>
            <FormStack className="rw-rail-form rw-cf-disconnect-form">
              <Field label="Confirmation" description='Type "remove-cloudflare" to enable disconnect.'>
                <Input value={confirm} onChange={(event) => setConfirm(event.target.value)} autoComplete="off" placeholder="remove-cloudflare" />
              </Field>
              <SettingsFormActions>
                <Button
                  type="button"
                  variant="danger"
                  disabled={confirm !== "remove-cloudflare"}
                  onClick={() =>
                    action("/settings/cloudflare/clear")
                      .then(() => {
                        reload();
                        queryClient.invalidateQueries({ queryKey: ["cloudflare-settings"] });
                        queryClient.invalidateQueries({ queryKey: ["dashboard", "bootstrap"] });
                        setConfirm("");
                        setEditing(false);
                        alertUser("Cloudflare removed");
                      })
                      .catch((error) => alertUser(error.message))
                  }
                >
                  Disconnect
                </Button>
              </SettingsFormActions>
            </FormStack>
          </SettingsPanel>
        </>
      )}
    </div>
  );
}
