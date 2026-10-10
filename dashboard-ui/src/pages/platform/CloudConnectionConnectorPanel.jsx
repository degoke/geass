import { useState } from "react";
import { action, list, resourceName } from "@/lib/api";
import { canMutate } from "@/lib/dashboard";
import { alertUser } from "@/lib/notify";
import { Badge, Button, DataTable, Field, FormStack, Input } from "@/components/geass-ui";
import { SettingsFormActions } from "@/layout/SettingsShell";

const EMPTY_FORM = {
  name: "",
  project: "",
  accessKeyId: "",
  secretAccessKey: "",
  region: "us-east-1",
  token: "",
  organization: "",
};

export function CloudConnectionConnectorPanel({ data, reload, provider, inSheet = false }) {
  const items = list(data, "cloudConnections").filter((item) => item.spec?.provider === provider);
  const [form, setForm] = useState(EMPTY_FORM);

  const resetForm = () => setForm(EMPTY_FORM);

  const onSubmit = (event) => {
    event.preventDefault();
    action("/cloud-connections/create", { ...form, provider })
      .then(() => {
        reload();
        alertUser("Connection saved");
        resetForm();
      })
      .catch((error) => alertUser(error.message));
  };

  return (
    <>
      <DataTable
        columns={[
          { label: "Name", key: "name" },
          { label: "Scope", key: "scope" },
          {
            label: "Status",
            align: "right",
            render: (row) => <Badge tone={row.ready ? "success" : "warning"}>{row.ready ? "Ready" : "Pending"}</Badge>,
          },
        ]}
        rows={items.map((item) => ({
          name: resourceName(item),
          scope: item.spec?.project || "Platform",
          ready: Boolean(item.status?.available),
        }))}
        getRowKey={(row) => row.name}
        empty={`No ${provider} connections yet.`}
      />
      <FormStack onSubmit={onSubmit} style={{ marginTop: inSheet ? 16 : 24 }}>
        <p className="rw-rail-body">
          Credentials are stored for the control plane only. Each connection is scoped to one Geass project.
        </p>
        <Field label="Name">
          <Input
            required
            value={form.name}
            onChange={(event) => setForm({ ...form, name: event.target.value })}
            placeholder={provider === "AWS" ? "prod-aws" : "prod-planetscale"}
          />
        </Field>
        <Field label="Project scope" description="Required. Credentials are only usable by this Geass project.">
          <Input
            required
            value={form.project}
            onChange={(event) => setForm({ ...form, project: event.target.value })}
            placeholder="payments"
          />
        </Field>
        {provider === "AWS" ? (
          <>
            <Field label="Access key ID">
              <Input required value={form.accessKeyId} onChange={(event) => setForm({ ...form, accessKeyId: event.target.value })} />
            </Field>
            <Field label="Secret access key">
              <Input required type="password" value={form.secretAccessKey} onChange={(event) => setForm({ ...form, secretAccessKey: event.target.value })} />
            </Field>
            <Field label="Region">
              <Input value={form.region} onChange={(event) => setForm({ ...form, region: event.target.value })} />
            </Field>
          </>
        ) : (
          <>
            <Field label="Organization">
              <Input required value={form.organization} onChange={(event) => setForm({ ...form, organization: event.target.value })} />
            </Field>
            <Field label="Service token">
              <Input required type="password" value={form.token} onChange={(event) => setForm({ ...form, token: event.target.value })} />
            </Field>
          </>
        )}
        <SettingsFormActions>
          <Button type="submit" disabled={!canMutate(data)}>Save connection</Button>
        </SettingsFormActions>
      </FormStack>
    </>
  );
}
