import { useState } from "react";
import { Modal, Stack, Text } from "@mantine/core";
import { Database } from "lucide-react";
import { action, condition, list, resourceDisplayName, resourceName } from "@/lib/api";
import { canMutate } from "@/lib/dashboard";
import { alertUser } from "@/lib/notify";
import { defaultSizeForKind, defaultStorageForEngine, storageSizeOptions } from "@/lib/capacity";
import { platformRegions, sharedServerRegion } from "@/lib/placement";
import { SizeFields } from "@/components/SizeFields";
import { Badge, Button, DataTable, Field, Input, Muted, Select, Switch } from "@/components/geass-ui";
import { SettingsSection, SettingsSheet } from "@/layout/SettingsShell";
import { PlatformPage } from "@/layout/PlatformPage";

const ENGINES = [
  { value: "Postgres", label: "PostgreSQL" },
  { value: "MySQL", label: "MySQL" },
];

/** Shared servers are databases that belong to no project. */
export function sharedDatabaseServers(data) {
  return list(data, "databases").filter((item) => !item.spec?.project);
}

function haDescription(engineHasHa, haAvailable) {
  if (!engineHasHa) return "Only PostgreSQL and MySQL servers can be highly available.";
  return haAvailable ? "Three instances, so the server survives losing a node." : "Needs at least three healthy nodes.";
}

function SetupForm({ data, reload, onDone }) {
  const regions = platformRegions(data);
  const [engine, setEngine] = useState("Postgres");
  const [name, setName] = useState("");
  const [region, setRegion] = useState("");
  const [ha, setHa] = useState(false);
  const defaults = defaultSizeForKind("database", engine.toLowerCase());
  const [cpu, setCpu] = useState(defaults.cpu);
  const [memory, setMemory] = useState(defaults.memory);
  const [storage, setStorage] = useState(defaultStorageForEngine(engine, data?.platform?.defaultStorage));
  const [busy, setBusy] = useState(false);
  // PostgreSQL and MySQL have replication and failover; SQLite and Redis run as one instance.
  const engineHasHa = engine === "Postgres" || engine === "MySQL";
  const haAvailable = engineHasHa && Boolean(data?.platform?.haReady);

  const submit = async (event) => {
    event.preventDefault();
    setBusy(true);
    try {
      await action("/shared-databases/create", {
        engine,
        name: name.trim(),
        region,
        cpu,
        memory,
        storageSize: storage,
        highAvailability: ha && engineHasHa ? "on" : "",
      });
      await reload?.();
      alertUser("Shared database server is starting");
      onDone();
    } catch (error) {
      alertUser(error.message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <form onSubmit={submit}>
      <Stack gap="sm">
        <Field label="Engine">
          <Select value={engine} onChange={(event) => setEngine(event.currentTarget.value)} aria-label="Engine">
            {ENGINES.map((entry) => <option key={entry.value} value={entry.value}>{entry.label}</option>)}
          </Select>
        </Field>
        <Field label="Name" description="Optional. Projects see this name when they choose a server.">
          <Input value={name} onChange={(event) => setName(event.currentTarget.value)} placeholder={`cluster-${engine.toLowerCase()}`} aria-label="Name" />
        </Field>
        {regions.length ? (
          <Field label="Region" description="Projects can only use a server in their own region, so every query stays close.">
            <Select value={region} onChange={(event) => setRegion(event.currentTarget.value)} aria-label="Region">
              <option value="">Default region</option>
              {regions.map((entry) => <option key={entry.name} value={entry.name}>{entry.name}</option>)}
            </Select>
          </Field>
        ) : null}
        <SizeFields cpu={cpu} memory={memory} onChange={(key, value) => (key === "cpu" ? setCpu(value) : setMemory(value))} data={data} />
        <Field label="Storage" description="Disk for each instance. It can be increased later, not decreased.">
          <Select value={storage} onChange={(event) => setStorage(event.currentTarget.value)} aria-label="Storage">
            {storageSizeOptions(storage, "").map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}
          </Select>
        </Field>
        <Switch
          label="High availability"
          description={haDescription(engineHasHa, haAvailable)}
          checked={ha && engineHasHa}
          disabled={!haAvailable}
          onChange={(event) => setHa(event.currentTarget.checked)}
        />
        <Muted size="xs">
          Each project that uses this server gets a database and a login of its own. A login can reach only its database.
        </Muted>
        <Button type="submit" loading={busy}>Set up server</Button>
      </Stack>
    </form>
  );
}

function RemoveDialog({ server, inUse, reload, onClose }) {
  const [confirm, setConfirm] = useState("");
  const [busy, setBusy] = useState(false);
  const name = server ? resourceName(server) : "";
  const remove = async (event) => {
    event.preventDefault();
    setBusy(true);
    try {
      await action(`/databases/${name}/delete`, { confirmName: confirm });
      await reload?.();
      alertUser("Shared database server removed");
      onClose();
    } catch (error) {
      alertUser(error.message);
    } finally {
      setBusy(false);
    }
  };
  return (
    <Modal opened={Boolean(server)} onClose={() => { if (!busy) onClose(); }} title="Remove shared database server" centered>
      {server ? (
        <form onSubmit={remove}>
          <Stack gap="sm">
            <Text size="sm">
              This deletes <strong>{resourceDisplayName(server)}</strong> and everything on it.
              {inUse ? " Projects still have databases on it, so delete those first." : " Nothing is using it."}
            </Text>
            <Field label={`Type ${name} to confirm`}>
              <Input value={confirm} onChange={(event) => setConfirm(event.currentTarget.value)} aria-label="Confirm name" />
            </Field>
            <Button type="submit" color="red" disabled={inUse || confirm !== name} loading={busy}>Remove server</Button>
          </Stack>
        </form>
      ) : null}
    </Modal>
  );
}

const DESCRIPTION = "A Postgres or MySQL server that projects share. Each project takes a database of its own on it, with a login that reaches only that database.";

export function SharedDatabasesPage({ data, reload, embedded = false }) {
  const [setupOpen, setSetupOpen] = useState(false);
  const [removing, setRemoving] = useState(null);
  const mutable = canMutate(data);
  const servers = sharedDatabaseServers(data);
  const logical = list(data, "logicalDatabases");

  const rows = servers.map((server) => {
    const name = resourceName(server);
    const databases = logical.filter((item) => item.spec?.serverRef === name);
    const projects = new Set(databases.map((item) => item.spec?.project)).size;
    const ready = condition(server) === "True";
    return {
      key: name,
      name: resourceDisplayName(server),
      engine: server.spec?.engine === "MySQL" ? "MySQL" : "PostgreSQL",
      region: sharedServerRegion(server) || "—",
      usage: databases.length ? `${databases.length} ${databases.length === 1 ? "database" : "databases"} · ${projects} ${projects === 1 ? "project" : "projects"}` : "Not used yet",
      ready,
      inUse: databases.length > 0,
      raw: server,
    };
  });

  const body = (
    <>
      <SettingsSection
        title={embedded ? "Shared databases" : undefined}
        description={embedded ? DESCRIPTION : undefined}
        flat={embedded}
        actions={mutable ? <Button leftSection={<Database size={14} />} onClick={() => setSetupOpen(true)}>Set up a server</Button> : null}
      >
        <DataTable
          columns={[
            { label: "Server", key: "name" },
            { label: "Engine", key: "engine" },
            { label: "Region", key: "region" },
            { label: "In use", key: "usage" },
            { label: "Status", align: "right", render: (row) => <Badge tone={row.ready ? "success" : "warning"}>{row.ready ? "Ready" : "Starting"}</Badge> },
            {
              label: "",
              align: "right",
              render: (row) => (mutable ? <Button size="xs" variant="outline" onClick={() => setRemoving(row.raw)} aria-label={`Remove ${row.name}`}>Remove</Button> : null),
            },
          ]}
          rows={rows}
          getRowKey={(row) => row.key}
          empty="No shared database server yet. Set one up so projects can create databases on it without running a server each."
        />
      </SettingsSection>
      <SettingsSheet
        opened={setupOpen}
        onClose={() => setSetupOpen(false)}
        title="Shared database server"
        description="One server for many projects."
      >
        <SetupForm data={data} reload={reload} onDone={() => setSetupOpen(false)} />
      </SettingsSheet>
      <RemoveDialog
        server={removing}
        inUse={removing ? rows.find((row) => row.key === resourceName(removing))?.inUse : false}
        reload={reload}
        onClose={() => setRemoving(null)}
      />
    </>
  );

  if (embedded) return body;
  return <PlatformPage title="Databases" description={DESCRIPTION}>{body}</PlatformPage>;
}
