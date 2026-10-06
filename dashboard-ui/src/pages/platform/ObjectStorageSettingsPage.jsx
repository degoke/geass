import { useState } from "react";
import { action, condition, list, resourceName } from "@/lib/api";
import { canMutate } from "@/lib/dashboard";
import { alertUser } from "@/lib/notify";
import { capacityFits, cpuMillis, memoryBytes } from "@/lib/capacity";
import { AppLink } from "@/components/AppLink";
import { SizeFields } from "@/components/SizeFields";
import { Badge, Button, DangerZone, DataTable, Field, FormStack, Input, Muted, PageSection } from "@/components/geass-ui";
import { SettingsSection } from "@/layout/SettingsShell";
import { PlatformPage } from "@/layout/PlatformPage";
import { Trash2 } from "lucide-react";

export function ObjectStorageSettingsPage({ data, reload, embedded = false }) {
  const items = list(data, "objectStores").filter((item) => !item.spec?.project && item.spec?.engine !== "S3" && item.spec?.placement !== "External");
  const server = items[0];
  const ready = condition(server) === "True";
  const capacity = data?.platform?.capacity || {};
  const [cpu, setCpu] = useState("250m");
  const [memory, setMemory] = useState("512Mi");
  const [confirmDelete, setConfirmDelete] = useState("");
  const live = { cpu, memory, cpuMillis: cpuMillis(cpu), memoryBytes: memoryBytes(memory), perCpuMillis: cpuMillis(cpu), perMemoryBytes: memoryBytes(memory), replicas: 1 };
  const fits = capacityFits(capacity, live);
  const projectBuckets = list(data, "objectStores").filter((item) => item.spec?.project && item.spec?.engine !== "S3" && item.spec?.placement !== "External");
  const serverName = resourceName(server);
  const body = (
    <>
      <PageSection
        title={server ? "MinIO server" : "Set up MinIO"}
        description={server ? server.status?.endpoint || "Cluster MinIO server" : "In-cluster buckets stay disabled until this server exists."}
        plain={embedded}
      >
        {server ? (
          <>
            <DataTable
              columns={[
                { label: "Server", key: "name" },
                { label: "Endpoint", key: "endpoint" },
                {
                  label: "Status",
                  align: "right",
                  render: (row) => <Badge tone={row.ready ? "success" : "warning"}>{row.ready ? "Ready" : "Pending"}</Badge>,
                },
              ]}
              rows={[{ name: serverName, endpoint: server.status?.endpoint || "Cluster MinIO server", ready }]}
              getRowKey={(row) => row.name}
            />
            {canMutate(data) && (
              projectBuckets.length
                ? <Muted size="sm" mt="md">Delete project buckets before removing the MinIO server.</Muted>
                : (
                  <DangerZone>
                    <Field label={`Type ${serverName} to remove the MinIO server`}>
                      <Input value={confirmDelete} onChange={(event) => setConfirmDelete(event.target.value)} autoComplete="off" placeholder={serverName} />
                    </Field>
                    <Button type="button" variant="danger" disabled={confirmDelete !== serverName} onClick={() => action(`/object-stores/${serverName}/delete`, { confirmName: confirmDelete }).then(() => { reload(); alertUser("MinIO server removed"); }).catch((error) => alertUser(error.message))}>
                      <Trash2 size={15} /> Remove MinIO server
                    </Button>
                  </DangerZone>
                )
            )}
          </>
        ) : (
          <FormStack>
            <SizeFields cpu={cpu} memory={memory} onChange={(key, value) => { if (key === "cpu") setCpu(value); else setMemory(value); }} data={data} />
            {!fits && (
              <Muted size="sm">
                The cluster does not have enough capacity. <AppLink href="/cluster">Open cluster capacity</AppLink> before creating MinIO.
              </Muted>
            )}
            <Button disabled={!canMutate(data) || !fits} onClick={() => action("/object-stores/create", { cluster: "on", engine: "MinIO", placement: "InCluster", cpu, memory }).then(() => { reload(); alertUser("MinIO server created"); }).catch((error) => alertUser(error.message))}>
              Set up MinIO server
            </Button>
          </FormStack>
        )}
      </PageSection>
    </>
  );

  if (embedded) {
    return (
      <SettingsSection title="Object storage" description="One MinIO server for the cluster. Project buckets are created on this server.">
        {body}
      </SettingsSection>
    );
  }

  return (
    <PlatformPage title="Object storage" description="One MinIO server for the cluster. Project buckets are created on this server.">
      {body}
    </PlatformPage>
  );
}
