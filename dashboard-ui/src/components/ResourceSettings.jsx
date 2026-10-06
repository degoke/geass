import { useState } from "react";
import { action, resourceName } from "@/lib/api";
import { canMutate } from "@/lib/dashboard";
import { alertUser } from "@/lib/notify";
import { defaultSizeForKind } from "@/lib/capacity";
import { SizeFields } from "@/components/SizeFields";
import { Button, DangerZone, Field, FormStack, Input, KeyValueTable, Muted, PageSection, PageStack, Switch } from "@/components/geass-ui";
import { Trash2 } from "lucide-react";

export function ResourceSettings({ item, kind, name, title, data, project, reload, navigate }) {
  const defaults = defaultSizeForKind(kind === "apps" ? "service" : "database", (item.spec?.engine || "postgres").toLowerCase());
  const [cpu, setCpu] = useState(item.spec?.resources?.requests?.cpu || defaults.cpu);
  const [memory, setMemory] = useState(item.spec?.resources?.requests?.memory || defaults.memory);
  const [replicas, setReplicas] = useState(item.spec?.replicas || 1);
  const [autoscaling, setAutoscaling] = useState(Boolean(item.spec?.autoscaling?.maxReplicas > 1));
  const [maxReplicas, setMaxReplicas] = useState(item.spec?.autoscaling?.maxReplicas || 3);
  const [targetCPU, setTargetCPU] = useState(item.spec?.autoscaling?.targetCPUUtilization || 50);
  const [version, setVersion] = useState(item.spec?.version || "");
  const [confirmDelete, setConfirmDelete] = useState("");
  const inCluster = kind === "apps" || (kind === "databases" && item.spec?.placement !== "External");
  if (!canMutate(data)) {
    return (
      <PageSection title="Settings" description="Viewer role cannot change this resource.">
        <Muted size="sm">Your viewer role can inspect this resource but cannot change it.</Muted>
      </PageSection>
    );
  }
  const save = (event) => {
    event.preventDefault();
    const values = { project: item.spec?.project, environment: item.spec?.environment };
    if (kind === "databases") values.version = version;
    if (kind === "apps") Object.assign(values, { cpu, memory, replicas: String(replicas), autoscaling: autoscaling ? "on" : "", maxReplicas: String(maxReplicas), targetCPU: String(targetCPU) });
    if (kind === "databases" && inCluster) Object.assign(values, { cpu, memory });
    action(`/${kind}/${name}/update`, values).then(() => { reload(); alertUser(kind === "apps" ? "Settings saved. Deploy to apply them." : "Settings saved"); }).catch((error) => alertUser(error.message));
  };
  return (
    <PageStack>
      <PageSection title="Resource settings" description="Sizing, scaling, and metadata for this resource.">
        <KeyValueTable
          items={[
            { label: "Project", value: item.spec?.project },
            { label: "Environment", value: item.spec?.environment },
          ]}
        />
        <FormStack onSubmit={save}>
          {kind === "databases" && <Field label="Version"><Input value={version} onChange={(event) => setVersion(event.target.value)} /></Field>}
          {inCluster && <SizeFields cpu={cpu} memory={memory} onChange={(key, value) => { if (key === "cpu") setCpu(value); else setMemory(value); }} data={data} />}
          {kind === "apps" && (
            <>
              <Field label="Replicas"><Input type="number" min="1" value={replicas} onChange={(event) => setReplicas(Number(event.target.value) || 1)} /></Field>
              <Switch label="Autoscaling" checked={autoscaling} onChange={(event) => setAutoscaling(event.currentTarget.checked)} />
              {autoscaling && <>
                <Field label="Scale up to"><Input type="number" min={replicas || 1} value={maxReplicas} onChange={(event) => setMaxReplicas(Number(event.target.value) || 3)} /></Field>
                <Field label="CPU target %"><Input type="number" min="1" max="100" value={targetCPU} onChange={(event) => setTargetCPU(Number(event.target.value) || 50)} /></Field>
              </>}
            </>
          )}
          <Button type="submit">Save changes</Button>
        </FormStack>
      </PageSection>
      <PageSection title="Delete resource">
        <DangerZone>
          <Field label={`Type ${title} to delete`}>
            <Input value={confirmDelete} onChange={(event) => setConfirmDelete(event.target.value)} autoComplete="off" placeholder={title} />
          </Field>
          <Button type="button" variant="danger" disabled={confirmDelete !== title} onClick={() => action(`/${kind}/${name}/delete`, { confirmName: confirmDelete }).then(() => navigate({ to: `/projects/${resourceName(project)}` })).catch((error) => alertUser(error.message))}>
            <Trash2 size={15} /> Delete
          </Button>
        </DangerZone>
      </PageSection>
    </PageStack>
  );
}
