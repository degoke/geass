import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Link, useNavigate, useRouterState } from "@tanstack/react-router";
import { Box, Database, GitBranch, HardDrive, Maximize2, SquareFunction, ZoomIn, ZoomOut } from "lucide-react";
import { Status } from "@/components/Status";

const KIND_ICON = {
  Service: Box,
  Bucket: HardDrive,
  Redis: Database,
};

const NODE_WIDTH = 236;
const NODE_HEIGHT = 112;
const NODE_STEP = 300;
const ZOOM_MIN = 0.45;
const ZOOM_MAX = 1.6;
const ZOOM_STEP = 0.1;
const DRAG_CLICK_THRESHOLD = 4;

function storageKey(projectKey) {
  return `geass-canvas-pos:${projectKey || "default"}`;
}

function loadPositions(projectKey) {
  try {
    const raw = localStorage.getItem(storageKey(projectKey));
    return raw ? JSON.parse(raw) : {};
  } catch {
    return {};
  }
}

function savePositions(projectKey, positions) {
  try {
    localStorage.setItem(storageKey(projectKey), JSON.stringify(positions));
  } catch {
    /* ignore */
  }
}

function layoutNodes(rows) {
  const apps = rows.filter((row) => row.kind === "Service");
  const others = rows.filter((row) => row.kind !== "Service");
  if (!apps.length) {
    return others.map((row, index) => ({
      ...row,
      x: 80 + (index % 3) * NODE_STEP,
      y: 120 + Math.floor(index / 3) * 170,
    }));
  }
  // Both rows start at the same left edge and are centred on each other, so nothing slides under the rail.
  const span = (count) => (count ? (count - 1) * NODE_STEP + NODE_WIDTH : 0);
  const columns = Math.min(3, others.length);
  const width = Math.max(span(apps.length), span(columns));
  const nodes = [];
  apps.forEach((row, index) => {
    nodes.push({ ...row, x: 80 + (width - span(apps.length)) / 2 + index * NODE_STEP, y: 72 });
  });
  others.forEach((row, index) => {
    nodes.push({
      ...row,
      x: 80 + (width - span(columns)) / 2 + (index % 3) * NODE_STEP,
      y: 280 + Math.floor(index / 3) * 170,
    });
  });
  return nodes;
}

/** Only draw edges for explicit resource references (no decorative hub links). */
function buildEdges(nodes) {
  const byResourceName = new Map();
  for (const node of nodes) {
    if (node.resourceName) {
      byResourceName.set(node.resourceName, node);
    }
  }
  const edges = [];
  const seen = new Set();
  for (const from of nodes) {
    for (const targetName of from.linkTo || []) {
      const to = byResourceName.get(String(targetName).trim());
      if (!to || to.key === from.key) continue;
      const edgeKey = `${from.key}->${to.key}`;
      if (seen.has(edgeKey)) continue;
      seen.add(edgeKey);
      edges.push({ from, to });
    }
  }
  return edges;
}

/** Lines leave and enter a card at right angles, like wiring on a board. */
function edgePath(from, to) {
  const x1 = from.x + NODE_WIDTH / 2;
  const y1 = from.y + NODE_HEIGHT;
  const x2 = to.x + NODE_WIDTH / 2;
  const y2 = to.y;
  const midY = (y1 + y2) / 2;
  return `M ${x1} ${y1} V ${midY} H ${x2} V ${y2}`;
}

export function ProjectCanvas({ projectKey, rows, environment }) {
  const navigate = useNavigate();
  const pathname = useRouterState({ select: (state) => state.location.pathname });
  const viewportRef = useRef(null);
  const [zoom, setZoom] = useState(1);
  const [pan, setPan] = useState({ x: 0, y: 0 });
  const [panning, setPanning] = useState(false);
  const [positions, setPositions] = useState(() => loadPositions(projectKey));
  const [draggingKey, setDraggingKey] = useState("");
  const panStart = useRef({ x: 0, y: 0, panX: 0, panY: 0 });
  const nodeDrag = useRef(null);
  const suppressClick = useRef(false);
  const zoomRef = useRef(zoom);
  zoomRef.current = zoom;

  useEffect(() => {
    setPositions(loadPositions(projectKey));
  }, [projectKey]);

  const layout = useMemo(() => layoutNodes(rows), [rows]);
  const nodes = useMemo(
    () => layout.map((node) => {
      const saved = positions[node.key];
      return saved ? { ...node, x: saved.x, y: saved.y } : node;
    }),
    [layout, positions],
  );
  const edges = useMemo(() => buildEdges(nodes), [nodes]);
  const selectedKey = nodes.find((node) => node.href === pathname)?.key || "";

  // The drawer docks beside the canvas, so a card that was visible can end up beneath it. Pan just enough
  // to keep the selected card in view.
  useEffect(() => {
    const viewport = viewportRef.current;
    const selected = nodes.find((node) => node.key === selectedKey);
    if (!viewport || !selected) return;
    const margin = 32;
    const left = selected.x * zoomRef.current;
    const right = left + NODE_WIDTH * zoomRef.current;
    setPan((current) => {
      const screenRight = right + current.x;
      const screenLeft = left + current.x;
      const width = viewport.clientWidth;
      if (screenRight > width - margin) return { ...current, x: current.x - (screenRight - (width - margin)) };
      if (screenLeft < margin) return { ...current, x: current.x + (margin - screenLeft) };
      return current;
    });
    // Only when the selection changes: reacting to every pan or drag would fight the user.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selectedKey]);

  const clampZoom = (value) => Math.min(ZOOM_MAX, Math.max(ZOOM_MIN, value));

  const onWheel = useCallback((event) => {
    event.preventDefault();
    const delta = event.deltaY > 0 ? -ZOOM_STEP : ZOOM_STEP;
    setZoom((value) => clampZoom(Number((value + delta).toFixed(2))));
  }, []);

  const onPointerDown = (event) => {
    if (event.target.closest("a, button")) return;
    viewportRef.current?.setPointerCapture(event.pointerId);
    setPanning(true);
    panStart.current = { x: event.clientX, y: event.clientY, panX: pan.x, panY: pan.y };
  };

  const clearNodeDragListeners = useRef(() => {});

  const endNodeDrag = useCallback(
    (event) => {
      if (!nodeDrag.current) return;
      const { moved, href, envSearch } = nodeDrag.current;
      if (moved) {
        suppressClick.current = true;
        setPositions((previous) => {
          savePositions(projectKey, previous);
          return previous;
        });
      } else if (href) {
        navigate({ to: href, search: envSearch });
      }
      clearNodeDragListeners.current();
      const viewport = viewportRef.current;
      if (viewport && event?.pointerId != null) {
        try {
          viewport.releasePointerCapture(event.pointerId);
        } catch {
          /* ignore */
        }
      }
      nodeDrag.current = null;
      setDraggingKey("");
    },
    [navigate, projectKey],
  );

  const onNodePointerMove = useCallback((event) => {
    const drag = nodeDrag.current;
    if (!drag) return;
    const screenDx = event.clientX - drag.startX;
    const screenDy = event.clientY - drag.startY;
    if (!drag.moved) {
      if (Math.abs(screenDx) <= DRAG_CLICK_THRESHOLD && Math.abs(screenDy) <= DRAG_CLICK_THRESHOLD) return;
      drag.moved = true;
    }
    event.preventDefault();
    const scale = zoomRef.current || 1;
    const x = Math.max(0, drag.originX + screenDx / scale);
    const y = Math.max(0, drag.originY + screenDy / scale);
    setPositions((previous) => ({ ...previous, [drag.key]: { x, y } }));
  }, []);

  const onNodePointerDown = (event, node) => {
    if (event.button !== 0) return;
    event.preventDefault();
    event.stopPropagation();
    viewportRef.current?.setPointerCapture(event.pointerId);
    nodeDrag.current = {
      key: node.key,
      href: node.href,
      envSearch: environment ? { environment } : undefined,
      startX: event.clientX,
      startY: event.clientY,
      originX: node.x,
      originY: node.y,
      moved: false,
    };
    setDraggingKey(node.key);

    const onWindowMove = (moveEvent) => onNodePointerMove(moveEvent);
    const onWindowUp = (upEvent) => endNodeDrag(upEvent);
    window.addEventListener("pointermove", onWindowMove);
    window.addEventListener("pointerup", onWindowUp);
    window.addEventListener("pointercancel", onWindowUp);
    clearNodeDragListeners.current = () => {
      window.removeEventListener("pointermove", onWindowMove);
      window.removeEventListener("pointerup", onWindowUp);
      window.removeEventListener("pointercancel", onWindowUp);
    };
  };

  const onPointerMove = (event) => {
    if (nodeDrag.current) {
      onNodePointerMove(event);
      return;
    }
    if (!panning) return;
    setPan({
      x: panStart.current.panX + (event.clientX - panStart.current.x),
      y: panStart.current.panY + (event.clientY - panStart.current.y),
    });
  };

  const onPointerUp = (event) => {
    if (nodeDrag.current) {
      endNodeDrag(event);
      return;
    }
    if (!panning) return;
    viewportRef.current?.releasePointerCapture(event.pointerId);
    setPanning(false);
  };

  const onNodeClick = (event) => {
    if (suppressClick.current) {
      event.preventDefault();
      event.stopPropagation();
      suppressClick.current = false;
    }
  };

  useEffect(() => () => clearNodeDragListeners.current(), []);

  const fitView = () => {
    setZoom(1);
    setPan({ x: 0, y: 0 });
  };

  const search = environment ? { environment } : undefined;

  return (
    <div
      ref={viewportRef}
      className={`rw-project-canvas-viewport${panning ? " is-panning" : ""}`}
      onWheel={onWheel}
      onPointerDown={onPointerDown}
      onPointerMove={onPointerMove}
      onPointerUp={onPointerUp}
      onPointerCancel={onPointerUp}
    >
      <div className="rw-project-canvas-controls" aria-label="Canvas controls">
        <button type="button" className="rw-canvas-ctl" onClick={() => setZoom((value) => clampZoom(value + ZOOM_STEP))} aria-label="Zoom in">
          <ZoomIn size={16} />
        </button>
        <button type="button" className="rw-canvas-ctl" onClick={() => setZoom((value) => clampZoom(value - ZOOM_STEP))} aria-label="Zoom out">
          <ZoomOut size={16} />
        </button>
        <button type="button" className="rw-canvas-ctl" onClick={fitView} aria-label="Reset view">
          <Maximize2 size={16} />
        </button>
      </div>

      <div
        className="rw-project-canvas-stage"
        style={{ transform: `translate(${pan.x}px, ${pan.y}px) scale(${zoom})` }}
      >
        {edges.length ? (
          <svg className="rw-project-canvas-edges" aria-hidden="true">
            {edges.map(({ from, to }) => (
              <path
                key={`${from.key}-${to.key}`}
                d={edgePath(from, to)}
                className={pathname === from.href || pathname === to.href ? "rw-project-canvas-edge is-active" : "rw-project-canvas-edge"}
              />
            ))}
          </svg>
        ) : null}

        {nodes.map((node) => {
          const Icon = KIND_ICON[node.kind] || Database;
          const isGit = node.kind === "Service" && (node.sourceKind === "git" || String(node.source || "").includes("github"));
          const isFunction = node.sourceKind === "function";
          const pending = node.pendingKinds?.length;
          const deleting = node.isDeleting;
          const failed = node.status === "Failed" || node.status === "False" || node.status === "Error";
          const selected = pathname === node.href;
          const classes = [
            "rw-canvas-node",
            draggingKey === node.key ? "is-dragging" : "",
            selected ? "is-selected" : "",
            failed ? "is-failed" : "",
            pending ? "has-pending" : "",
            node.isNew ? "is-new" : "",
            deleting ? "is-deleting" : "",
          ].filter(Boolean).join(" ");
          return (
            <Link
              key={node.key}
              to={node.href}
              search={search}
              className={classes}
              style={{ left: node.x, top: node.y }}
              onPointerDown={(event) => onNodePointerDown(event, node)}
              onClick={onNodeClick}
              onClickCapture={onNodeClick}
              draggable={false}
            >
              <div className="rw-canvas-node-head">
                <span className="rw-canvas-node-icon">{isFunction ? <SquareFunction size={16} /> : isGit ? <GitBranch size={16} /> : <Icon size={16} />}</span>
                <span className="rw-canvas-node-name">{node.name}</span>
                {deleting ? <span className="rw-canvas-node-badge is-deleting">Deleting</span> : null}
                {!deleting && node.isNew ? <span className="rw-canvas-node-badge">New</span> : null}
                {node.meta ? <span className="rw-canvas-node-kind">{node.meta}</span> : null}
              </div>
              <div className="rw-canvas-node-sub">{node.source}</div>
              {node.warning ? <div className="rw-backup-node-warning">{node.warning}</div> : null}
              <div className="rw-canvas-node-foot">
                <Status value={node.status} />
                {pending ? (
                  <span className="rw-canvas-node-change">{node.pendingCount} {node.pendingCount === 1 ? "change" : "changes"}</span>
                ) : node.endpoint ? (
                  <span className="rw-canvas-node-endpoint">{node.endpoint}</span>
                ) : null}
              </div>
            </Link>
          );
        })}

        {!nodes.length ? <div className="rw-canvas-empty">No resources deployed</div> : null}
      </div>
    </div>
  );
}
