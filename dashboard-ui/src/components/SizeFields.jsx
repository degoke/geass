import { useEffect } from "react";
import { Field, Select } from "@/components/geass-ui";
import { cpuMillis, memoryBytes } from "@/lib/capacity";

const defaultCpuSizes = [
  { value: "100m", label: "0.1 CPU" },
  { value: "250m", label: "0.25 CPU" },
  { value: "500m", label: "0.5 CPU" },
  { value: "1", label: "1 CPU" },
  { value: "2", label: "2 CPU" },
];
const defaultMemorySizes = [
  { value: "128Mi", label: "128 MB" },
  { value: "256Mi", label: "256 MB" },
  { value: "512Mi", label: "512 MB" },
  { value: "1Gi", label: "1 GB" },
  { value: "2Gi", label: "2 GB" },
  { value: "4Gi", label: "4 GB" },
];

/** The sizes a service can be allowed to use: none below what it reserves, and the amount it has now even if it is not on offer. */
function ceilingOptions(options, reserved, amount, current) {
  const offered = options.filter((option) => amount(option.value) >= amount(reserved));
  if (current && !offered.some((option) => option.value === current)) offered.push({ value: current, label: current });
  return offered;
}

/**
 * CPU and memory of a workload. With `bursts` it also asks for the most a service may use, which is what it is held to when
 * the server has room: "" means the same as it reserves. Databases do not burst, so they never pass it.
 */
export function SizeFields({ cpu, memory, onChange, data, bursts = false, cpuMax = "", memoryMax = "" }) {
  const cpuSizes = data?.platform?.capacity?.cpuSizes || defaultCpuSizes;
  const memorySizes = data?.platform?.capacity?.memorySizes || defaultMemorySizes;

  // Reserving more than the service was allowed to use makes it the same as what it reserves.
  useEffect(() => {
    if (bursts && cpuMax && cpuMillis(cpuMax) < cpuMillis(cpu)) onChange("cpuMax", "");
    if (bursts && memoryMax && memoryBytes(memoryMax) < memoryBytes(memory)) onChange("memoryMax", "");
  }, [bursts, cpu, memory, cpuMax, memoryMax, onChange]);

  return (
    <div className="size-grid">
      <Field label={bursts ? "CPU reserved" : "CPU"}>
        <Select value={cpu} onChange={(event) => onChange("cpu", event.target.value)}>
          {cpuSizes.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}
        </Select>
      </Field>
      <Field label={bursts ? "Memory reserved" : "Memory"}>
        <Select value={memory} onChange={(event) => onChange("memory", event.target.value)}>
          {memorySizes.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}
        </Select>
      </Field>
      {bursts ? (
        <>
          <Field label="CPU can use up to">
            <Select value={cpuMax} onChange={(event) => onChange("cpuMax", event.target.value)}>
              <option value="">Same as reserved</option>
              {ceilingOptions(cpuSizes, cpu, cpuMillis, cpuMax).map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}
            </Select>
          </Field>
          <Field
            label="Memory can use up to"
            description="Reserved is always yours. The rest is used only while the server has room, so a service that goes beyond what it reserved is the first to restart if the server runs out of memory."
          >
            <Select value={memoryMax} onChange={(event) => onChange("memoryMax", event.target.value)}>
              <option value="">Same as reserved</option>
              {ceilingOptions(memorySizes, memory, memoryBytes, memoryMax).map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}
            </Select>
          </Field>
        </>
      ) : null}
    </div>
  );
}
