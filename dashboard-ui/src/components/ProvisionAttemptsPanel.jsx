import { Stack, Text } from "@mantine/core";
import { formatProvisionAttemptLabel, resourceProvisionAttemptsForUI } from "@/lib/services";
import { Muted } from "@/components/geass-ui";

function phaseLabel(phase) {
  if (phase === "Succeeded") return "Succeeded";
  if (phase === "Failed") return "Failed";
  return "In progress";
}

export function ProvisionAttemptsPanel({ item, alwaysShow = false }) {
  const attempts = resourceProvisionAttemptsForUI(item);

  return (
    <Stack gap="sm" className="rw-provision-attempts">
      <Text size="sm" fw={600}>Deploy history</Text>
      {!attempts.length ? (
        <Muted size="sm">
          {alwaysShow
            ? "No deploys recorded yet. Deploy from the project bar or use Retry after a failure to start history."
            : "No deploy history yet."}
        </Muted>
      ) : null}
      {attempts.length ? (
      <ul className="rw-provision-attempts-list">
        {attempts.map((attempt) => {
          const errors = attempt.errors || [];
          const lastError = errors.length ? errors[errors.length - 1].message : "";
          return (
            <li key={attempt.id} className="rw-provision-attempts-item">
              <div className="rw-provision-attempts-head">
                <Text size="sm" fw={500}>{formatProvisionAttemptLabel(attempt)}</Text>
                <Muted size="xs">{phaseLabel(attempt.phase)}</Muted>
              </div>
              {attempt.summary && attempt.phase !== "Running" ? <Muted size="sm">{attempt.summary}</Muted> : null}
              {attempt.buildRef ? <Muted size="xs">Build: {attempt.buildRef}</Muted> : null}
              {attempt.deploymentRef ? <Muted size="xs">Release: {attempt.deploymentRef}</Muted> : null}
              {errors.length ? (
                <ul className="rw-provision-attempts-errors">
                  {errors.map((entry, index) => (
                    <li key={`${attempt.id}-${index}`}>
                      <Muted size="xs">{entry.message}</Muted>
                    </li>
                  ))}
                </ul>
              ) : lastError ? (
                <Muted size="sm">{lastError}</Muted>
              ) : null}
            </li>
          );
        })}
      </ul>
      ) : null}
    </Stack>
  );
}
