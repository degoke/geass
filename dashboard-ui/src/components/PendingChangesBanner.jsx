import { action } from "@/lib/api";
import { canMutate } from "@/lib/dashboard";
import { alertUser } from "@/lib/notify";
import { deployPathForKind, pendingChangeCopy } from "@/lib/services";
import { Button, PendingBanner } from "@/components/geass-ui";

export function PendingChangesBanner({ item, kind, name, reload, data }) {
  const copy = pendingChangeCopy(item);
  if (!copy) return null;
  const deployPath = deployPathForKind(kind, name);
  const message = copy.detail ? `${copy.message}. ${copy.detail}` : copy.message;
  return (
    <PendingBanner
      message={message}
      action={canMutate(data) ? (
        <Button
          size="sm"
          onClick={() => action(deployPath).then(() => reload?.()).then(() => alertUser("Deployed")).catch((error) => alertUser(error.message))}
        >
          {copy.action}
        </Button>
      ) : null}
    />
  );
}
