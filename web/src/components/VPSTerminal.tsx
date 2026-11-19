import { useEffect, useRef, useState } from "react";
import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import "@xterm/xterm/css/xterm.css";

type Status = "connecting" | "connected" | "closed";

/**
 * Interactive shell in a VPS over a WebSocket (binary frames = terminal
 * bytes, text frames = JSON control messages). Loaded lazily: xterm.js is
 * only downloaded when a terminal is opened.
 */
export default function VPSTerminal({
  vpsId,
  onStatus,
  focusKey,
}: {
  vpsId: number;
  onStatus?: (s: Status, reason?: string) => void;
  /** Change this to give the keyboard back to the terminal (e.g. after toggling full screen). */
  focusKey?: unknown;
}) {
  const host = useRef<HTMLDivElement>(null);
  const termRef = useRef<Terminal | null>(null);
  const [status, setStatus] = useState<Status>("connecting");
  const [reason, setReason] = useState("");

  useEffect(() => {
    const el = host.current;
    if (!el) return;
    const term = new Terminal({
      cursorBlink: true,
      fontFamily: '"IBM Plex Mono", ui-monospace, SFMono-Regular, Menlo, Consolas, monospace',
      fontSize: 14,
      scrollback: 5000,
      theme: {
        background: "#020b16",
        foreground: "#d7e3ef",
        cursor: "#5cc8ff",
        selectionBackground: "#24507f",
        green: "#72ca9b",
        brightGreen: "#8ed9b0",
        blue: "#5cc8ff",
        brightBlue: "#9adcff",
        cyan: "#5cc8ff",
        red: "#fa999c",
        yellow: "#fbc27e",
      },
    });
    termRef.current = term;
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(el);
    fit.fit();

    const update = (s: Status, r = "") => {
      setStatus(s);
      setReason(r);
      onStatus?.(s, r);
    };

    const proto = window.location.protocol === "https:" ? "wss:" : "ws:";
    const url = `${proto}//${window.location.host}/api/vps/${vpsId}/terminal?cols=${term.cols}&rows=${term.rows}`;
    const ws = new WebSocket(url);
    ws.binaryType = "arraybuffer";
    const enc = new TextEncoder();

    ws.onopen = () => {
      update("connected");
      term.focus();
    };
    ws.onmessage = (ev) => {
      if (ev.data instanceof ArrayBuffer) term.write(new Uint8Array(ev.data));
      else term.write(String(ev.data));
    };
    ws.onclose = (ev) => {
      const why = ev.reason || (ev.code === 1006 ? "connection lost (is the VPS running and are you still signed in?)" : "");
      term.write(`\r\n\x1b[90m[disconnected${why ? ": " + why : ""}]\x1b[0m\r\n`);
      update("closed", why);
    };

    const input = term.onData((d) => {
      if (ws.readyState === WebSocket.OPEN) ws.send(enc.encode(d));
    });
    const binary = term.onBinary((d) => {
      if (ws.readyState !== WebSocket.OPEN) return;
      const buf = new Uint8Array(d.length);
      for (let i = 0; i < d.length; i++) buf[i] = d.charCodeAt(i) & 0xff;
      ws.send(buf);
    });
    const resized = term.onResize(({ cols, rows }) => {
      if (ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify({ type: "resize", cols, rows }));
    });

    const ro = new ResizeObserver(() => {
      try {
        fit.fit();
      } catch {
        /* element hidden */
      }
    });
    ro.observe(el);

    return () => {
      ro.disconnect();
      input.dispose();
      binary.dispose();
      resized.dispose();
      ws.close();
      term.dispose();
      termRef.current = null;
    };
    // onStatus intentionally not a dependency: reconnecting on every render would drop the session.
  }, [vpsId]);

  useEffect(() => {
    // Let layout settle (full-screen size change) before focusing.
    const t = setTimeout(() => termRef.current?.focus(), 50);
    return () => clearTimeout(t);
  }, [focusKey]);

  return (
    <div className="flex h-full flex-col">
      <div ref={host} className="min-h-0 flex-1 overflow-hidden rounded-[2px] border border-line-soft bg-term p-2" data-testid="terminal" />
      {status === "connecting" && <p className="mt-2 label-mono text-[10px] text-ink-faint">Connecting…</p>}
      {status === "closed" && reason && <p className="mt-2 label-mono text-[10px] text-ink-faint">Session ended: {reason}</p>}
    </div>
  );
}
