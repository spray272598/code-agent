import * as React from "react";
import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import "@xterm/xterm/css/xterm.css";
import { api, getAccessToken } from "@/lib/api";

interface SSHConnection {
  Name: string;
  Host: string;
  Port: number;
  Username: string;
}

type Status = "idle" | "connecting" | "open" | "closed" | "error";

export default function SSHTerminal() {
  const [connections, setConnections] = React.useState<SSHConnection[]>([]);
  const [connName, setConnName] = React.useState("");
  const [status, setStatus] = React.useState<Status>("idle");

  const termHostRef = React.useRef<HTMLDivElement>(null);
  const termRef = React.useRef<Terminal | null>(null);
  const fitRef = React.useRef<FitAddon | null>(null);
  const socketRef = React.useRef<WebSocket | null>(null);
  const cleanupRef = React.useRef<(() => void) | null>(null);

  React.useEffect(() => {
    api<SSHConnection[]>("/api/v1/ssh/connections")
      .then(setConnections)
      .catch(() => setConnections([]));
    return () => cleanupRef.current?.();
  }, []);

  const connect = () => {
    if (!connName || status === "connecting" || status === "open") return;
    cleanupRef.current?.();

    const token = getAccessToken() ?? "";
    const proto = window.location.protocol === "https:" ? "wss" : "ws";
    const url =
      `${proto}://${window.location.host}/ws/ssh` +
      `?token=${encodeURIComponent(token)}&connection=${encodeURIComponent(connName)}`;

    setStatus("connecting");
    const socket = new WebSocket(url);
    socketRef.current = socket;

    const term = new Terminal({
      cursorBlink: true,
      fontSize: 14,
      theme: { background: "#0b0f14" },
    });
    const fit = new FitAddon();
    term.loadAddon(fit);
    if (termHostRef.current) {
      term.open(termHostRef.current);
      // fit after layout settles
      requestAnimationFrame(() => {
        try {
          fit.fit();
        } catch {
          /* noop */
        }
      });
    }
    termRef.current = term;
    fitRef.current = fit;

    socket.onopen = () => {
      setStatus("open");
      try {
        socket.send(JSON.stringify({ type: "resize", cols: term.cols, rows: term.rows }));
      } catch {
        /* noop */
      }
    };
    socket.onmessage = (e) => term.write(String(e.data));
    socket.onclose = () => {
      setStatus("closed");
      term.writeln("\r\n\x1b[31m[连接已关闭]\x1b[0m");
    };
    socket.onerror = () => {
      setStatus("error");
      term.writeln("\r\n\x1b[31m[连接错误]\x1b[0m");
    };

    term.onData((data) => {
      if (socket.readyState === WebSocket.OPEN) socket.send(data);
    });

    const ro = new ResizeObserver(() => {
      try {
        fit.fit();
        if (socket.readyState === WebSocket.OPEN) {
          socket.send(JSON.stringify({ type: "resize", cols: term.cols, rows: term.rows }));
        }
      } catch {
        /* noop */
      }
    });
    if (termHostRef.current) ro.observe(termHostRef.current);

    cleanupRef.current = () => {
      ro.disconnect();
      try {
        socket.close();
      } catch {
        /* noop */
      }
      term.dispose();
      termRef.current = null;
      fitRef.current = null;
      socketRef.current = null;
    };
  };

  const disconnect = () => cleanupRef.current?.();

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-2">
        <select
          value={connName}
          onChange={(e) => setConnName(e.target.value)}
          className="rounded-md border bg-card px-3 py-2 text-sm"
        >
          <option value="">选择 SSH 连接…</option>
          {connections.map((c) => (
            <option key={c.Name} value={c.Name}>
              {c.Name} ({c.Username}@{c.Host}:{c.Port})
            </option>
          ))}
        </select>
        <input
          placeholder="或手动输入连接名"
          value={connName}
          onChange={(e) => setConnName(e.target.value)}
          className="rounded-md border bg-card px-3 py-2 text-sm"
        />
        <button
          onClick={connect}
          disabled={!connName || status === "connecting" || status === "open"}
          className="rounded-md bg-primary px-3 py-2 text-sm font-medium text-primary-foreground disabled:opacity-50"
        >
          连接
        </button>
        <button
          onClick={disconnect}
          disabled={status !== "open"}
          className="rounded-md border px-3 py-2 text-sm disabled:opacity-50"
        >
          断开
        </button>
        <span className="text-xs text-muted-foreground">{status}</span>
      </div>
      <div
        ref={termHostRef}
        className="h-[480px] w-full overflow-hidden rounded-md border bg-[#0b0f14] p-2"
      />
    </div>
  );
}
