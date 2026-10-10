import { useEffect, useMemo, useRef, useState } from "react";
import { action } from "@/lib/api";
import { canMutate } from "@/lib/dashboard";
import { FUNCTION_RUNTIMES, functionRuntime } from "@/lib/functions";
import { alertUser } from "@/lib/notify";
import { Button, Muted, Select } from "@/components/geass-ui";

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
  const [code, setCode] = useState(savedCode);
  const [runtime, setRuntime] = useState(savedRuntime);
  const [saving, setSaving] = useState(false);
  const editorRef = useRef(null);
  const gutterRef = useRef(null);
  const dirty = code !== savedCode || runtime !== savedRuntime;

  // Follow the saved copy (for example after a save) unless there are unsaved edits.
  useEffect(() => {
    if (!dirty) {
      setCode(savedCode);
      setRuntime(savedRuntime);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [savedCode, savedRuntime]);

  const lineCount = useMemo(() => Math.max(1, code.split("\n").length), [code]);
  const file = functionRuntime(runtime)?.file || "index.ts";

  const save = () => {
    if (!dirty || saving || !mutable) return;
    setSaving(true);
    action(`/apps/${name}/update`, { functionRuntime: runtime, functionCode: code })
      .then(() => { reload?.(); alertUser("Code saved. Deploy to apply it."); })
      .catch((error) => alertUser(error.message))
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
        <span className="rw-code-spacer" />
        <Button variant="outline" size="sm" disabled={!dirty} onClick={() => { setCode(savedCode); setRuntime(savedRuntime); }}>Discard</Button>
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
        Your code must serve HTTP on the port in the <code>PORT</code> environment variable. Changes apply on the next deploy. {mutable ? "Press ⌘/Ctrl+S to save." : ""}
      </Muted>
    </div>
  );
}
