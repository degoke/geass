import { useEffect, useRef, useState } from "react";
import { Title } from "@mantine/core";
import { action } from "@/lib/api";
import { alertUser } from "@/lib/notify";

export function EditableResourceTitle({ kind, name, value, canEdit, onSaved }) {
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(value);
  const inputRef = useRef(null);

  useEffect(() => {
    setDraft(value);
  }, [value]);

  useEffect(() => {
    if (editing) inputRef.current?.focus();
  }, [editing]);

  const commit = () => {
    const next = draft.trim();
    setEditing(false);
    if (next === value.trim()) return;
    action(`/${kind}/${name}/display-name`, { displayName: next })
      .then(() => onSaved?.())
      .catch((error) => {
        setDraft(value);
        alertUser(error.message);
      });
  };

  if (!canEdit) {
    return <Title order={1}>{value}</Title>;
  }

  if (!editing) {
    return (
      <Title
        order={1}
        className="rw-resource-display-name"
        tabIndex={0}
        role="button"
        aria-label="Edit display name"
        onClick={() => setEditing(true)}
        onKeyDown={(event) => {
          if (event.key === "Enter" || event.key === " ") {
            event.preventDefault();
            setEditing(true);
          }
        }}
      >
        {value}
      </Title>
    );
  }

  return (
    <input
      ref={inputRef}
      className="rw-resource-display-name-input"
      value={draft}
      aria-label="Display name"
      onChange={(event) => setDraft(event.target.value)}
      onBlur={commit}
      onKeyDown={(event) => {
        if (event.key === "Enter") {
          event.preventDefault();
          commit();
        }
        if (event.key === "Escape") {
          event.preventDefault();
          setDraft(value);
          setEditing(false);
        }
      }}
    />
  );
}
