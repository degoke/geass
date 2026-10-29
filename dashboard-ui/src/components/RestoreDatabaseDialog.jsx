import { useEffect, useState } from "react";
import { Group, Modal, Stack, Text } from "@mantine/core";
import { useNavigate } from "@tanstack/react-router";
import { action, list, resourceName } from "@/lib/api";
import { notify } from "@/lib/notify";
import {
  archiveDisplayName,
  formatDateTime,
  isSnapshotArchive,
  parseTime,
  recoveryWindow,
  restorePoints,
  toLocalInputValue,
} from "@/lib/backups";
import { Button, Field, Input, Muted, Select } from "@/components/geass-ui";

const MINUTE = 60 * 1000;

/** The first whole minute inside the window, and the last, as datetime-local inputs need. */
function inputBounds(recovery) {
  if (!recovery) return { min: undefined, max: undefined };
  return {
    min: toLocalInputValue(new Date(Math.ceil(recovery.start.getTime() / MINUTE) * MINUTE)),
    max: toLocalInputValue(new Date(Math.floor(recovery.end.getTime() / MINUTE) * MINUTE)),
  };
}

/**
 * Confirms a restore and starts it. A restore always creates a new database next to the original.
 *
 * `target` fixes what to restore to: `{ type: "latest" }`, `{ type: "point", id }` or
 * `{ type: "time", value }` (a datetime-local value). Without it the dialog lets the person choose.
 */
export function RestoreDatabaseDialog({ archive, target, data, reload, onClose }) {
  const navigate = useNavigate();
  const archiveName = archive ? resourceName(archive) : "";
  const points = restorePoints(archive);
  // A snapshot backup holds copies, not a change log, so there is no moment between them to pick.
  const snapshot = isSnapshotArchive(archive);
  const recovery = snapshot ? null : recoveryWindow(archive);
  const { min, max } = inputBounds(recovery);
  const project = list(data, "projects").find((item) => resourceName(item) === archive?.spec?.project);
  const environments = project?.spec?.environments || [];
  const originalEnvironment = archive?.spec?.environment || "";
  const needsEnvironment = Boolean(archive) && environments.length > 0 && !environments.includes(originalEnvironment);

  const [mode, setMode] = useState("latest");
  const [pointId, setPointId] = useState("");
  const [timeValue, setTimeValue] = useState("");
  const [environment, setEnvironment] = useState("");
  const [busy, setBusy] = useState(false);
  const [problem, setProblem] = useState("");

  useEffect(() => {
    setMode(target?.type || "latest");
    setPointId(target?.type === "point" ? target.id : "");
    setTimeValue(target?.type === "time" ? target.value : "");
    setEnvironment(environments.includes(originalEnvironment) ? originalEnvironment : environments[0] || "");
    setProblem("");
    // Reset only when a different restore is opened, not when the data refreshes behind the dialog.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [archiveName, target]);

  const close = () => {
    if (!busy) onClose();
  };

  const changeMode = (next) => {
    setMode(next);
    setProblem("");
    if (next === "point" && !pointId) setPointId(points[0]?.id || "");
    if (next === "time" && !timeValue && max) setTimeValue(max);
  };

  const submit = async (event) => {
    event.preventDefault();
    const values = { archive: archiveName };
    if (mode === "point") {
      if (!pointId) {
        setProblem("Choose a restore point");
        return;
      }
      values.restorePoint = pointId;
    }
    if (mode === "time") {
      const chosen = timeValue ? new Date(timeValue) : null;
      if (!chosen || Number.isNaN(chosen.getTime())) {
        setProblem("Enter a valid date and time");
        return;
      }
      if (recovery && (chosen < recovery.start || chosen > recovery.end)) {
        setProblem(`Choose a time between ${formatDateTime(recovery.start)} and ${formatDateTime(recovery.end)}`);
        return;
      }
      values.time = chosen.toISOString().replace(/\.\d{3}Z$/, "Z");
    }
    if (needsEnvironment && environment) values.environment = environment;

    setProblem("");
    setBusy(true);
    try {
      const result = await action("/databases/restore", values);
      await Promise.resolve(reload?.()).catch(() => {});
      onClose();
      notify("Restore started", "success");
      if (result?.name) {
        navigate({
          to: `/projects/${archive.spec.project}/databases/${result.name}`,
          search: { environment: values.environment || originalEnvironment, view: "deployments" },
        });
      }
    } catch (error) {
      // In the dialog, where the person is looking: the cluster refuses a restore it has no room for, and says why, and a
      // notice that opens behind the dialog is not seen.
      setProblem(error.message);
    } finally {
      setBusy(false);
    }
  };

  const fixedSummary = () => {
    if (target?.type === "point") {
      const point = points.find((entry) => entry.id === target.id);
      return point ? `the restore point from ${formatDateTime(point.time)}` : "the chosen restore point";
    }
    if (target?.type === "time") {
      const moment = parseTime(target.value);
      return moment ? formatDateTime(moment) : "the chosen time";
    }
    return snapshot ? "the latest backup" : "the most recent state";
  };

  return (
    <Modal opened={Boolean(archive)} onClose={close} title="Restore to a new database" centered>
      {archive ? (
        <form onSubmit={submit}>
          <Stack gap="sm">
            <Text size="sm">
              Restoring <strong>{archiveDisplayName(archive)}</strong>
              {originalEnvironment ? ` (${originalEnvironment})` : ""}
            </Text>
            {archive.spec?.deleteAfter ? (
              <Muted size="sm">These backups are kept until {formatDateTime(archive.spec.deleteAfter)}.</Muted>
            ) : null}

            {target ? (
              <Text size="sm">
                Restore to: <strong>{fixedSummary()}</strong>
              </Text>
            ) : (
              <>
                <Field label="Restore to">
                  <Select value={mode} onChange={(event) => changeMode(event.currentTarget.value)} aria-label="Restore to">
                    <option value="latest">{snapshot ? "The latest backup" : "The most recent state"}</option>
                    {points.length ? <option value="point">A restore point</option> : null}
                    {recovery ? <option value="time">A specific time</option> : null}
                  </Select>
                </Field>
                {mode === "point" ? (
                  <Field label="Restore point">
                    <Select value={pointId} onChange={(event) => setPointId(event.currentTarget.value)} aria-label="Restore point">
                      <option value="" disabled>Choose a restore point</option>
                      {points.map((point) => (
                        <option key={point.id} value={point.id}>{formatDateTime(point.time)}</option>
                      ))}
                    </Select>
                  </Field>
                ) : null}
                {mode === "time" && recovery ? (
                  <Field
                    label="Time"
                    description={`Between ${formatDateTime(recovery.start)} and ${formatDateTime(recovery.end)}, in your local time`}
                  >
                    <Input
                      type="datetime-local"
                      className="rw-backup-time"
                      value={timeValue}
                      min={min}
                      max={max}
                      onChange={(event) => setTimeValue(event.currentTarget.value)}
                      aria-label="Restore time"
                    />
                  </Field>
                ) : null}
              </>
            )}

            {needsEnvironment ? (
              <Field label="Environment" description={`The ${originalEnvironment} environment no longer exists`}>
                <Select value={environment} onChange={(event) => setEnvironment(event.currentTarget.value)} aria-label="Environment">
                  {environments.map((env) => <option key={env} value={env}>{env}</option>)}
                </Select>
              </Field>
            ) : null}

            <Text size="sm">Creates a new database. Your current one is not changed.</Text>
            {problem ? <Text size="sm" c="red">{problem}</Text> : null}
            <Group justify="flex-end" gap="sm">
              <Button type="button" variant="outline" disabled={busy} onClick={close}>Cancel</Button>
              <Button type="submit" loading={busy} disabled={(mode === "time" && !timeValue) || (mode === "point" && !pointId)}>
                Restore
              </Button>
            </Group>
          </Stack>
        </form>
      ) : null}
    </Modal>
  );
}
