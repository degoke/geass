import { useEffect, useState } from "react";
import { Modal } from "@mantine/core";
import { Badge, Button, Field, Input } from "@/components/geass-ui";
import { SettingsRailContext } from "@/layout/SettingsShell";
import { PHASE_HELP, PHASE_TONE, poolPhase } from "@/lib/compute";
import "@/components/compute/compute.css";

/** The screens of this section are forms and tables in the settings look: native fields, flat buttons. */
export function ComputePage({ children, className }) {
  return (
    <SettingsRailContext.Provider value={true}>
      <div className={["rw-rail-settings", "cp-page", className].filter(Boolean).join(" ")}>{children}</div>
    </SettingsRailContext.Provider>
  );
}

/** A pool's phase. The word is always there, so the colour is never the only thing that says it. */
export function PhaseBadge({ pool }) {
  const phase = poolPhase(pool);
  return (
    <span title={PHASE_HELP[phase] || ""}>
      <Badge tone={PHASE_TONE[phase] || "neutral"}>{phase}</Badge>
    </span>
  );
}

const NODE_TONE = {
  Ready: "success",
  Failed: "danger",
  Error: "danger",
  Unknown: "warning",
};

export function NodePhaseBadge({ node }) {
  const phase = node.deleting ? "Removing" : node.phase || "Pending";
  return <Badge tone={NODE_TONE[node.phase] && !node.deleting ? NODE_TONE[node.phase] : "neutral"}>{phase}</Badge>;
}

/** One of a few choices shown as cards, with the chosen one marked (and announced) as pressed. */
export function Choice({ selected, title, description, onClick }) {
  return (
    <button type="button" className={`cp-choice${selected ? " is-selected" : ""}`} aria-pressed={selected} onClick={onClick}>
      <span className="cp-choice-title">{title}</span>
      <span className="cp-choice-text">{description}</span>
    </button>
  );
}

export function Required() {
  return <span className="cp-required" aria-hidden="true">*</span>;
}

export function Optional() {
  return <span className="cp-optional">optional</span>;
}

export function Alert({ tone = "warning", title, children, actions }) {
  return (
    <div className={`rw-settings-alert is-${tone} cp-alert`} role={tone === "danger" ? "alert" : "status"}>
      <div className="cp-alert-body">
        {title ? <p className="cp-alert-title">{title}</p> : null}
        {children}
      </div>
      {actions ? <div className="cp-alert-actions">{actions}</div> : null}
    </div>
  );
}

/** A callout that says in plain words what a button is about to cost or remove. */
export function Callout({ tone, children }) {
  return <p className={["cp-callout", tone ? `is-${tone}` : ""].filter(Boolean).join(" ")}>{children}</p>;
}

/**
 * Asks for the name of the thing before it is deleted. `blocked` is why it cannot be deleted now, if so: the button stays
 * off and the reason is shown, because the server would refuse it anyway.
 */
export function ConfirmDelete({ opened, title, name, confirmLabel, blocked, busy, error, onClose, onConfirm, children }) {
  const [typed, setTyped] = useState("");
  useEffect(() => {
    if (opened) setTyped("");
  }, [opened]);
  const confirmed = typed.trim() === name && !blocked;
  const submit = (event) => {
    event.preventDefault();
    if (confirmed && !busy) onConfirm();
  };
  return (
    <Modal opened={opened} onClose={() => { if (!busy) onClose(); }} title={title} centered classNames={{ content: "rw-danger-modal" }}>
      <SettingsRailContext.Provider value={true}>
        <form className="rw-rail-settings rw-rail-form" onSubmit={submit}>
          <div className="cp-danger-note">{children}</div>
          {blocked ? <Alert tone="warning">{blocked}</Alert> : null}
          {error ? <Alert tone="danger">{error}</Alert> : null}
          <Field label={`Type ${name} to confirm`}>
            <Input
              value={typed}
              onChange={(event) => setTyped(event.currentTarget.value)}
              autoComplete="off"
              placeholder={name}
              disabled={busy || Boolean(blocked)}
              aria-label={`Type ${name} to confirm`}
              data-autofocus
            />
          </Field>
          <div className="cp-confirm-actions">
            <Button type="button" variant="outline" disabled={busy} onClick={onClose}>Cancel</Button>
            <Button type="submit" variant="danger" disabled={!confirmed} loading={busy}>{confirmLabel}</Button>
          </div>
        </form>
      </SettingsRailContext.Provider>
    </Modal>
  );
}
