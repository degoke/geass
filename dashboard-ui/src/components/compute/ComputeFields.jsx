import { useRef } from "react";
import { X } from "lucide-react";
import { Button, Field, Input, Muted, Select, Switch } from "@/components/geass-ui";
import { Optional, Required } from "@/components/compute/ComputeBits";
import { formatMemoryMiB, formatPrice, parseList } from "@/lib/compute";
import { parseTagList } from "@/lib/placement";

const MAX_ROWS = 60;

/** A message as a sentence: capitalised and ended with a full stop. */
function sentence(text) {
  const clean = String(text).trim().replace(/[.\s]+$/, "");
  return clean ? `${clean[0].toUpperCase()}${clean.slice(1)}.` : "";
}

/**
 * Picks a machine size. The cloud's own list is shown with what each size is and costs, and a size can also be typed: a
 * cloud that does not list one, or a list that cannot be loaded, must not stop anyone from naming a size.
 * `query` is the react-query result of useSizes.
 */
export function SizePicker({ label = "Size", value, onChange, query, connection, disabled, required, hint, inputId, quiet = false }) {
  const sizes = query?.data || [];
  const listRef = useRef(null);
  const exact = sizes.some((size) => size.id === value);
  const filter = exact ? "" : value.trim().toLowerCase();
  const matches = filter
    ? sizes.filter((size) => `${size.id} ${size.description || ""}`.toLowerCase().includes(filter))
    : sizes;
  const shown = matches.slice(0, MAX_ROWS);
  const selected = sizes.find((size) => size.id === value);

  const moveFocus = (event, from) => {
    if (event.key !== "ArrowDown" && event.key !== "ArrowUp") return;
    const buttons = Array.from(listRef.current?.querySelectorAll("button.cp-size") || []);
    if (!buttons.length) return;
    event.preventDefault();
    const index = buttons.indexOf(document.activeElement);
    let next;
    if (from === "input") next = event.key === "ArrowDown" ? 0 : -1;
    else next = event.key === "ArrowDown" ? Math.min(buttons.length - 1, index + 1) : index - 1;
    if (next < 0) document.getElementById(inputId)?.focus();
    else buttons[next].focus();
  };

  let note = null;
  if (quiet) note = null;
  else if (!connection) note = "Choose a connection to see the sizes its cloud offers. You can also type a size.";
  else if (query?.isPending && query?.fetchStatus !== "idle") note = "Asking the cloud for its sizes…";
  else if (query?.isError) note = `${sentence(query.error?.message || "The cloud did not list its sizes")} You can still type a size.`;
  else if (!sizes.length && connection) note = "The cloud listed no sizes for this region. You can still type one.";

  return (
    <Field label={<>{label}{required ? <Required /> : null}</>} description={hint}>
      <Input
        id={inputId}
        value={value}
        onChange={(event) => onChange(event.currentTarget.value)}
        onKeyDown={(event) => moveFocus(event, "input")}
        placeholder={sizes[0]?.id || "the cloud's name for a size"}
        autoComplete="off"
        spellCheck={false}
        disabled={disabled}
        aria-label={label}
      />
      {sizes.length ? (
        <div className="cp-sizes" ref={listRef} role="group" aria-label="Sizes the cloud offers">
          <div className="cp-size-list" onKeyDown={(event) => moveFocus(event, "list")}>
            <div className="cp-size-head" aria-hidden="true">
              <span>Size</span><span>vCPU</span><span>Memory</span><span>Disk</span><span>Price</span>
            </div>
            {shown.map((size) => (
              <button
                key={size.id}
                type="button"
                className="cp-size"
                aria-pressed={size.id === value}
                disabled={disabled}
                onClick={() => onChange(size.id)}
              >
                <span className="cp-size-id">
                  {size.id}
                  {size.arch && size.arch !== "amd64" && size.arch !== "x86_64" ? <small>{size.arch}</small> : null}
                </span>
                <span>{size.vcpus}</span>
                <span>{formatMemoryMiB(size.memoryMiB)}</span>
                <span>{size.diskGB ? `${size.diskGB} GB` : "—"}</span>
                <span className="cp-size-price">{formatPrice(size.hourlyPrice, size.currency) || "—"}</span>
              </button>
            ))}
            {matches.length > shown.length ? (
              <p className="cp-size-note">{matches.length - shown.length} more sizes. Type to narrow the list.</p>
            ) : null}
            {!matches.length ? <p className="cp-size-note">No listed size matches "{value}". Geass will try it as typed.</p> : null}
          </div>
        </div>
      ) : null}
      {selected ? (
        <Muted size="xs">
          {selected.vcpus} vCPU, {formatMemoryMiB(selected.memoryMiB)} of memory
          {selected.hourlyPrice ? `, ${formatPrice(selected.hourlyPrice, selected.currency)}` : ", price not stated by the cloud"}
        </Muted>
      ) : null}
      {note ? <Muted size="xs">{note}</Muted> : null}
    </Field>
  );
}

/** A list of sizes typed by commas, with a menu to add one from the cloud's list. */
export function SizeListField({ label, hint, value, onChange, sizes, disabled, max = 5 }) {
  const chosen = parseList(value);
  const options = (sizes || []).filter((size) => !chosen.includes(size.id)).slice(0, 300);
  return (
    <Field label={<>{label}<Optional /></>} description={hint}>
      <Input
        value={value}
        onChange={(event) => onChange(event.currentTarget.value)}
        placeholder={(sizes || []).slice(1, 3).map((size) => size.id).join(", ") || "one size, then another"}
        autoComplete="off"
        spellCheck={false}
        disabled={disabled}
        aria-label={label}
      />
      {options.length && chosen.length < max ? (
        <Select
          value=""
          disabled={disabled}
          aria-label={`Add to ${label}`}
          onChange={(event) => {
            const id = event.currentTarget.value;
            if (id) onChange([...chosen, id].join(", "));
          }}
        >
          <option value="">Add a size from the list…</option>
          {options.map((size) => (
            <option key={size.id} value={size.id}>
              {size.id} · {size.vcpus} vCPU · {formatMemoryMiB(size.memoryMiB)}
              {size.hourlyPrice ? ` · ${formatPrice(size.hourlyPrice, size.currency)}` : ""}
            </option>
          ))}
        </Select>
      ) : null}
    </Field>
  );
}

/**
 * The settings a cloud reads for a pool or a server (a subnet, a firewall), made from the provider's own description:
 * the ones it names, with the required ones marked, and for a provider that takes any, rows of name and value.
 * `values` is {key: text} for the named ones and `rows` is [{key, value}] for the free ones.
 */
export function ProviderSettingsFields({ descriptor, values, rows, onValues, onRows, disabled }) {
  if (!descriptor) return null;
  const named = descriptor.settings || [];
  if (!named.length && !descriptor.freeSettings) return null;
  return (
    <>
      {named.map((setting) => (
        <Field
          key={setting.key}
          label={<>{setting.label}{setting.required ? <Required /> : <Optional />}</>}
          description={setting.help}
        >
          <Input
            value={values[setting.key] || ""}
            onChange={(event) => onValues({ ...values, [setting.key]: event.currentTarget.value })}
            autoComplete="off"
            spellCheck={false}
            disabled={disabled}
            aria-label={setting.label}
            required={Boolean(setting.required)}
          />
        </Field>
      ))}
      {descriptor.freeSettings ? (
        <Field
          label={<>Settings<Optional /></>}
          description={`${descriptor.label} passes these on, as named, to the service that creates the machines.`}
        >
          <div className="cp-rows">
            {rows.map((row, index) => (
              <div className="cp-repeat is-pair" key={index}>
                <Input
                  value={row.key}
                  placeholder="name"
                  autoComplete="off"
                  spellCheck={false}
                  disabled={disabled}
                  aria-label={`Setting ${index + 1} name`}
                  onChange={(event) => onRows(rows.map((item, i) => (i === index ? { ...item, key: event.currentTarget.value } : item)))}
                />
                <Input
                  value={row.value}
                  placeholder="value"
                  autoComplete="off"
                  spellCheck={false}
                  disabled={disabled}
                  aria-label={`Setting ${index + 1} value`}
                  onChange={(event) => onRows(rows.map((item, i) => (i === index ? { ...item, value: event.currentTarget.value } : item)))}
                />
                <Button type="button" variant="ghost" size="sm" disabled={disabled} aria-label={`Remove setting ${index + 1}`} onClick={() => onRows(rows.filter((_, i) => i !== index))}>
                  <X size={14} />
                </Button>
              </div>
            ))}
            {!disabled ? (
              <Button type="button" variant="outline" size="sm" onClick={() => onRows([...rows, { key: "", value: "" }])}>
                Add a setting
              </Button>
            ) : null}
          </div>
        </Field>
      ) : null}
    </>
  );
}

/**
 * Where the nodes sit and what they are for. A reserved tag keeps every workload that does not ask for it off the
 * nodes. Optional, and left out of what is sent when empty.
 */
export function PlacementFields({ region, zone, tags, reserved, onChange, disabled }) {
  const names = parseTagList(tags);
  return (
    <>
      <div className="cp-row">
        <Field label={<>Region<Optional /></>} description="Projects pinned to a region only run on nodes in it.">
          <Input value={region} onChange={(event) => onChange({ region: event.currentTarget.value })} placeholder="eu" autoComplete="off" disabled={disabled} aria-label="Placement region" />
        </Field>
        <Field label={<>Site<Optional /></>} description="The provider or site inside the region.">
          <Input value={zone} onChange={(event) => onChange({ zone: event.currentTarget.value })} placeholder="a name for this site" autoComplete="off" disabled={disabled} aria-label="Placement site" />
        </Field>
      </div>
      <Field label={<>Tags<Optional /></>} description="What the nodes are for, separated by commas: compute, gpu.">
        <Input value={tags} onChange={(event) => onChange({ tags: event.currentTarget.value })} placeholder="compute" autoComplete="off" disabled={disabled} aria-label="Node tags" />
      </Field>
      {names.length ? (
        <div className="cp-check">
          {names.map((name) => (
            <Switch
              key={name}
              label={`Reserve the nodes for ${name}`}
              checked={reserved.includes(name)}
              disabled={disabled}
              onChange={(event) => {
                const on = event.currentTarget.checked;
                onChange({ reserved: on ? [...reserved.filter((tag) => tag !== name), name] : reserved.filter((tag) => tag !== name) });
              }}
            />
          ))}
          <p className="cp-check-hint">A reserved node only runs workloads that ask for that tag. Everything else stays off it.</p>
        </div>
      ) : null}
    </>
  );
}
