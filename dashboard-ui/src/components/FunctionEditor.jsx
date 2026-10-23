import { useEffect, useMemo, useRef, useState } from "react";
import { action } from "@/lib/api";
import { canMutate } from "@/lib/dashboard";
import { FUNCTION_RUNTIMES, functionRuntime } from "@/lib/functions";
import { alertUser, alertError } from "@/lib/notify";
import { Button, Input, Muted, Select } from "@/components/geass-ui";

const INDENT = "  ";

/**
 * Source code tab for a function service. Code is saved to the service as a pending change and
 * goes live with the next deploy, like any other setting.
 */
export function FunctionEditor({ item, name, data, reload }) {
  const fn = item.spec?.source?.function;
  const mutable = canMutate(data);
  const savedCode = fn?.code || "";
  const savedRuntime = fn?.runtime || "bun";
  const savedMode = fn?.mode || "http";
  const [code, setCode] = useState(savedCode);
  const [runtime, setRuntime] = useState(savedRuntime);
  const [mode, setMode] = useState(savedMode);
  const [saving, setSaving] = useState(false);
  const editorRef = useRef(null);
  const gutterRef = useRef(null);
  const dirty = code !== savedCode || runtime !== savedRuntime || mode !== savedMode;

  // Follow the saved copy (for example after a save) unless there are unsaved edits.
  useEffect(() => {
    if (!dirty) {
      setCode(savedCode);
      setRuntime(savedRuntime);
      setMode(savedMode);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [savedCode, savedRuntime, savedMode]);

  const lineCount = useMemo(() => Math.max(1, code.split("\n").length), [code]);
  const file = functionRuntime(runtime)?.file || "index.ts";

  const save = () => {
    if (!dirty || saving || !mutable) return;
    setSaving(true);
    action(`/apps/${name}/update`, { functionRuntime: runtime, functionCode: code, functionMode: mode })
      .then(() => { reload?.(); alertUser("Code saved"); })
      .catch((error) => alertError(error.message))
      .finally(() => setSaving(false));
  };

  const onKeyDown = (event) => {
    if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "s") {
      event.preventDefault();
      save();
      return;
    }
    if (event.key === "Tab" && !event.shiftKey) {
      event.preventDefault();
      const field = event.currentTarget;
      const { selectionStart: start, selectionEnd: end } = field;
      const next = code.slice(0, start) + INDENT + code.slice(end);
      setCode(next);
      requestAnimationFrame(() => {
        field.selectionStart = field.selectionEnd = start + INDENT.length;
      });
    }
  };

  if (!fn) return null;
  if (!mutable) {
    return <Muted size="sm" className="rw-console-note">Your viewer role cannot view function code.</Muted>;
  }

  return (
    <div className="rw-code">
      <div className="rw-code-bar">
        <span className="rw-code-file">{file}{dirty ? <i className="rw-code-dirty" title="Unsaved changes" /> : null}</span>
        <Select value={runtime} onChange={(event) => setRuntime(event.currentTarget.value)} aria-label="Runtime">
          {FUNCTION_RUNTIMES.map((option) => <option key={option.id} value={option.id}>{option.label}</option>)}
        </Select>
        <Select value={mode} onChange={(event) => setMode(event.currentTarget.value)} aria-label="Mode">
          <option value="http">Server (serves HTTP)</option>
          <option value="event">Handler (gets an event)</option>
        </Select>
        <span className="rw-code-spacer" />
        <Button variant="outline" size="sm" disabled={!dirty} onClick={() => { setCode(savedCode); setRuntime(savedRuntime); setMode(savedMode); }}>Discard</Button>
        <Button size="sm" loading={saving} disabled={!dirty} onClick={save}>Save</Button>
      </div>
      <div className="rw-code-editor">
        <div className="rw-code-gutter" ref={gutterRef} aria-hidden>
          {Array.from({ length: lineCount }, (_, index) => <span key={index}>{index + 1}</span>)}
        </div>
        <textarea
          ref={editorRef}
          className="rw-code-input"
          value={code}
          spellCheck={false}
          autoCapitalize="off"
          autoCorrect="off"
          aria-label="Function source code"
          onChange={(event) => setCode(event.currentTarget.value)}
          onKeyDown={onKeyDown}
          onScroll={(event) => { if (gutterRef.current) gutterRef.current.scrollTop = event.currentTarget.scrollTop; }}
        />
      </div>
      <Muted size="xs">
        {mode === "event" ? (
          <>
            Export <code>handler(event)</code>. The event is <code>{"{ method, path, query, headers, body, trigger }"}</code>; return an object, text, or <code>{"{ statusCode, headers, body }"}</code>.
          </>
        ) : (
          <>Serve HTTP on the port in <code>PORT</code>.</>
        )}
      </Muted>
      <FunctionTriggers item={item} name={name} reload={reload} />
    </div>
  );
}

/** Schedules that call the function. They are saved with the service and start with the next deploy. */
function FunctionTriggers({ item, name, reload }) {
  const saved = item.spec?.source?.function?.triggers || [];
  const savedKey = JSON.stringify(saved);
  const [triggers, setTriggers] = useState(saved);
  const [saving, setSaving] = useState(false);
  const dirty = JSON.stringify(triggers) !== savedKey;

  useEffect(() => {
    if (!dirty) setTriggers(saved);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [savedKey]);

  const change = (index, key, value) => setTriggers((rows) => rows.map((row, i) => (i === index ? { ...row, [key]: value } : row)));
  const add = () => setTriggers((rows) => [...rows, { name: `schedule-${rows.length + 1}`, schedule: "0 * * * *" }]);
  const remove = (index) => setTriggers((rows) => rows.filter((_, i) => i !== index));
  const save = () => {
    setSaving(true);
    const clean = triggers.map(({ name: triggerName, schedule, path, payload }) => ({
      name: triggerName, schedule, ...(path ? { path } : {}), ...(payload ? { payload } : {}),
    }));
    action(`/apps/${name}/update`, { functionTriggers: clean.length ? JSON.stringify(clean) : "" })
      .then(() => { reload?.(); alertUser("Schedules saved"); })
      .catch((error) => alertError(error.message))
      .finally(() => setSaving(false));
  };

  return (
    <div className="rw-code-triggers">
      <h4>Schedules</h4>
      <Muted size="xs">Cron, in UTC.</Muted>
      {triggers.map((trigger, index) => (
        <div className="rw-code-trigger" key={index}>
          <Input value={trigger.name} onChange={(event) => change(index, "name", event.currentTarget.value)} aria-label="Schedule name" placeholder="nightly" />
          <Input value={trigger.schedule} onChange={(event) => change(index, "schedule", event.currentTarget.value)} aria-label="Cron schedule" placeholder="0 3 * * *" />
          <Input value={trigger.path || ""} onChange={(event) => change(index, "path", event.currentTarget.value)} aria-label="Path" placeholder="/" />
          <Button variant="outline" size="sm" onClick={() => remove(index)}>Remove</Button>
        </div>
      ))}
      <div className="rw-code-trigger-actions">
        <Button variant="outline" size="sm" onClick={add} disabled={triggers.length >= 8}>Add schedule</Button>
        <Button size="sm" loading={saving} disabled={!dirty} onClick={save}>Save schedules</Button>
      </div>
    </div>
  );
}
