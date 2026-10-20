import { useEffect, useMemo, useState } from "react";
import { Link } from "@tanstack/react-router";
import { X } from "lucide-react";
import { action } from "@/lib/api";
import { notify } from "@/lib/notify";
import { parseTagList } from "@/lib/placement";
import {
  flag,
  formatPrice,
  isMachineConnection,
  missingSettings,
  nameProblem,
  parseList,
  providerLabel,
  providerOf,
  settingsFromPool,
  settingsPayload,
  useDebounced,
  useRefreshCompute,
  useSizes,
} from "@/lib/compute";
import { Button, Field, Input, Muted, Select, Switch, Textarea } from "@/components/geass-ui";
import { Alert, Callout, Choice, Optional, Required } from "@/components/compute/ComputeBits";
import {
  PlacementFields,
  ProviderSettingsFields,
  SizeListField,
  SizePicker,
} from "@/components/compute/ComputeFields";
import { SettingsFormActions } from "@/layout/SettingsShell";

const DEFAULT_SCALE = {
  scaleUpDelaySeconds: 30,
  maxScaleUp: 5,
  scaleDownUtilizationPercent: 50,
  scaleDownAfterMinutes: 10,
  scaleDownDelayAfterAddMinutes: 10,
  minNodeLifetimeMinutes: 0,
  spareNodes: 0,
};

// What each autoscaling setting means, in plain words, with the limits the server enforces.
const SCALE_FIELDS = [
  {
    key: "scaleUpDelaySeconds", label: "Wait before adding a node", unit: "seconds", min: 1, max: 3600,
    hint: "A pod must have been unable to start this long before a node is added for it. It keeps a pod that is only slow to be scheduled from buying a machine.",
  },
  {
    key: "maxScaleUp", label: "Add at most", unit: "nodes at a time", min: 1, max: 50,
    hint: "The most nodes added in one step.",
  },
  {
    key: "scaleDownUtilizationPercent", label: "A node is idle below", unit: "% used", min: 1, max: 100,
    hint: "The share of a node's CPU or memory, whichever is higher, that its pods reserve. Below it, the node is a candidate for removal.",
  },
  {
    key: "scaleDownAfterMinutes", label: "Remove an idle node after", unit: "minutes", min: 1, max: 1440,
    hint: "How long a node must have been idle before it is removed.",
  },
  {
    key: "scaleDownDelayAfterAddMinutes", label: "No removals after adding a node for", unit: "minutes", min: 1, max: 1440,
    hint: "After a node is added, none is removed for this long, so capacity is not taken away before the load that asked for it has used it.",
  },
  {
    key: "minNodeLifetimeMinutes", label: "Keep every node at least", unit: "minutes", min: 0, max: 10080,
    hint: "Most clouds bill each started hour, so a node removed after ten minutes costs a full hour. 55 keeps a node for the hour that is paid for anyway.",
  },
  {
    key: "spareNodes", label: "Keep empty nodes ready", unit: "nodes", min: 0, max: 20,
    hint: "Spare nodes so that a burst finds room at once instead of waiting minutes for a machine. They count against the maximum and cost money while idle.",
  },
];

const MONTH_HOURS = 730;

function NumberField({ label, hint, value, onChange, min, max, unit, required, id, ...rest }) {
  return (
    <Field label={<>{label}{required ? <Required /> : null}</>} description={hint}>
      <div className="cp-unit">
        <Input
          id={id}
          type="number"
          inputMode="numeric"
          min={min}
          max={max}
          step="1"
          value={value}
          onChange={(event) => onChange(event.currentTarget.value)}
          aria-label={label}
          {...rest}
        />
        {unit ? <span>{unit}</span> : null}
      </div>
    </Field>
  );
}

function emptyServer() {
  return { name: "", address: "", sshUser: "root", sshPort: "22" };
}

function wholeNumber(text) {
  const value = String(text).trim();
  return /^\d+$/.test(value) ? Number(value) : NaN;
}

/**
 * Makes a pool of nodes, or changes one. A pool either creates machines at a cloud (a connection) or joins servers you
 * own when it needs room (an inventory). Money is spent only up to the maximum, which is why the maximum is required and
 * what it costs is said next to it.
 */
export function PoolForm({ pool, providers, connections, mutable = true, onDone, onCancel }) {
  const editing = Boolean(pool);
  const refresh = useRefreshCompute();
  const machineConnections = useMemo(() => connections.filter(isMachineConnection), [connections]);

  const [source, setSource] = useState(pool ? (pool.connection ? "cloud" : "inventory") : "cloud");
  const [name, setName] = useState(pool?.name || "");
  const [connection, setConnection] = useState(pool?.connection || "");
  const [region, setRegion] = useState(pool?.region || "");
  const [size, setSize] = useState(pool?.size || "");
  const [fallbackSizes, setFallbackSizes] = useState((pool?.fallbackSizes || []).join(", "));
  const [fallbackRegions, setFallbackRegions] = useState((pool?.fallbackRegions || []).join(", "));
  const [image, setImage] = useState(pool?.image || "");
  const [spot, setSpot] = useState(Boolean(pool?.spot));
  const [addressType, setAddressType] = useState(pool?.addressType || "Public");
  const [storage, setStorage] = useState(pool?.storage || "None");
  const [minNodes, setMinNodes] = useState(pool ? String(pool.minNodes) : "0");
  const [maxNodes, setMaxNodes] = useState(pool ? String(pool.maxNodes) : "");
  const [autoscaling, setAutoscaling] = useState(pool ? Boolean(pool.autoscaling?.enabled) : true);
  const [suspend, setSuspend] = useState(Boolean(pool?.suspended));
  const [scale, setScale] = useState(() => {
    const current = pool?.autoscaling;
    return Object.fromEntries(Object.keys(DEFAULT_SCALE).map((key) => [key, String(current?.[key] ?? DEFAULT_SCALE[key])]));
  });
  const [orphanPolicy, setOrphanPolicy] = useState(pool?.orphanPolicy || "Report");
  const [servers, setServers] = useState([emptyServer()]);
  const [sshKey, setSshKey] = useState("");
  const [knownHosts, setKnownHosts] = useState("");
  const [placement, setPlacement] = useState({ region: "", zone: "", tags: "", reserved: [] });
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  // A new pool starts on the first cloud that accepts its connection.
  useEffect(() => {
    if (editing || connection || !machineConnections.length) return;
    const first = machineConnections.find((item) => item.available) || machineConnections[0];
    setConnection(first.name);
  }, [editing, connection, machineConnections]);

  const connectionInfo = connections.find((item) => item.name === connection);
  const providerId = pool?.provider || connectionInfo?.provider || "";
  const descriptor = providerOf(providers, providerId);
  const providerName = providerLabel(providers, providerId);

  // A pool being edited starts with its own settings; a new one starts empty, and starts again for another connection.
  const [settingValues, setSettingValues] = useState(() => (pool ? settingsFromPool(descriptor, pool.settings).values : {}));
  const [settingRows, setSettingRows] = useState(() => (pool ? settingsFromPool(descriptor, pool.settings).rows : []));
  useEffect(() => {
    if (editing) return;
    setSettingValues({});
    setSettingRows([]);
  }, [editing, connection]);

  const settledRegion = useDebounced(region.trim());
  const sizes = useSizes(source === "cloud" ? connection : "", settledRegion, mutable);
  const sizeList = sizes.data || [];
  const sizeInfo = sizeList.find((item) => item.id === size.trim()) || (editing && pool?.activeSize?.name === size.trim() ? pool.activeSize : null);

  const max = wholeNumber(maxNodes);
  const min = wholeNumber(minNodes);
  const price = Number(sizeInfo?.hourlyPrice);
  const currency = sizeInfo?.currency || "";
  const cap = price > 0 && max > 0 ? price * max : 0;

  const validate = () => {
    if (!editing) {
      const problem = nameProblem(name.trim(), "pool", 48);
      if (problem) return problem;
    }
    if (source === "cloud") {
      if (!editing && !connection) return "Choose the cloud connection that creates the machines.";
      if (!size.trim()) return "Give the size of the machines to create.";
      const missing = missingSettings(descriptor, settingValues);
      if (missing.length) return `${providerName} needs ${missing.map((setting) => setting.label).join(", ")}.`;
    } else if (!editing) {
      const listed = servers.filter((server) => server.name || server.address);
      if (!listed.length) return "List at least one server.";
      for (const server of listed) {
        const serverProblem = nameProblem(server.name.trim(), "server", 55);
        if (serverProblem) return `Server "${server.name}": ${serverProblem}`;
        if (!server.address.trim()) return `The server ${server.name} needs an address.`;
        if (!server.sshUser.trim()) return `The server ${server.name} needs an SSH user.`;
        if (server.sshPort.trim() && !(wholeNumber(server.sshPort) > 0 && wholeNumber(server.sshPort) < 65536)) return `The SSH port of ${server.name} is a number from 1 to 65535.`;
      }
      if (new Set(listed.map((server) => server.name.trim())).size !== listed.length) return "Two servers have the same name.";
      if (!sshKey.includes("PRIVATE KEY")) return "Paste the SSH private key, with its BEGIN and END lines.";
    }
    if (Number.isNaN(max)) return "Say the most nodes the pool may ever have. It is the limit on what the pool can cost.";
    if (Number.isNaN(min)) return "The fewest nodes is a whole number, 0 or more.";
    if (max > 200 || min > 200) return "The fewest and the most nodes are from 0 to 200.";
    if (max < min) return "The most nodes cannot be below the fewest.";
    if (source === "inventory" && !editing) {
      const count = servers.filter((server) => server.name || server.address).length;
      if (max > count) return `The most nodes is ${max} but you listed ${count} ${count === 1 ? "server" : "servers"}. A pool cannot have more nodes than servers.`;
    }
    if (source === "inventory" && editing && max > (pool.inventory || []).length) {
      return `The most nodes is ${max} but the pool has ${(pool.inventory || []).length} servers.`;
    }
    for (const field of SCALE_FIELDS) {
      if (!autoscaling) break;
      const value = wholeNumber(scale[field.key]);
      if (Number.isNaN(value) || value < field.min || value > field.max) return `${field.label}: a whole number from ${field.min} to ${field.max}.`;
    }
    return "";
  };

  const submit = async (event) => {
    event.preventDefault();
    setError("");
    const problem = validate();
    if (problem) {
      setError(problem);
      return;
    }
    const body = {
      minNodes: String(min),
      maxNodes: String(max),
      storage,
      orphanPolicy,
      autoscaling: flag(autoscaling),
    };
    if (autoscaling) SCALE_FIELDS.forEach((field) => { body[field.key] = String(wholeNumber(scale[field.key])); });
    if (editing) body.suspend = flag(suspend);
    if (!editing) body.name = name.trim();
    if (source === "cloud") {
      if (!editing) body.connection = connection;
      Object.assign(body, {
        region: region.trim(),
        size: size.trim(),
        fallbackSizes: parseList(fallbackSizes).join(","),
        fallbackRegions: parseList(fallbackRegions).join(","),
        image: image.trim(),
        spot: flag(spot),
        addressType,
        settings: settingsPayload(descriptor, settingValues, settingRows),
      });
    } else if (!editing) {
      body.inventory = JSON.stringify(
        servers
          .filter((server) => server.name || server.address)
          .map((server) => ({
            name: server.name.trim(),
            address: server.address.trim(),
            sshUser: server.sshUser.trim(),
            sshPort: server.sshPort.trim() ? wholeNumber(server.sshPort) : 22,
          })),
      );
      body.inventoryKey = sshKey;
      if (knownHosts.trim()) body.knownHosts = knownHosts;
    }
    if (!editing) {
      const tags = parseTagList(placement.tags);
      if (placement.region.trim() || placement.zone.trim() || tags.length) {
        body.placementRegion = placement.region.trim();
        body.placementZone = placement.zone.trim();
        body.tags = tags.join(",");
        body.reservedTags = placement.reserved.filter((tag) => tags.includes(tag)).join(",");
      }
    }
    setBusy(true);
    try {
      await action(editing ? `/pools/${pool.name}/update` : "/pools/create", body);
      await refresh();
      notify(editing ? `Saved the pool ${pool.name}` : `Created the pool ${name.trim()}`, "success");
      onDone?.(editing ? pool.name : name.trim());
    } catch (failure) {
      setError(failure.message);
    } finally {
      setBusy(false);
    }
  };

  const unusable = (item) => !item.available;

  return (
    <form className="cp-form cp-form-wide" onSubmit={submit}>
      <fieldset className="cp-lock" disabled={!mutable || busy}>
        {!editing ? (
          <fieldset className="cp-fieldset">
            <legend>Pool</legend>
            <Field label={<>Name<Required /></>} description="Lowercase letters, numbers and hyphens, at most 48 characters. Each node's name is made from it.">
              <Input
                value={name}
                onChange={(event) => setName(event.currentTarget.value.toLowerCase())}
                placeholder="burst"
                maxLength={48}
                autoComplete="off"
                spellCheck={false}
                aria-label="Pool name"
                data-autofocus
              />
            </Field>
            <Field label="Where do the machines come from?">
              <div className="cp-choices" role="group" aria-label="Where the machines come from">
                <Choice
                  selected={source === "cloud"}
                  onClick={() => setSource("cloud")}
                  title="A cloud"
                  description="Geass creates machines at a cloud when pods cannot start, and deletes them when they are idle."
                />
                <Choice
                  selected={source === "inventory"}
                  onClick={() => setSource("inventory")}
                  title="My servers"
                  description="Geass joins servers you already own when it needs room, and gives them back when it does not."
                />
              </div>
            </Field>
          </fieldset>
        ) : null}

        {source === "cloud" ? (
          <fieldset className="cp-fieldset">
            <legend>Machines</legend>
            {editing ? (
              <p className="cp-fieldset-note">
                Cloud: <strong>{providerName || "a cloud"}</strong> through the connection <strong>{pool.connection}</strong>. Size, region and settings apply to nodes
                made from now on. The nodes the pool has keep theirs.
              </p>
            ) : (
              <Field
                label={<>Cloud connection<Required /></>}
                description={machineConnections.length ? "The connection whose credentials create the machines." : undefined}
              >
                {machineConnections.length ? (
                  <Select value={connection} onChange={(event) => setConnection(event.currentTarget.value)} aria-label="Cloud connection">
                    {machineConnections.map((item) => (
                      <option key={item.name} value={item.name}>
                        {item.name} · {providerLabel(providers, item.provider)}{unusable(item) ? " (not available)" : ""}
                      </option>
                    ))}
                  </Select>
                ) : (
                  <Callout>
                    No cloud is connected yet. <Link to="/settings/connectors">Connect a cloud</Link> in Connectors, then come back, or make a pool of your own servers.
                  </Callout>
                )}
                {connectionInfo && unusable(connectionInfo) ? (
                  <Muted size="xs">This connection is not available now{connectionInfo.message ? `: ${connectionInfo.message}` : ""}. The pool cannot create machines until it is.</Muted>
                ) : null}
              </Field>
            )}
            <Field
              label={<>Region<Optional /></>}
              description={[descriptor?.regionHelp, connectionInfo?.region ? `The connection's default is ${connectionInfo.region}.` : ""].filter(Boolean).join(" ") || "The cloud's own region or location."}
            >
              <Input
                value={region}
                onChange={(event) => setRegion(event.currentTarget.value)}
                placeholder={connectionInfo?.region || "Cloud's choice"}
                autoComplete="off"
                spellCheck={false}
                aria-label="Region"
              />
            </Field>
            <SizePicker
              inputId="cp-pool-size"
              label="Machine size"
              required
              value={size}
              onChange={setSize}
              query={sizes}
              connection={mutable ? connection : ""}
              quiet={!mutable}
              hint="The cloud's name for a size. Choose one from the list, or type it."
            />
            <SizeListField
              label="Fallback sizes"
              hint="Tried in order, up to five, when the cloud has no room for the size above or the account is at a limit for it."
              value={fallbackSizes}
              onChange={setFallbackSizes}
              sizes={sizeList}
            />
            <Field label={<>Fallback regions<Optional /></>} description="Tried in order, up to five, when the cloud has no room in the region above. Separate with commas.">
              <Input value={fallbackRegions} onChange={(event) => setFallbackRegions(event.currentTarget.value)} placeholder="one region, then another" autoComplete="off" spellCheck={false} aria-label="Fallback regions" />
            </Field>
            <ProviderSettingsFields
              descriptor={descriptor}
              values={settingValues}
              rows={settingRows}
              onValues={setSettingValues}
              onRows={setSettingRows}
            />
            <div className="cp-check">
              <Switch label="Use spot machines" checked={spot} onChange={(event) => setSpot(event.currentTarget.checked)} />
              <p className="cp-check-hint">The cloud's cheaper machines, which it may take back at any time. A node that is taken back is replaced, and the services on it start again elsewhere.</p>
            </div>
          </fieldset>
        ) : null}

        {source === "inventory" ? (
          <fieldset className="cp-fieldset">
            <legend>Servers</legend>
            {editing ? (
              <>
                <p className="cp-fieldset-note">
                  The servers of an inventory cannot be changed here. To draw on others, make a new pool.
                </p>
                <p className="cp-mono">{(pool.inventory || []).join(", ")}</p>
              </>
            ) : (
              <>
                <p className="cp-fieldset-note">
                  Servers you already own. Geass reaches them over SSH as a user that is root or can use sudo without a password, joins one to the cluster when pods need
                  room, and gives it back when it is idle. Nothing is bought.
                </p>
                <div className="cp-rows">
                  {servers.map((server, index) => {
                    const update = (patch) => setServers(servers.map((item, i) => (i === index ? { ...item, ...patch } : item)));
                    return (
                      <div className="cp-repeat" key={index}>
                        <Field label={index === 0 ? "Name" : undefined}>
                          <Input value={server.name} onChange={(event) => update({ name: event.currentTarget.value.toLowerCase() })} placeholder="spare-1" autoComplete="off" spellCheck={false} aria-label={`Server ${index + 1} name`} />
                        </Field>
                        <Field label={index === 0 ? "Address" : undefined}>
                          <Input value={server.address} onChange={(event) => update({ address: event.currentTarget.value })} placeholder="198.51.100.10" autoComplete="off" spellCheck={false} aria-label={`Server ${index + 1} address`} />
                        </Field>
                        <Field label={index === 0 ? "SSH user" : undefined}>
                          <Input value={server.sshUser} onChange={(event) => update({ sshUser: event.currentTarget.value })} autoComplete="off" spellCheck={false} aria-label={`Server ${index + 1} SSH user`} />
                        </Field>
                        <Field label={index === 0 ? "Port" : undefined}>
                          <Input value={server.sshPort} inputMode="numeric" onChange={(event) => update({ sshPort: event.currentTarget.value })} autoComplete="off" aria-label={`Server ${index + 1} SSH port`} />
                        </Field>
                        <Button
                          type="button"
                          variant="ghost"
                          size="sm"
                          disabled={servers.length === 1}
                          aria-label={`Remove server ${index + 1}`}
                          onClick={() => setServers(servers.filter((_, i) => i !== index))}
                        >
                          <X size={14} />
                        </Button>
                      </div>
                    );
                  })}
                  {servers.length < 100 ? (
                    <Button type="button" variant="outline" size="sm" onClick={() => setServers([...servers, emptyServer()])}>Add a server</Button>
                  ) : null}
                </div>
                <Field
                  label={<>SSH private key<Required /></>}
                  description="One key for every server in the list. It is stored in a Secret that only the Geass operator reads, and is never shown again."
                >
                  <Textarea
                    className="cp-textarea-mono"
                    rows={6}
                    value={sshKey}
                    onChange={(event) => setSshKey(event.currentTarget.value)}
                    placeholder={"-----BEGIN OPENSSH PRIVATE KEY-----"}
                    autoComplete="off"
                    spellCheck={false}
                    aria-label="SSH private key"
                  />
                </Field>
                <Field label={<>Host keys<Optional /></>} description="The servers' host keys, in known_hosts form, to pin them. Without them Geass does not check which server it is talking to.">
                  <Textarea
                    className="cp-textarea-mono"
                    rows={3}
                    value={knownHosts}
                    onChange={(event) => setKnownHosts(event.currentTarget.value)}
                    autoComplete="off"
                    spellCheck={false}
                    aria-label="Host keys"
                  />
                </Field>
              </>
            )}
          </fieldset>
        ) : null}

        <fieldset className="cp-fieldset">
          <legend>How many nodes</legend>
          <div className="cp-row">
            <NumberField
              label="Fewest nodes"
              required
              value={minNodes}
              onChange={setMinNodes}
              min={0}
              max={200}
              hint={autoscaling ? "The pool always has at least this many." : "A pool that does not autoscale has exactly this many."}
            />
            <NumberField
              label="Most nodes"
              required
              value={maxNodes}
              onChange={setMaxNodes}
              min={0}
              max={200}
              placeholder="5"
              hint="The pool never has more. This is the cost cap."
            />
          </div>
          <Field label="Data on these nodes" description={storage === "None"
            ? "None keeps databases, buckets and volumes off these nodes. Only services run on them, which keep nothing on the node, so a node can be removed at any time."
            : "Allow makes them ordinary workers that may hold data. The autoscaler never removes a node while it holds a volume."}
          >
            <Select value={storage} onChange={(event) => setStorage(event.currentTarget.value)} aria-label="Data on these nodes">
              <option value="None">None (default)</option>
              <option value="Allow">Allow</option>
            </Select>
          </Field>
        </fieldset>

        <fieldset className="cp-fieldset">
          <legend>Autoscale</legend>
          <div className="cp-check">
            <Switch
              label="Autoscale"
              checked={autoscaling}
              onChange={(event) => setAutoscaling(event.currentTarget.checked)}
            />
            <p className="cp-check-hint">
              Add nodes when pods cannot start, and remove idle ones, between the fewest and the most. Off, the pool keeps exactly the fewest nodes.
            </p>
          </div>
          {autoscaling ? (
            <>
              <div className="cp-scale-grid">
                {SCALE_FIELDS.map((field) => (
                  <NumberField
                    key={field.key}
                    label={field.label}
                    unit={field.unit}
                    min={field.min}
                    max={field.max}
                    value={scale[field.key]}
                    hint={`${field.hint} Default ${DEFAULT_SCALE[field.key]}.`}
                    onChange={(value) => setScale({ ...scale, [field.key]: value })}
                  />
                ))}
              </div>
              <Button type="button" variant="outline" size="sm" onClick={() => setScale(Object.fromEntries(Object.keys(DEFAULT_SCALE).map((key) => [key, String(DEFAULT_SCALE[key])])))}>
                Use the defaults
              </Button>
            </>
          ) : null}
          {editing ? (
            <div className="cp-check">
              <Switch label="Suspend scaling" checked={suspend} onChange={(event) => setSuspend(event.currentTarget.checked)} />
              <p className="cp-check-hint">Stop adding and removing nodes. The nodes the pool has stay, and keep costing what they cost.</p>
            </div>
          ) : null}
        </fieldset>

        <details className="cp-details">
          <summary>More options</summary>
          <div className="cp-details-body">
            {source === "cloud" ? (
              <>
                <Field label={<>Image<Optional /></>} description="The operating system image. Default: the cloud's Ubuntu 24.04.">
                  <Input value={image} onChange={(event) => setImage(event.currentTarget.value)} autoComplete="off" spellCheck={false} aria-label="Image" />
                </Field>
                <Field label="Address Geass uses" description="Public (the default) is for a cluster that spans sites. Private suits a cluster whose nodes share a private network with these machines.">
                  <Select value={addressType} onChange={(event) => setAddressType(event.currentTarget.value)} aria-label="Address type">
                    <option value="Public">Public</option>
                    <option value="Private">Private</option>
                  </Select>
                </Field>
              </>
            ) : null}
            <Field
              label="Machines no node owns"
              description={orphanPolicy === "Delete"
                ? "Delete removes a machine at the cloud that has had no node for twenty minutes. A deleted machine cannot be brought back."
                : "Report only lists such machines in the pool's status and raises a problem. It is the default, because deleting a machine cannot be undone."}
            >
              <Select value={orphanPolicy} onChange={(event) => setOrphanPolicy(event.currentTarget.value)} aria-label="Machines no node owns">
                <option value="Report">Report (default)</option>
                <option value="Delete">Delete</option>
              </Select>
            </Field>
            {!editing ? (
              <>
                <p className="cp-fieldset-note">Placement: where the nodes sit and what they are for. Left empty, the nodes carry no region or tags.</p>
                <PlacementFields
                  region={placement.region}
                  zone={placement.zone}
                  tags={placement.tags}
                  reserved={placement.reserved}
                  onChange={(patch) => setPlacement({ ...placement, ...patch })}
                />
              </>
            ) : (
              <p className="cp-fieldset-note">
                {pool.placement
                  ? `Placement: ${[pool.placement.region && `region ${pool.placement.region}`, pool.placement.zone && `site ${pool.placement.zone}`, (pool.placement.tags || []).length && `tags ${pool.placement.tags.join(", ")}`].filter(Boolean).join(", ")}. It is set when the pool is made.`
                  : "The nodes carry no region or tags. Placement is set when the pool is made."}
              </p>
            )}
          </div>
        </details>

        {source === "cloud" ? (
          <Callout tone="cost">
            <strong>This spends money.</strong> Geass creates the machines in your account at {providerName || "the cloud"}, and the cloud bills you for them.{" "}
            {Number.isNaN(max)
              ? "Set the most nodes: it is the most machines the pool can ever create, and so the most it can cost."
              : cap > 0
                ? <>At most <strong>{max}</strong> {max === 1 ? "machine" : "machines"} at {formatPrice(price, currency)} each: up to <strong>{formatPrice(cap, currency)}</strong>, about {formatPrice(cap * MONTH_HOURS, currency, "")} a month, if the pool grows to its maximum.</>
                : <>At most <strong>{max}</strong> {max === 1 ? "machine" : "machines"}. The cloud does not state a price for this size, so check what it costs before you rely on it.</>}
            {!Number.isNaN(min) && min > 0 && !editing ? ` ${min} ${min === 1 ? "machine is" : "machines are"} created as soon as the pool is saved.` : ""}
            {editing && !Number.isNaN(min) && min > pool.minNodes && min > (pool.nodes || 0)
              ? ` Raising the fewest nodes to ${min} creates ${min - (pool.nodes || 0)} more ${min - (pool.nodes || 0) === 1 ? "machine" : "machines"} as soon as you save.`
              : ""}
          </Callout>
        ) : (
          <Callout>Nothing is bought: the pool uses servers you already own. It joins them to the cluster when pods need room.</Callout>
        )}

        {error ? <Alert tone="danger">{error}</Alert> : null}
        {mutable ? (
          <SettingsFormActions>
            <Button type="submit" loading={busy}>{editing ? "Save changes" : "Create pool"}</Button>
            {onCancel ? <Button type="button" variant="outline" disabled={busy} onClick={onCancel}>Cancel</Button> : null}
          </SettingsFormActions>
        ) : (
          <Muted size="sm">Only an admin can change a pool.</Muted>
        )}
      </fieldset>
    </form>
  );
}
