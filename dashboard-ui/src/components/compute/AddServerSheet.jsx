import { useEffect, useMemo, useState } from "react";
import { Link } from "@tanstack/react-router";
import { action } from "@/lib/api";
import { notify } from "@/lib/notify";
import { parseTagList } from "@/lib/placement";
import {
  formatPrice,
  isMachineConnection,
  missingSettings,
  nameProblem,
  providerLabel,
  providerOf,
  settingsPayload,
  useConnections,
  useDebounced,
  useProviders,
  useRefreshCompute,
  useSizes,
} from "@/lib/compute";
import { Button, Field, Input, Muted, Select, Textarea } from "@/components/geass-ui";
import { Alert, Callout, Choice, Optional, Required } from "@/components/compute/ComputeBits";
import {
  PlacementFields,
  ProviderSettingsFields,
  SizePicker,
} from "@/components/compute/ComputeFields";
import { SettingsFormActions, SettingsSheet } from "@/layout/SettingsShell";

/**
 * Adds one server to the cluster: a machine Geass creates at a cloud, or a server you already have, which Geass reaches over
 * SSH. A server added this way is its own: it can be removed again, unlike the nodes of a pool.
 */
function AddServerForm({ providers, connections, onDone, onCancel }) {
  const refresh = useRefreshCompute();
  const machineConnections = useMemo(() => connections.filter(isMachineConnection), [connections]);
  const [source, setSource] = useState("cloud");
  const [name, setName] = useState("");
  const [connection, setConnection] = useState("");
  const [region, setRegion] = useState("");
  const [size, setSize] = useState("");
  const [image, setImage] = useState("");
  const [settingValues, setSettingValues] = useState({});
  const [settingRows, setSettingRows] = useState([]);
  const [address, setAddress] = useState("");
  const [sshUser, setSshUser] = useState("root");
  const [sshPort, setSshPort] = useState("22");
  const [sshKey, setSshKey] = useState("");
  const [knownHosts, setKnownHosts] = useState("");
  const [storage, setStorage] = useState("Allow");
  const [placement, setPlacement] = useState({ region: "", zone: "", tags: "", reserved: [] });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    if (connection || !machineConnections.length) return;
    setConnection((machineConnections.find((item) => item.available) || machineConnections[0]).name);
  }, [connection, machineConnections]);
  useEffect(() => {
    setSettingValues({});
    setSettingRows([]);
  }, [connection]);

  const connectionInfo = connections.find((item) => item.name === connection);
  const descriptor = providerOf(providers, connectionInfo?.provider);
  const providerName = providerLabel(providers, connectionInfo?.provider);
  const settledRegion = useDebounced(region.trim());
  const sizes = useSizes(source === "cloud" ? connection : "", settledRegion);
  const sizeInfo = (sizes.data || []).find((item) => item.id === size.trim());

  const submit = async (event) => {
    event.preventDefault();
    setError("");
    const trimmed = name.trim();
    let problem = "";
    if (source === "existing" || trimmed) problem = nameProblem(trimmed, "server", 55);
    if (!problem && source === "cloud") {
      if (!connection) problem = "Choose the cloud connection that creates the machine.";
      else if (!size.trim()) problem = "Give the size of the machine to create.";
      else if (missingSettings(descriptor, settingValues).length) {
        problem = `${providerName} needs ${missingSettings(descriptor, settingValues).map((setting) => setting.label).join(", ")}.`;
      }
    }
    if (!problem && source === "existing") {
      if (!address.trim()) problem = "Give the address Geass reaches the server at.";
      else if (!sshUser.trim()) problem = "Give an SSH user that is root or can use sudo without a password.";
      else if (sshPort.trim() && !/^\d+$/.test(sshPort.trim())) problem = "The SSH port is a number.";
      else if (!sshKey.includes("PRIVATE KEY")) problem = "Paste the SSH private key, with its BEGIN and END lines.";
    }
    if (problem) {
      setError(problem);
      return;
    }
    const body = { source, storage };
    if (trimmed) body.name = trimmed;
    if (source === "cloud") {
      Object.assign(body, {
        connection,
        size: size.trim(),
        region: region.trim(),
        image: image.trim(),
        settings: settingsPayload(descriptor, settingValues, settingRows),
      });
    } else {
      Object.assign(body, { address: address.trim(), sshUser: sshUser.trim(), sshPort: sshPort.trim() || "22", sshPrivateKey: sshKey });
      if (knownHosts.trim()) body.knownHosts = knownHosts;
    }
    const tags = parseTagList(placement.tags);
    if (placement.region.trim() || placement.zone.trim() || tags.length) {
      body.placementRegion = placement.region.trim();
      body.placementZone = placement.zone.trim();
      body.tags = tags.join(",");
      body.reservedTags = placement.reserved.filter((tag) => tags.includes(tag)).join(",");
    }
    setBusy(true);
    try {
      const result = await action("/servers/create", body);
      await refresh();
      notify(source === "cloud" ? `Creating the server ${result?.name || ""} at ${providerName}. It joins the cluster when it is ready.` : `Adding the server ${result?.name || trimmed}. It joins the cluster when Geass has set it up.`, "success");
      onDone();
    } catch (failure) {
      setError(failure.message);
    } finally {
      setBusy(false);
    }
  };

  const price = Number(sizeInfo?.hourlyPrice);

  return (
    <form className="cp-form" onSubmit={submit}>
      <fieldset className="cp-lock" disabled={busy}>
        <Field label="Where does the server come from?">
          <div className="cp-choices" role="group" aria-label="Where the server comes from">
            <Choice
              selected={source === "cloud"}
              onClick={() => setSource("cloud")}
              title="A cloud machine"
              description="Geass creates one machine at a cloud and joins it to the cluster."
            />
            <Choice
              selected={source === "existing"}
              onClick={() => setSource("existing")}
              title="A server I have"
              description="Geass reaches it over SSH and joins it to the cluster."
            />
          </div>
        </Field>

        <Field
          label={<>Name{source === "existing" ? <Required /> : <Optional />}</>}
          description={source === "cloud" ? "Lowercase letters, numbers and hyphens. Made up for you when left empty." : "Lowercase letters, numbers and hyphens. It is the node's name once it joins."}
        >
          <Input value={name} onChange={(event) => setName(event.currentTarget.value.toLowerCase())} maxLength={55} autoComplete="off" spellCheck={false} placeholder={source === "cloud" ? "server-x7k2m" : "storage-1"} aria-label="Server name" data-autofocus />
        </Field>

        {source === "cloud" ? (
          <>
            <Field label={<>Cloud connection<Required /></>}>
              {machineConnections.length ? (
                <Select value={connection} onChange={(event) => setConnection(event.currentTarget.value)} aria-label="Cloud connection">
                  {machineConnections.map((item) => (
                    <option key={item.name} value={item.name}>
                      {item.name} · {providerLabel(providers, item.provider)}{item.available ? "" : " (not available)"}
                    </option>
                  ))}
                </Select>
              ) : (
                <Callout>
                  No cloud is connected yet. <Link to="/settings/connectors">Connect a cloud</Link> in Connectors first, or add a server you have.
                </Callout>
              )}
            </Field>
            <Field label={<>Region<Optional /></>} description={descriptor?.regionHelp || "The cloud's own region or location."}>
              <Input value={region} onChange={(event) => setRegion(event.currentTarget.value)} placeholder={connectionInfo?.region || "Cloud's choice"} autoComplete="off" spellCheck={false} aria-label="Region" />
            </Field>
            <SizePicker inputId="cp-server-size" label="Machine size" required value={size} onChange={setSize} query={sizes} connection={connection} hint="The cloud's name for a size. Choose one from the list, or type it." />
            <Field label={<>Image<Optional /></>} description="The operating system image. Default: the cloud's Ubuntu 24.04.">
              <Input value={image} onChange={(event) => setImage(event.currentTarget.value)} autoComplete="off" spellCheck={false} aria-label="Image" />
            </Field>
            <ProviderSettingsFields descriptor={descriptor} values={settingValues} rows={settingRows} onValues={setSettingValues} onRows={setSettingRows} />
          </>
        ) : (
          <>
            <Field label={<>Address<Required /></>} description="Where Geass reaches the server over SSH.">
              <Input value={address} onChange={(event) => setAddress(event.currentTarget.value)} placeholder="198.51.100.10" autoComplete="off" spellCheck={false} aria-label="Address" />
            </Field>
            <div className="cp-row">
              <Field label={<>SSH user<Required /></>} description="A user that is root, or can use sudo without a password.">
                <Input value={sshUser} onChange={(event) => setSshUser(event.currentTarget.value)} autoComplete="off" spellCheck={false} aria-label="SSH user" />
              </Field>
              <Field label="SSH port">
                <Input value={sshPort} inputMode="numeric" onChange={(event) => setSshPort(event.currentTarget.value)} autoComplete="off" aria-label="SSH port" />
              </Field>
            </div>
            <Field label={<>SSH private key<Required /></>} description="Paste the whole key. It is stored in a Secret that only the Geass operator reads, and is never shown again.">
              <Textarea className="cp-textarea-mono" rows={6} value={sshKey} onChange={(event) => setSshKey(event.currentTarget.value)} placeholder={"-----BEGIN OPENSSH PRIVATE KEY-----"} autoComplete="off" spellCheck={false} aria-label="SSH private key" />
            </Field>
            <Field label={<>Host keys<Optional /></>} description="The server's host keys, in known_hosts form, to pin it. Without them Geass does not check which server it is talking to.">
              <Textarea className="cp-textarea-mono" rows={3} value={knownHosts} onChange={(event) => setKnownHosts(event.currentTarget.value)} autoComplete="off" spellCheck={false} aria-label="Host keys" />
            </Field>
          </>
        )}

        <Field label="Data on this server" description={storage === "Allow" ? "Allow (the default for a single server): databases, buckets and volumes may run on it." : "None keeps databases, buckets and volumes off it. Only services run on it."}>
          <Select value={storage} onChange={(event) => setStorage(event.currentTarget.value)} aria-label="Data on this server">
            <option value="Allow">Allow</option>
            <option value="None">None</option>
          </Select>
        </Field>

        <details className="cp-details">
          <summary>Placement</summary>
          <div className="cp-details-body">
            <p className="cp-fieldset-note">Where the node sits and what it is for. Left empty, it carries no region or tags.</p>
            <PlacementFields region={placement.region} zone={placement.zone} tags={placement.tags} reserved={placement.reserved} onChange={(patch) => setPlacement({ ...placement, ...patch })} />
          </div>
        </details>

        {source === "cloud" ? (
          <Callout tone="cost">
            <strong>This spends money.</strong> Geass creates one machine in your account at {providerName || "the cloud"}, and the cloud bills you for it
            {price > 0 ? <> at {formatPrice(price, sizeInfo.currency)}</> : null} until you remove the server.
          </Callout>
        ) : (
          <Callout>Nothing is bought. Geass installs what the cluster needs on the server over SSH and joins it to the cluster.</Callout>
        )}
        {error ? <Alert tone="danger">{error}</Alert> : null}
        <SettingsFormActions>
          <Button type="submit" loading={busy}>Add server</Button>
          <Button type="button" variant="outline" disabled={busy} onClick={onCancel}>Cancel</Button>
        </SettingsFormActions>
      </fieldset>
    </form>
  );
}

export function AddServerSheet({ opened, onClose }) {
  const providersQuery = useProviders();
  const connections = useConnections();
  const loading = providersQuery.isPending || connections.isPending;
  return (
    <SettingsSheet opened={opened} onClose={onClose} title="Add a server" description="Add one server to the cluster, on its own and not part of a pool. You can remove it again later.">
      {opened ? (
        loading ? (
          <Muted size="sm">Loading…</Muted>
        ) : providersQuery.isError ? (
          <Alert tone="danger">{providersQuery.error.message}</Alert>
        ) : (
          <AddServerForm providers={providersQuery.data || []} connections={connections.data || []} onDone={onClose} onCancel={onClose} />
        )
      ) : null}
    </SettingsSheet>
  );
}
