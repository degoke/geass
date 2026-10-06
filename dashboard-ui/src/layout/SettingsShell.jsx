import { createContext, useContext } from "react";
import { Link } from "@tanstack/react-router";
import { Drawer } from "@mantine/core";

export const SettingsRailContext = createContext(false);

export function useSettingsRail() {
  return useContext(SettingsRailContext);
}

/**
 * Railway-style settings: page title + left icon nav + scrollable content.
 */
export function SettingsShell({ title, nav, children }) {
  return (
    <SettingsRailContext.Provider value={true}>
    <div className="rw-settings-shell rw-rail-settings">
      <h1 className="rw-settings-title">{title}</h1>
      <div className="rw-settings-layout">
        <nav className="rw-settings-nav" aria-label={`${title} sections`}>
          {nav.map((item) => {
            const className = [
              "rw-settings-nav-item",
              item.active ? "is-active" : "",
              item.danger ? "is-danger" : "",
            ]
              .filter(Boolean)
              .join(" ");
            const inner = (
              <>
                {item.icon ? <item.icon size={16} strokeWidth={1.75} aria-hidden="true" /> : null}
                <span>{item.label}</span>
              </>
            );
            if (item.to) {
              return (
                <Link
                  key={item.id}
                  to={item.to}
                  search={item.search}
                  className={className}
                  aria-current={item.active ? "page" : undefined}
                >
                  {inner}
                </Link>
              );
            }
            return (
              <button
                key={item.id}
                type="button"
                className={className}
                aria-current={item.active ? "true" : undefined}
                onClick={item.onClick}
              >
                {inner}
              </button>
            );
          })}
        </nav>
        <div className="rw-settings-main">{children}</div>
      </div>
    </div>
    </SettingsRailContext.Provider>
  );
}

export function SettingsDivider() {
  return <hr className="rw-settings-divider" />;
}

export function SettingsSection({ title, description, children, actions, flat = false }) {
  return (
    <section className={flat ? "rw-settings-section is-flat" : "rw-settings-section"}>
      {(title || description || actions) && (
        <header className="rw-settings-section-head">
          <div>
            {title ? <h2 className="rw-settings-section-title">{title}</h2> : null}
            {description ? <p className="rw-settings-section-desc">{description}</p> : null}
          </div>
          {actions || null}
        </header>
      )}
      {children}
    </section>
  );
}

export function SettingsPanel({ children, className = "", variant = "card" }) {
  const variantClass = variant === "flat" ? " rw-settings-panel-flat" : "";
  return <div className={`rw-settings-panel${variantClass}${className ? ` ${className}` : ""}`}>{children}</div>;
}

export function SettingsAlert({ tone = "danger", children }) {
  return <div className={`rw-settings-alert is-${tone}`}>{children}</div>;
}

export function SettingsFormActions({ children, className }) {
  return <div className={["rw-form-actions", className].filter(Boolean).join(" ")}>{children}</div>;
}

/** Side sheet for connector / integration configuration (Railway-style). */
export function SettingsSheet({ opened, onClose, title, description, children }) {
  return (
    <Drawer
      opened={opened}
      onClose={onClose}
      title={title}
      position="right"
      size="40rem"
      padding="lg"
      classNames={{
        content: "rw-settings-sheet",
        header: "rw-settings-sheet-header",
        body: "rw-settings-sheet-body",
        title: "rw-settings-sheet-title",
      }}
    >
      <SettingsRailContext.Provider value={true}>
        <div className="rw-rail-settings rw-settings-sheet-inner">
          {description ? <p className="rw-rail-body rw-settings-sheet-desc">{description}</p> : null}
          {children}
        </div>
      </SettingsRailContext.Provider>
    </Drawer>
  );
}
