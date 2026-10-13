import { useCallback, useEffect, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { FitAddon } from "@xterm/addon-fit";
import { Terminal } from "@xterm/xterm";
import "@xterm/xterm/css/xterm.css";
import { Maximize2, Minimize2, Terminal as TerminalIcon } from "lucide-react";
import { action, api } from "@/lib/api";
import { canMutate } from "@/lib/dashboard";
import { alertUser } from "@/lib/notify";
import { Button, Muted, Select } from "@/components/geass-ui";

const SESSION_SECONDS = 300;
const encoder = new TextEncoder();

const THEME = {
  background: "#000000",
  foreground: "#d4d4d4",
  cursor: "#7ce0a0",
  cursorAccent: "#000000",
  selectionBackground: "#7ce0a055",
  black: "#151515",
  brightBlack: "#8f8f8f",
  red: "#ff8a8a",
  green: "#7ce0a0",
  yellow: "#e0a455",
  blue: "#7aa2f7",
  magenta: "#b48cf8",
  cyan: "#6fd0d8",
  white: "#d4d4d4",
};

function kubectlCommand(namespace, pod, container) {
  return `kubectl exec -it -n ${namespace} ${pod} -c ${container} -- sh`;
}

function socketURL(name, session, terminal) {
  const scheme = window.location.protocol === "https:" ? "wss" : "ws";
  const query = new URLSearchParams({ session, cols: String(terminal.cols), rows: String(terminal.rows) });
  return `${scheme}://${window.location.host}/api/apps/${name}/console/ws?${query}`;
}

/**
 * Interactive terminal for a service. A console session (5 minutes, recorded with the actor)
 * is created first, then an xterm.js terminal attaches to the pod over a WebSocket with a real TTY.
 */
export function ServiceConsole({ item, name, data }) {
  const mutable = canMutate(data);
  const runtime = useQuery({ queryKey: ["runtime", "apps", name], queryFn: () => api(`/api/apps/${name}/runtime`) });
  const pods = (runtime.data?.pods || []).filter((pod) => pod.phase === "Running");
  const [target, setTarget] = useState("");
  const [state, setState] = useState("idle"); // idle | connecting | connected
  const [endsAt, setEndsAt] = useState(0);
  const [secondsLeft, setSecondsLeft] = useState(0);
  const [fullscreen, setFullscreen] = useState(false);
  const hostRef = useRef(null);
  const termRef = useRef(null);
  const fitRef = useRef(null);
  const socketRef = useRef(null);

  useEffect(() => {
    if (!target && pods.length) setTarget(`${pods[0].name}|${pods[0].container}`);
  }, [pods, target]);

  const fit = useCallback(() => {
    try {
      fitRef.current?.fit();
    } catch {
      /* the host is not laid out yet */
    }
  }, []);

  // Create the terminal once and keep it for the life of the tab.
  useEffect(() => {
    if (!mutable || !hostRef.current) return undefined;
    const terminal = new Terminal({
      cursorBlink: true,
      fontFamily: '"JetBrains Mono Variable", "JetBrains Mono", ui-monospace, Menlo, monospace',
      fontSize: 13,
      scrollback: 5000,
      theme: THEME,
      convertEol: false,
    });
    const fitAddon = new FitAddon();
    terminal.loadAddon(fitAddon);
    terminal.open(hostRef.current);
    termRef.current = terminal;
    fitRef.current = fitAddon;
    fit();
    terminal.writeln("\x1b[90mConnect to open a shell in a running pod.\x1b[0m");
    terminal.writeln("\x1b[90mSessions last five minutes and are recorded.\x1b[0m");

    const input = terminal.onData((text) => {
      const socket = socketRef.current;
      if (socket && socket.readyState === WebSocket.OPEN) socket.send(encoder.encode(text));
    });
    const resize = terminal.onResize(({ cols, rows }) => {
      const socket = socketRef.current;
      if (socket && socket.readyState === WebSocket.OPEN) socket.send(JSON.stringify({ type: "resize", cols, rows }));
    });
    const observer = new ResizeObserver(() => fit());
    observer.observe(hostRef.current);

    return () => {
      observer.disconnect();
      input.dispose();
      resize.dispose();
      socketRef.current?.close();
      socketRef.current = null;
      terminal.dispose();
      termRef.current = null;
      fitRef.current = null;
    };
  }, [mutable, fit]);

  useEffect(() => {
    fit();
    termRef.current?.focus();
  }, [fullscreen, fit]);

  useEffect(() => {
    if (state !== "connected") return undefined;
    const tick = () => setSecondsLeft(Math.max(0, Math.round((endsAt - Date.now()) / 1000)));
    tick();
    const timer = setInterval(tick, 1000);
    return () => clearInterval(timer);
  }, [state, endsAt]);

  const [podName, container] = target.split("|");
  const namespace = item.status?.targetNamespace || "";

  const connect = async () => {
    const terminal = termRef.current;
    if (!target || !terminal) return;
    setState("connecting");
    let session;
    try {
      const result = await action(`/apps/${name}/console/create`, { target, command: "sh" });
      session = result.session;
    } catch (error) {
      setState("idle");
      alertUser(error.message);
      return;
    }
    fit();
    terminal.reset();
    terminal.writeln(`\x1b[90mConnecting to ${podName} (${container})…\x1b[0m`);
    const socket = new WebSocket(socketURL(name, session, terminal));
    socket.binaryType = "arraybuffer";
    socketRef.current = socket;
    let opened = false;

    socket.onopen = () => {
      opened = true;
      setEndsAt(Date.now() + SESSION_SECONDS * 1000);
      setState("connected");
      terminal.reset();
      terminal.focus();
    };
    socket.onmessage = (event) => {
      if (typeof event.data === "string") {
        try {
          const message = JSON.parse(event.data);
          if (message.type === "exit") {
            const detail = message.error ? `: ${message.error}` : message.code ? ` (status ${message.code})` : "";
            terminal.writeln(`\r\n\x1b[90mSession ended${detail}\x1b[0m`);
          }
        } catch {
          /* ignore malformed control frames */
        }
        return;
      }
      terminal.write(new Uint8Array(event.data));
    };
    socket.onclose = () => {
      if (socketRef.current === socket) socketRef.current = null;
      setState("idle");
      if (!opened) {
        terminal.writeln("\r\n\x1b[31mCould not open the terminal. The session may have ended; connect again.\x1b[0m");
      }
    };
  };

  const disconnect = () => {
    socketRef.current?.close();
  };

  const copyKubectl = () => {
    navigator.clipboard?.writeText(kubectlCommand(namespace, podName, container)).then(
      () => alertUser("kubectl command copied"),
      () => alertUser("Could not copy"),
    );
  };

  if (!mutable) {
    return <Muted size="sm" className="rw-console-note">Your viewer role cannot open consoles.</Muted>;
  }

  return (
    <div className={`rw-console${fullscreen ? " is-fullscreen" : ""}`}>
      <div className="rw-console-bar">
        <Select
          value={target}
          onChange={(event) => setTarget(event.currentTarget.value)}
          disabled={state !== "idle" || !pods.length}
          aria-label="Pod"
        >
          {pods.length ? pods.map((pod) => (
            <option key={pod.name} value={`${pod.name}|${pod.container}`}>{pod.name}</option>
          )) : <option value="">No running pods</option>}
        </Select>
        {state === "connected" ? (
          <>
            <span className="rw-console-state"><i aria-hidden /> Connected · {Math.floor(secondsLeft / 60)}:{String(secondsLeft % 60).padStart(2, "0")}</span>
            <Button variant="outline" size="sm" onClick={disconnect}>Disconnect</Button>
          </>
        ) : (
          <Button size="sm" leftSection={<TerminalIcon size={14} />} loading={state === "connecting"} disabled={!target} onClick={connect}>
            Connect
          </Button>
        )}
        <span className="rw-console-spacer" />
        {namespace && podName ? (
          <button type="button" className="rw-console-link" onClick={copyKubectl}>Copy kubectl command</button>
        ) : null}
        <button
          type="button"
          className="rw-console-link"
          onClick={() => setFullscreen((value) => !value)}
          aria-label={fullscreen ? "Exit full screen" : "Full screen"}
        >
          {fullscreen ? <Minimize2 size={14} /> : <Maximize2 size={14} />}
        </button>
      </div>
      <div className="rw-console-terminal" ref={hostRef} aria-label="Terminal" />
    </div>
  );
}
