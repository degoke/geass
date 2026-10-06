import { Field, Select } from "@/components/geass-ui";

export function SizeFields({ cpu, memory, onChange, data }) {
  const cpuSizes = data?.platform?.capacity?.cpuSizes || [
    { value: "100m", label: "0.1 CPU" },
    { value: "250m", label: "0.25 CPU" },
    { value: "500m", label: "0.5 CPU" },
    { value: "1", label: "1 CPU" },
    { value: "2", label: "2 CPU" },
  ];
  const memorySizes = data?.platform?.capacity?.memorySizes || [
    { value: "128Mi", label: "128 MB" },
    { value: "256Mi", label: "256 MB" },
    { value: "512Mi", label: "512 MB" },
    { value: "1Gi", label: "1 GB" },
    { value: "2Gi", label: "2 GB" },
    { value: "4Gi", label: "4 GB" },
  ];
  return (
    <div className="size-grid">
      <Field label="CPU">
        <Select value={cpu} onChange={(event) => onChange("cpu", event.target.value)}>
          {cpuSizes.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}
        </Select>
      </Field>
      <Field label="Memory">
        <Select value={memory} onChange={(event) => onChange("memory", event.target.value)}>
          {memorySizes.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}
        </Select>
      </Field>
    </div>
  );
}
