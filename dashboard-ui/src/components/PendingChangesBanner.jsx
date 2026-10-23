import { action } from "@/lib/api";
import { canMutate } from "@/lib/dashboard";
import { alertUser, alertError } from "@/lib/notify";
import { deployPathForKind, pendingChangeCopy } from "@/lib/services";
import { Button, PendingBanner } from "@/components/geass-ui";

export function PendingChangesBanner({ item, kind, name, reload, data }) {
  const copy = pendingChangeCopy(item);
  if (!copy) return null;
  const deployPath = deployPathForKind(kind, name);
  return (
    <PendingBanner
      message={copy.message}
      action={canMutate(data) ? (
        <Button
          size="sm"
          onClick={() => action(deployPath).then(() => reload?.()).then(() => alertUser("Deployed")).catch((error) => alertError(error.message))}
        >
          {copy.action}
        </Button>
      ) : null}
    />
  );
}
