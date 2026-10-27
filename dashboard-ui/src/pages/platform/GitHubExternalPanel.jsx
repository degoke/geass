import { Badge, DataTable, Muted } from "@/components/geass-ui";
import { SettingsAlert, SettingsPanel } from "@/layout/SettingsShell";

const TOKEN_TONE = { Valid: "success", ExpiringSoon: "warning", Expired: "danger", Missing: "danger" };
const TOKEN_LABEL = { Valid: "Token valid", ExpiringSoon: "Due for renewal", Expired: "Token expired", Missing: "No token yet" };

function expiry(value) {
  if (!value) return "";
  const at = new Date(value);
  return Number.isNaN(at.getTime()) ? "" : at.toLocaleString();
}

// The cluster's GitHub App is run by a provider outside it (rungeass): there is nothing to register here. What is shown
// is what the provider has given the cluster: the GitHub accounts it is installed on, and whether Geass holds a token for
// each. No token or secret ever reaches the page.
export function GitHubExternalPanel({ external }) {
  const installations = external.installations || [];
  return (
    <SettingsPanel variant="flat">
      <h3 className="rw-settings-section-title">GitHub is provided by {external.provider}</h3>
      <p className="rw-rail-body">
        {external.provider} runs the GitHub App{external.appSlug ? ` (${external.appSlug})` : ""} and holds its key. It gives this
        cluster a token for each installation and forwards GitHub&apos;s events, so there is no App to set up here.
        {external.installURL ? (
          <>
            {" "}Install the App on another account at <a href={external.installURL} target="_blank" rel="noreferrer">{external.provider}</a>.
          </>
        ) : null}
      </p>
      {!external.ready ? (
        <SettingsAlert tone="danger">The secret {external.provider} signs forwarded events with is missing, so GitHub&apos;s events are refused.</SettingsAlert>
      ) : null}
      {external.tokensReady === "False" ? <SettingsAlert tone="warning">{external.tokensMessage}</SettingsAlert> : null}
      <DataTable
        columns={[
          {
            label: "Account",
            render: (row) => (
              <span>
                {row.account}
                <Muted size="xs">{[row.accountType, `installation ${row.id}`, row.org ? `organization ${row.org}` : ""].filter(Boolean).join(" · ")}</Muted>
              </span>
            ),
          },
          { label: "Projects", render: (row) => (row.projects || []).length },
          {
            label: "Token",
            align: "right",
            render: (row) => (
              <span>
                <Badge tone={TOKEN_TONE[row.tokenState] || "neutral"}>{TOKEN_LABEL[row.tokenState] || row.tokenState}</Badge>
                {row.tokenExpiresAt ? <Muted size="xs">until {expiry(row.tokenExpiresAt)}</Muted> : null}
              </span>
            ),
          },
        ]}
        rows={installations}
        getRowKey={(row) => String(row.id)}
        empty={`${external.provider} has not given this cluster an installation yet.`}
      />
    </SettingsPanel>
  );
}
