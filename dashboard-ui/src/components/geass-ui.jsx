import React from "react";
import { Link } from "@tanstack/react-router";
import { useSettingsRail } from "@/layout/SettingsShell";
import { RailInput, RailSelect, RailTextarea } from "@/components/rail-controls";
import {
  Alert,
  Badge as MantineBadge,
  Button as MantineButton,
  Center,
  Checkbox as MantineCheckbox,
  Code,
  Divider,
  Group,
  Modal,
  NativeSelect,
  NavLink,
  Paper,
  Progress,
  SimpleGrid,
  Stack,
  Switch as MantineSwitch,
  Table as MantineTable,
  Text,
  Textarea as MantineTextarea,
  TextInput,
  Title,
} from "@mantine/core";
import { Box as BoxIcon } from "lucide-react";

export const Checkbox = MantineCheckbox;
export const Switch = MantineSwitch;

const buttonVariant = { default: "filled", outline: "default", ghost: "subtle", danger: "filled", signal: "filled" };
const buttonColor = { default: "signal", outline: "gray", ghost: "gray", danger: "red", signal: "signal" };

export function Button({ variant = "default", size, leftSection, fullWidth, style, children, ...props }) {
  const mantineSize = size === "sm" ? "compact-sm" : size === "icon" ? "compact-sm" : "sm";
  return (
    <MantineButton
      variant={buttonVariant[variant] || "filled"}
      color={buttonColor[variant] || "cf"}
      size={mantineSize}
      leftSection={leftSection}
      fullWidth={fullWidth}
      style={{
        alignSelf: fullWidth ? undefined : "flex-start",
        ...(variant === "danger" ? { background: "#e66a6a", color: "#fff" } : {}),
        ...style,
      }}
      {...props}
    >
      {children}
    </MantineButton>
  );
}

export function Card({ children, p = "md", ...props }) {
  return <Paper withBorder radius="md" p={p} {...props}>{children}</Paper>;
}

export function Eyebrow({ children }) {
  return <Text size="xs" c="dimmed" fw={500}>{children}</Text>;
}

export function Muted({ children, size = "sm", ...props }) {
  return <Text size={size} c="dimmed" {...props}>{children}</Text>;
}

export function StatCard({ label, value, detail, children }) {
  return (
    <Paper withBorder radius="md" p="md">
      <Text size="sm" c="dimmed" fw={500}>{label}</Text>
      <Text fz={20} fw={600} mt={4}>{value}</Text>
      {detail && <Text size="sm" c="dimmed" mt={4}>{detail}</Text>}
      {children}
    </Paper>
  );
}

export function Badge({ children, tone = "neutral" }) {
  const color = tone === "success" ? "green" : tone === "danger" ? "red" : tone === "warning" ? "yellow" : tone === "signal" ? "yellow" : "gray";
  return <MantineBadge color={color} variant={tone === "neutral" ? "outline" : "light"} tt="none" fw={500} radius="sm">{children}</MantineBadge>;
}

const formInputClassNames = {
  root: "rw-form-control",
  label: "rw-form-label",
  description: "rw-form-hint",
  input: "rw-form-input",
  section: "rw-form-section",
  wrapper: "rw-form-input-wrap",
};

function mergeClassNames(base, extra) {
  if (!extra) return base;
  const merged = { ...base };
  Object.keys(extra).forEach((key) => {
    merged[key] = extra[key] ? `${base[key] || ""} ${extra[key]}`.trim() : base[key];
  });
  return merged;
}

export function Input({ classNames, className, ...props }) {
  const railway = useSettingsRail();
  if (railway) {
    return <RailInput className={className} {...props} />;
  }
  return <TextInput classNames={mergeClassNames(formInputClassNames, classNames)} className={className} {...props} />;
}

export function Textarea({ rows = 4, classNames, className, ...props }) {
  const railway = useSettingsRail();
  if (railway) {
    return <RailTextarea rows={rows} className={className} {...props} />;
  }
  return (
    <MantineTextarea
      autosize
      minRows={rows}
      classNames={mergeClassNames({ ...formInputClassNames, input: "rw-form-input rw-form-textarea" }, classNames)}
      className={className}
      {...props}
    />
  );
}

export function Select({ children, classNames, className, ...props }) {
  const railway = useSettingsRail();
  if (railway) {
    return <RailSelect className={className} {...props}>{children}</RailSelect>;
  }
  return (
    <NativeSelect classNames={mergeClassNames(formInputClassNames, classNames)} className={className} {...props}>
      {children}
    </NativeSelect>
  );
}

export function Field({ label, children, description, className }) {
  const railway = useSettingsRail();
  const rootClass = railway
    ? ["rw-rail-field", className].filter(Boolean).join(" ")
    : ["rw-form-field", className].filter(Boolean).join(" ");
  const labelClass = railway ? "rw-rail-label" : "rw-form-label";
  const hintClass = railway ? "rw-rail-hint" : "rw-form-hint";
  return (
    <div className={rootClass}>
      {label ? <span className={labelClass}>{label}</span> : null}
      {children}
      {description ? <span className={hintClass}>{description}</span> : null}
    </div>
  );
}

export function Separator() {
  return <Divider />;
}

export function TableLink({ to, children }) {
  return <Link to={to} style={{ color: "inherit", textDecoration: "none" }}>{children}</Link>;
}

export function DataTable({ columns, rows, empty, getRowKey, onRowClick, rowClassName }) {
  if (!rows?.length) {
    if (empty == null) return null;
    return typeof empty === "string" ? <Text size="sm" c="dimmed">{empty}</Text> : empty;
  }
  return (
    <MantineTable.ScrollContainer minWidth={480}>
      <MantineTable highlightOnHover={Boolean(onRowClick)} withRowBorders verticalSpacing="sm" horizontalSpacing="md">
        <MantineTable.Thead>
          <MantineTable.Tr>
            {columns.map((col) => (
              <MantineTable.Th key={col.key || col.label} ta={col.align}>{col.label}</MantineTable.Th>
            ))}
          </MantineTable.Tr>
        </MantineTable.Thead>
        <MantineTable.Tbody>
          {rows.map((row, index) => {
            const mark = rowClassName ? rowClassName(row, index) : "";
            return (
            <MantineTable.Tr
              key={getRowKey ? getRowKey(row, index) : index}
              onClick={onRowClick ? () => onRowClick(row, index) : undefined}
              bg={String(mark).includes("selected") ? "#f3f7ff" : undefined}
              style={{ cursor: onRowClick ? "pointer" : undefined, opacity: String(mark).includes("disabled") ? 0.45 : 1 }}
            >
              {columns.map((col) => (
                <MantineTable.Td key={col.key || col.label} ta={col.align}>
                  {col.render ? col.render(row, index) : row[col.key]}
                </MantineTable.Td>
              ))}
            </MantineTable.Tr>
            );
          })}
        </MantineTable.Tbody>
      </MantineTable>
    </MantineTable.ScrollContainer>
  );
}

export function KeyValueTable({ items, empty }) {
  if (!items?.length) {
    if (empty == null) return null;
    return typeof empty === "string" ? <Text size="sm" c="dimmed">{empty}</Text> : empty;
  }
  return (
    <MantineTable withRowBorders verticalSpacing="xs">
      <MantineTable.Tbody>
        {items.map((entry) => (
          <MantineTable.Tr key={entry.label}>
            <MantineTable.Th w="34%">{entry.label}</MantineTable.Th>
            <MantineTable.Td>{entry.value ?? "—"}</MantineTable.Td>
          </MantineTable.Tr>
        ))}
      </MantineTable.Tbody>
    </MantineTable>
  );
}

export function Table({ headers, children }) {
  return (
    <MantineTable withTableBorder highlightOnHover>
      <MantineTable.Thead>
        <MantineTable.Tr>
          {headers.map((header) => <MantineTable.Th key={header}>{header}</MantineTable.Th>)}
        </MantineTable.Tr>
      </MantineTable.Thead>
      <MantineTable.Tbody>
        {React.Children.map(children, (row) => (
          <MantineTable.Tr key={row?.key}>
            {React.Children.map(row?.props?.children, (cell) => (
              <MantineTable.Td>{cell?.props?.children}</MantineTable.Td>
            ))}
          </MantineTable.Tr>
        ))}
      </MantineTable.Tbody>
    </MantineTable>
  );
}

export function PageHeader({ leading, title, description, actions }) {
  return (
    <Group justify="space-between" align="flex-end" wrap="wrap">
      <Group gap="sm" wrap="nowrap" align="center">
        {leading}
        <div>
          {title && <Title order={1}>{title}</Title>}
          {description && <Text size="sm" c="dimmed">{description}</Text>}
        </div>
      </Group>
      {actions ? <Group gap="xs">{actions}</Group> : null}
    </Group>
  );
}

export function ResourceList({ columns, rows, empty, getRowKey }) {
  return (
    <Stack gap="xs">
      <SimpleGrid cols={columns.length} spacing="md">
        {columns.map((column) => (
          <Text key={column.label} size="sm" c="dimmed">{column.label}</Text>
        ))}
      </SimpleGrid>
      {rows?.length ? rows.map((row, index) => {
        const key = getRowKey ? getRowKey(row, index) : index;
        const cells = columns.map((column) => (
          <div key={column.label}>
            {column.render ? column.render(row, index) : row[column.key]}
          </div>
        ));
        const body = <SimpleGrid cols={columns.length} spacing="md">{cells}</SimpleGrid>;
        if (row.href) {
          return (
            <Paper key={key} withBorder radius="md" p="sm" component={Link} to={row.href} c="inherit">
              {body}
            </Paper>
          );
        }
        return <Paper key={key} withBorder radius="md" p="sm">{body}</Paper>;
      }) : (
        <Paper withBorder radius="md" p="xl" mih={220}>
          <Center h="100%">
            {typeof empty === "string" ? <Text c="dimmed">{empty}</Text> : empty}
          </Center>
        </Paper>
      )}
    </Stack>
  );
}

export function ResourceName({ icon: Icon, children }) {
  return (
    <Group gap="xs" wrap="nowrap">
      {Icon ? <Icon size={16} /> : null}
      <Text size="sm" fw={500}>{children}</Text>
    </Group>
  );
}

export function MetricPanel({ label, value, children }) {
  return (
    <Paper withBorder radius="md" p="md">
      {(label || value != null) && (
        <Group justify="space-between" mb={children ? "sm" : 0}>
          {label ? <Text size="sm">{label}</Text> : <span />}
          {value != null && value !== "" ? <Text fz={20} fw={600}>{value}</Text> : null}
        </Group>
      )}
      {children}
    </Paper>
  );
}

export function ResourceHero(props) {
  return <PageHeader {...props} />;
}

export function Empty({ icon: Icon = BoxIcon, title, description, action }) {
  return (
    <Center py={48}>
      <Stack align="center" gap="xs">
        <Icon size={28} />
        <Title order={3}>{title}</Title>
        <Text size="sm" c="dimmed" ta="center" maw={420}>{description}</Text>
        {action}
      </Stack>
    </Center>
  );
}

export function Section({ title, description, children }) {
  return (
    <Stack gap="sm">
      <div>
        <Title order={3}>{title}</Title>
        {description && <Text size="sm" c="dimmed">{description}</Text>}
      </div>
      {children}
    </Stack>
  );
}

export function ResourceModal({ open, onOpenChange, title, description, children }) {
  return (
    <Modal opened={open} onClose={() => onOpenChange(false)} title={title} size="lg">
      {description && <Text c="dimmed" size="sm" mb="md">{description}</Text>}
      {children}
    </Modal>
  );
}

export function ChoiceCard({ selected, disabled, onClick, title, description }) {
  return (
    <Paper
      withBorder
      radius="md"
      p="sm"
      component="button"
      type="button"
      w="100%"
      onClick={onClick}
      disabled={disabled}
      style={{
        textAlign: "left",
        cursor: disabled ? "not-allowed" : "pointer",
        opacity: disabled ? 0.5 : 1,
        background: selected ? "color-mix(in srgb, #888cf8 16%, #17171a)" : "#1c1c21",
        borderColor: selected ? "#888cf8" : "#3a3a44",
        color: "#f2f2f3",
      }}
    >
      <Text size="sm" fw={500}>{title}</Text>
      <Text size="sm" c="dimmed">{description}</Text>
    </Paper>
  );
}

export function ResourceRowLink({ href, icon: Icon, title, subtitle, status }) {
  return (
    <Paper withBorder radius="md" p="sm" component={Link} to={href} c="inherit">
      <Group justify="space-between" wrap="nowrap">
        <Group gap="sm" wrap="nowrap">
          {Icon ? <Icon size={16} /> : null}
          <div>
            <Text size="sm" fw={500}>{title}</Text>
            <Text size="xs" c="dimmed">{subtitle}</Text>
          </div>
        </Group>
        {status}
      </Group>
    </Paper>
  );
}

export function ProjectCard({ href, displayName, slug, environments, resourceCount, ready }) {
  return (
    <Paper withBorder radius="md" p="sm" component={Link} to={href} c="inherit">
      <Group justify="space-between">
        <div>
          <Text size="sm" fw={500}>{displayName}</Text>
          <Text size="xs" c="dimmed">{slug}</Text>
        </div>
        <Group gap="md">
          <Text size="sm" c="dimmed">{environments || "—"}</Text>
          <Text size="sm" c="dimmed">{resourceCount} resources</Text>
          <Badge tone={ready ? "success" : "warning"}>{ready ? "Available" : "Pending"}</Badge>
        </Group>
      </Group>
    </Paper>
  );
}

export function NavTabs({ value, items }) {
  return (
    <Stack gap={4}>
      {items.map((item) => (
        <NavLink
          key={item.id}
          component={Link}
          to={item.to}
          search={item.search}
          label={item.label}
          active={value === item.id}
          color="gray"
          variant="subtle"
        />
      ))}
    </Stack>
  );
}

export function DetailTabs({ value, tabs, hrefFor }) {
  const label = (tab) => tab.charAt(0).toUpperCase() + tab.slice(1);
  return (
    <div className="rw-tabs">
      {tabs.map((tab) => (
        <Link key={tab} to={hrefFor(tab)} className={value === tab ? "rw-tab rw-tab-on" : "rw-tab"}>
          {label(tab)}
        </Link>
      ))}
    </div>
  );
}

export function PageStack({ children }) {
  return <Stack gap="lg">{children}</Stack>;
}

export function PageSection({ title, description, actions, children, plain = false }) {
  const body = plain ? children : (
    <Paper withBorder radius={8} p="md" shadow="none" className="rw-settings-mantine-panel">{children}</Paper>
  );
  return (
    <Stack gap={6} className="rw-page-section">
      {(title || description || actions) && (
        <Group justify="space-between" align="flex-end" wrap="wrap">
          <Stack gap={2}>
            {title ? <Text fw={600} size="sm" className="rw-page-section-title">{title}</Text> : null}
            {description ? <Text size="sm" c="dimmed">{description}</Text> : null}
          </Stack>
          {actions}
        </Group>
      )}
      {body}
    </Stack>
  );
}

export function SectionCard(props) {
  return <PageSection {...props} />;
}

export function FormStack({ children, onSubmit, className, ...formProps }) {
  const content = <Stack gap="md" className={["rw-form-stack", className].filter(Boolean).join(" ")}>{children}</Stack>;
  if (onSubmit) return <form onSubmit={onSubmit} className="rw-form" {...formProps}>{content}</form>;
  return content;
}

export function ListRow({ title, subtitle, trailing }) {
  return (
    <Group justify="space-between" wrap="nowrap">
      <div>
        <Text size="sm" fw={500}>{title}</Text>
        {subtitle ? <Text size="sm" c="dimmed">{subtitle}</Text> : null}
      </div>
      {trailing}
    </Group>
  );
}

export function DangerZone({ children }) {
  return <Stack gap="sm" mt="xl">{children}</Stack>;
}

export function DetailRow({ label, value }) {
  return (
    <Group justify="space-between" py="xs">
      <Text size="sm" c="dimmed">{label}</Text>
      <Text size="sm">{value}</Text>
    </Group>
  );
}

export function SettingRowLink({ href, title, description }) {
  return (
    <Text component={Link} to={href} size="sm">
      {title}
      {description ? <Text span c="dimmed" ml="sm">{description}</Text> : null}
    </Text>
  );
}

export function PendingBanner({ message, action }) {
  return (
    <Alert color="yellow" variant="light">
      <Group justify="space-between" wrap="wrap">
        <Text size="sm">{message}</Text>
        {action}
      </Group>
    </Alert>
  );
}

export function CapacityBar({ value }) {
  const color = value >= 90 ? "red" : value >= 70 ? "yellow" : "green";
  return <Progress value={value} color={color} size="sm" mt="sm" aria-label={`${value}% utilized`} />;
}

export function EndpointBox({ children }) {
  return <Code block>{children}</Code>;
}

export function LoginPanel({ children }) {
  return (
    <Center mih="100vh" p="md" bg="#0e0e10">
      <Paper withBorder radius={8} p="xl" shadow="none" maw={420} w="100%" bg="#17171a">
        <Stack align="center" gap="lg">
          <img src="/geass-symbol.svg" alt="" width={40} height={40} />
          {children}
        </Stack>
      </Paper>
    </Center>
  );
}
