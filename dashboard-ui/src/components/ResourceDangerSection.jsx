import { useState } from "react";
import { Group, Modal, Text } from "@mantine/core";
import { AlertTriangle, Trash2 } from "lucide-react";
import { action, resourceDisplayName, resourceName } from "@/lib/api";
import { alertUser } from "@/lib/notify";
import { backupSpec, supportsBackups } from "@/lib/backups";
import { isResourceDeleting } from "@/lib/services";
import { Button, Field, Input, Muted } from "@/components/geass-ui";
import { ResourceSettingsBlock, ResourceSettingsSection } from "@/layout/ResourceSettingsLayout";
import { SettingsAlert, SettingsFormActions } from "@/layout/SettingsShell";

const KIND_LABEL = {
  apps: "service",
  "object-stores": "bucket",
  "logical-databases": "logical database",
  databases: "database",
};

const BACKED_UP_STATES = ["Protected", "AtRisk", "Starting"];

/** What deleting a Postgres database does to its data, depending on whether it is backed up. */
function deleteBackupNote(item) {
  if (backupSpec(item).enabled && BACKED_UP_STATES.includes(item.status?.backup?.state)) {
    return "Geass takes a final backup first and keeps it for 7 days, so you can restore this database from Project settings → Backups.";
  }
  return "This database has no backups. Deleting it permanently destroys its data.";
}

export function ResourceDangerSection({ item, kind, name, project, reload, navigate, onClose }) {
  const [confirmDelete, setConfirmDelete] = useState("");
  const [modalOpen, setModalOpen] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const displayLabel = resourceDisplayName(item);
  const confirmed = confirmDelete.trim() === name;
  const terminating = isResourceDeleting(item);
  const kindLabel = KIND_LABEL[kind] || "resource";
  const backupNote = kind === "databases" && supportsBackups(item) ? deleteBackupNote(item) : "";

  const returnToWorkspace = () => {
    if (onClose) {
      onClose();
      return;
    }
    navigate({
      to: `/projects/${resourceName(project)}`,
      search: item.spec?.environment ? { environment: item.spec.environment } : undefined,
    });
  };

  const deleteResource = () => {
    setSubmitting(true);
    action(`/${kind}/${name}/delete`, { confirmName: name })
      .then(() => reload?.())
      .then(() => {
        setModalOpen(false);
        setConfirmDelete("");
        returnToWorkspace();
        alertUser("Resource deleted");
      })
      .catch((error) => alertUser(error.message))
      .finally(() => setSubmitting(false));
  };

  return (
    <ResourceSettingsSection id="danger" icon={AlertTriangle} title={`Delete ${kindLabel}`}>
      <ResourceSettingsBlock>
        <SettingsAlert tone="danger">
          {terminating
            ? "Deletion is in progress. This resource will disappear from the project when cleanup finishes."
            : `Deleting this ${kindLabel} permanently removes it from Geass. Deployed workloads and data may be removed depending on the resource type. This cannot be undone.`}
        </SettingsAlert>
        {!terminating ? (
          <>
            {backupNote ? <p className="rw-rail-body">{backupNote}</p> : null}
            <Field
              label={`Type ${name} to delete`}
              description={
                displayLabel !== name
                  ? `Use the resource ID (${name}), not the display name (${displayLabel}).`
                  : undefined
              }
            >
              <Input
                value={confirmDelete}
                onChange={(event) => setConfirmDelete(event.target.value)}
                autoComplete="off"
                placeholder={name}
                disabled={submitting}
                aria-label="Confirm resource ID"
              />
            </Field>
            <SettingsFormActions>
              <Button
                type="button"
                variant="danger"
                disabled={submitting || !confirmed}
                onClick={() => setModalOpen(true)}
              >
                <Trash2 size={15} /> Delete {kindLabel}
              </Button>
            </SettingsFormActions>
          </>
        ) : (
          <Muted size="sm">No further action is required while deletion runs.</Muted>
        )}
        <Modal
          opened={modalOpen}
          onClose={() => { if (!submitting) setModalOpen(false); }}
          title="Delete resource permanently?"
          centered
          classNames={{ content: "rw-danger-modal" }}
        >
          <Text size="sm" mb="md">
            This will permanently delete the {kindLabel} <strong>{displayLabel}</strong> ({name}) from project{" "}
            <strong>{item.spec?.project}</strong>
            {item.spec?.environment ? ` (${item.spec?.environment})` : ""}. Deployed workloads and data may be removed
            depending on the resource type. This action cannot be undone.
          </Text>
          <Group justify="flex-end" gap="sm">
            <Button type="button" variant="outline" disabled={submitting} onClick={() => setModalOpen(false)}>
              Cancel
            </Button>
            <Button type="button" variant="danger" loading={submitting} onClick={deleteResource}>
              Delete permanently
            </Button>
          </Group>
        </Modal>
      </ResourceSettingsBlock>
    </ResourceSettingsSection>
  );
}
