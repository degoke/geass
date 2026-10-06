import { useState } from "react";
import { action, list, resourceName } from "@/lib/api";
import { canMutate } from "@/lib/dashboard";
import { alertUser } from "@/lib/notify";
import { Badge, Button, DataTable, Field, FormStack, Input, PageSection, Select } from "@/components/geass-ui";
import { SettingsFormActions, SettingsSection } from "@/layout/SettingsShell";
import { PlatformPage } from "@/layout/PlatformPage";

export function CloudConnectionsPage({ data, reload, embedded = false }) {
  const items = list(data, "cloudConnections");
  const [provider, setProvider] = useState("AWS");
  const [form, setForm] = useState({ name: "", project: "", accessKeyId: "", secretAccessKey: "", region: "us-east-1", token: "", organization: "" });
  const body = (
    <>
      <PageSection title="Connections" description="External databases and buckets stay disabled until a connection is ready." plain={embedded}>
        <DataTable
          columns={[
            { label: "Name", key: "name" },
            { label: "Provider", key: "provider" },
            { label: "Scope", key: "scope" },
            {
              label: "Status",
              align: "right",
              render: (row) => <Badge tone={row.ready ? "success" : "warning"}>{row.ready ? "Ready" : "Pending"}</Badge>,
            },
          ]}
          rows={items.map((item) => ({
            name: resourceName(item),
            provider: item.spec?.provider,
            scope: item.spec?.project || "Platform",
            ready: Boolean(item.status?.available),
          }))}
          getRowKey={(row) => row.name}
          empty="No cloud connections yet."
        />
      </PageSection>
      <PageSection title="Add connection" description="Credentials are stored for the control plane only." plain={embedded}>
        <FormStack onSubmit={(event) => { event.preventDefault(); action("/cloud-connections/create", { ...form, provider }).then(() => { reload(); alertUser("Connection saved"); setForm({ name: "", project: "", accessKeyId: "", secretAccessKey: "", region: "us-east-1", token: "", organization: "" }); }).catch((error) => alertUser(error.message)); }}>
          <Field label="Name"><Input required value={form.name} onChange={(event) => setForm({ ...form, name: event.target.value })} placeholder="prod-aws" /></Field>
          <Field label="Project scope" description="Required. Credentials are only usable by this Geass project.">
            <Input required value={form.project} onChange={(event) => setForm({ ...form, project: event.target.value })} placeholder="payments" />
          </Field>
          <Field label="Provider"><Select value={provider} onChange={(event) => setProvider(event.target.value)}><option>AWS</option><option>PlanetScale</option></Select></Field>
          {provider === "AWS" ? <>
            <Field label="Access key ID"><Input required value={form.accessKeyId} onChange={(event) => setForm({ ...form, accessKeyId: event.target.value })} /></Field>
            <Field label="Secret access key"><Input required type="password" value={form.secretAccessKey} onChange={(event) => setForm({ ...form, secretAccessKey: event.target.value })} /></Field>
            <Field label="Region"><Input value={form.region} onChange={(event) => setForm({ ...form, region: event.target.value })} /></Field>
          </> : <>
            <Field label="Organization"><Input required value={form.organization} onChange={(event) => setForm({ ...form, organization: event.target.value })} /></Field>
            <Field label="Service token"><Input required type="password" value={form.token} onChange={(event) => setForm({ ...form, token: event.target.value })} /></Field>
          </>}
          <SettingsFormActions>
            <Button type="submit" disabled={!canMutate(data)}>Save connection</Button>
          </SettingsFormActions>
        </FormStack>
      </PageSection>
    </>
  );

  if (embedded) {
    return (
      <SettingsSection title="Cloud connections" description="Connect AWS and PlanetScale once for the cluster.">
        {body}
      </SettingsSection>
    );
  }

  return (
    <PlatformPage title="Cloud connections" description="Connect AWS and PlanetScale once for the cluster.">
      {body}
    </PlatformPage>
  );
}
