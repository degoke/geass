/** Native form controls styled like Railway project settings (no Mantine input chrome). */

export function RailField({ label, hint, children, className }) {
  return (
    <div className={["rw-rail-field", className].filter(Boolean).join(" ")}>
      {label ? <span className="rw-rail-label">{label}</span> : null}
      {children}
      {hint ? <span className="rw-rail-hint">{hint}</span> : null}
    </div>
  );
}

export function RailInput({ className, ...props }) {
  return <input className={["rw-rail-input", className].filter(Boolean).join(" ")} {...props} />;
}

export function RailTextarea({ className, rows = 4, ...props }) {
  return <textarea className={["rw-rail-textarea", className].filter(Boolean).join(" ")} rows={rows} {...props} />;
}

export function RailSelect({ className, children, ...props }) {
  return (
    <select className={["rw-rail-select", className].filter(Boolean).join(" ")} {...props}>
      {children}
    </select>
  );
}

export function RailCopyField({ label, value, onCopy, "aria-label": ariaLabel }) {
  return (
    <RailField label={label}>
      <div className="rw-rail-copy">
        <input className="rw-rail-input rw-rail-input-readonly" readOnly value={value} aria-label={ariaLabel || label} />
        <button type="button" className="rw-rail-copy-btn" onClick={onCopy} aria-label={`Copy ${label}`}>
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" aria-hidden="true">
            <rect x="9" y="9" width="13" height="13" rx="2" />
            <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1" />
          </svg>
        </button>
      </div>
    </RailField>
  );
}
