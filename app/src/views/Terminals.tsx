import { useEffect, useRef, useState } from "react";
import { create } from "zustand";
import { useShallow } from "zustand/react/shallow";
import { LoaderCircle, Plus, RotateCw, SquareTerminal, X } from "lucide-react";
import { Terminal, type ITheme } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import { WebLinksAddon } from "@xterm/addon-web-links";
import "@xterm/xterm/css/xterm.css";
import { wsUrl } from "../lib/api";
import { useT } from "../lib/i18n";
import { closeTab, reconnectTab, useApp, type TermTab } from "../store";
import { Kbd, StatusDot, modKey } from "../components/ui";

// Warm palette tuned to the app, readable in both themes.
const theme: ITheme = {
  background: "#1f1e1d",
  foreground: "#e8e6dc",
  cursor: "#d97757",
  cursorAccent: "#1f1e1d",
  selectionBackground: "rgba(217,119,87,0.35)",
  black: "#2b2a28",
  red: "#e5735c",
  green: "#8fbf7f",
  yellow: "#e2b867",
  blue: "#7fa4d8",
  magenta: "#c79bd6",
  cyan: "#7cc4c0",
  white: "#dcdad1",
  brightBlack: "#6f6d66",
  brightRed: "#f08a74",
  brightGreen: "#a6d494",
  brightYellow: "#f0cc80",
  brightBlue: "#99b9e8",
  brightMagenta: "#d9b3e6",
  brightCyan: "#98d6d2",
  brightWhite: "#f5f4ee",
};

// Phone keyboards lack Esc/Tab/Ctrl/arrows: the key bar sends them to the
// active tab, and Ctrl/Alt latch onto the next key typed.
const senders = new Map<string, (data: string) => void>();
const useLatch = create<{ ctrl: boolean; alt: boolean }>(() => ({ ctrl: false, alt: false }));

function applyLatch(d: string): string {
  const { ctrl, alt } = useLatch.getState();
  if (!ctrl && !alt) return d;
  useLatch.setState({ ctrl: false, alt: false });
  let out = d;
  if (ctrl && d.length === 1) {
    const c = d.toLowerCase().charCodeAt(0);
    if (c >= 97 && c <= 122)
      out = String.fromCharCode(c - 96); // Ctrl+A..Z
    else if (d === "[") out = "\x1b";
    else if (d === " ") out = "\x00";
  }
  return alt ? "\x1b" + out : out;
}

const barKeys: { label: string; send?: string; latch?: "ctrl" | "alt" }[] = [
  { label: "Esc", send: "\x1b" },
  { label: "Tab", send: "\t" },
  { label: "Ctrl", latch: "ctrl" },
  { label: "Alt", latch: "alt" },
  { label: "←", send: "\x1b[D" },
  { label: "↑", send: "\x1b[A" },
  { label: "↓", send: "\x1b[B" },
  { label: "→", send: "\x1b[C" },
  { label: "|", send: "|" },
  { label: "~", send: "~" },
  { label: "/", send: "/" },
  { label: "-", send: "-" },
  { label: "Home", send: "\x1b[H" },
  { label: "End", send: "\x1b[F" },
];

function KeyBar({ tabId }: { tabId: string | null }) {
  const t = useT();
  const latch = useLatch();
  return (
    <div className="keybar" role="toolbar" aria-label={t("term.keys")}>
      {barKeys.map((k) => (
        <button
          key={k.label}
          className={k.latch && latch[k.latch] ? "on" : ""}
          // Keep focus in the terminal so the on-screen keyboard stays open.
          onPointerDown={(e) => e.preventDefault()}
          onClick={() => {
            if (k.latch) useLatch.setState({ [k.latch]: !latch[k.latch] });
            else if (tabId && k.send) senders.get(tabId)?.(applyLatch(k.send));
          }}
        >
          {k.label}
        </button>
      ))}
    </div>
  );
}

/** Always mounted (hidden when another view is active) so sessions survive navigation. */
export function Terminals({ visible }: { visible: boolean }) {
  const t = useT();
  const { tabs, activeTab, statuses } = useApp(
    useShallow((s) => ({ tabs: s.tabs, activeTab: s.activeTab, statuses: s.statuses })),
  );

  return (
    <div className={`terminals${visible ? "" : " hidden"}`}>
      {tabs.length === 0 ? (
        <div className="empty" style={{ margin: "auto" }}>
          <div className="empty-art">
            <SquareTerminal size={26} />
          </div>
          <h3>{t("term.empty")}</h3>
          <p>
            {(() => {
              const [before, after] = t("term.emptyBody", { kbd: "\u0001" }).split("\u0001");
              return (
                <>
                  {before}
                  <Kbd>{modKey}+K</Kbd>
                  {after}
                </>
              );
            })()}
          </p>
        </div>
      ) : (
        <>
          <div className="tabbar">
            {tabs.map((tab) => (
              <div
                key={tab.id}
                className={`tab${tab.id === activeTab ? " active" : ""}`}
                onClick={() => useApp.setState({ activeTab: tab.id })}
                onAuxClick={(e) => e.button === 1 && closeTab(tab.id)}
                title={tab.title}
              >
                <StatusDot level={statuses[tab.hostId]?.level ?? "unknown"} />
                <span className="truncate">{tab.title}</span>
                <span
                  className="x"
                  onClick={(e) => {
                    e.stopPropagation();
                    closeTab(tab.id);
                  }}
                >
                  <X size={12} />
                </span>
              </div>
            ))}
            <button
              className="icon-btn"
              style={{ alignSelf: "center", marginLeft: 4 }}
              title={t("term.new")}
              onClick={() => useApp.setState({ palette: true })}
            >
              <Plus size={15} />
            </button>
          </div>
          <div className="term-stage">
            {tabs.map((tab) => (
              <TerminalPane key={`${tab.id}:${tab.nonce}`} tab={tab} active={visible && tab.id === activeTab} />
            ))}
          </div>
          <KeyBar tabId={activeTab} />
        </>
      )}
    </div>
  );
}

type PaneState = { kind: "connecting" } | { kind: "open" } | { kind: "closed"; code?: number; message?: string };

function TerminalPane({ tab, active }: { tab: TermTab; active: boolean }) {
  const t = useT();
  const host = useRef<HTMLDivElement>(null);
  const termRef = useRef<Terminal | null>(null);
  const fitRef = useRef<FitAddon | null>(null);
  const [state, setState] = useState<PaneState>({ kind: "connecting" });

  useEffect(() => {
    const term = new Terminal({
      theme,
      fontFamily: '"JetBrains Mono Variable", "Cascadia Mono", Menlo, Consolas, monospace',
      fontSize: window.innerWidth < 600 ? 12 : 13.5,
      lineHeight: 1.25,
      cursorBlink: true,
      cursorStyle: "bar",
      scrollback: 10000,
      allowProposedApi: true,
      macOptionIsMeta: true,
    });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.loadAddon(new WebLinksAddon((_e, uri) => window.termward?.openExternal(uri) ?? window.open(uri, "_blank")));
    term.open(host.current!);
    termRef.current = term;
    fitRef.current = fit;
    try {
      fit.fit();
    } catch {
      /* not laid out yet */
    }

    // Ctrl+Shift+C / Ctrl+Shift+V copy & paste; plain Ctrl+C stays SIGINT.
    term.attachCustomKeyEventHandler((e) => {
      if (e.type !== "keydown") return true;
      const mod = e.ctrlKey || e.metaKey;
      if (mod && e.shiftKey && e.code === "KeyC") {
        const sel = term.getSelection();
        if (sel) void (window.termward?.copy(sel) ?? navigator.clipboard.writeText(sel));
        return false;
      }
      if (mod && e.shiftKey && e.code === "KeyV") {
        void navigator.clipboard
          .readText()
          .then((txt) => term.paste(txt))
          .catch(() => {});
        return false;
      }
      return true;
    });

    const ws = new WebSocket(wsUrl("/ws/terminal", { hostId: tab.hostId, cols: term.cols, rows: term.rows }));
    ws.binaryType = "arraybuffer";
    const enc = new TextEncoder();
    let closedByUs = false;

    ws.onmessage = (m) => {
      if (m.data instanceof ArrayBuffer) {
        term.write(new Uint8Array(m.data));
        return;
      }
      const msg = JSON.parse(m.data as string);
      if (msg.type === "ready") {
        setState({ kind: "open" });
        term.focus();
      } else if (msg.type === "exit") {
        setState({ kind: "closed", code: msg.code });
      } else if (msg.type === "error") {
        setState({ kind: "closed", message: msg.message });
      }
    };
    ws.onclose = () => {
      if (!closedByUs) setState((s) => (s.kind === "closed" ? s : { kind: "closed" }));
    };
    const send = (d: string) => {
      if (ws.readyState === WebSocket.OPEN) ws.send(enc.encode(d));
    };
    senders.set(tab.id, (d) => {
      send(d);
      term.focus();
    });
    const data = term.onData((d) => send(applyLatch(d)));
    const bin = term.onBinary((d) => {
      if (ws.readyState !== WebSocket.OPEN) return;
      const buf = new Uint8Array(d.length);
      for (let i = 0; i < d.length; i++) buf[i] = d.charCodeAt(i) & 0xff;
      ws.send(buf);
    });
    const resize = term.onResize(({ cols, rows }) => {
      if (ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify({ type: "resize", cols, rows }));
    });

    const ro = new ResizeObserver(() => {
      if (host.current && host.current.offsetWidth > 0) {
        try {
          fit.fit();
        } catch {
          /* ignore */
        }
      }
    });
    ro.observe(host.current!);

    return () => {
      closedByUs = true;
      senders.delete(tab.id);
      ro.disconnect();
      data.dispose();
      bin.dispose();
      resize.dispose();
      ws.close();
      term.dispose();
    };
  }, [tab.hostId]);

  useEffect(() => {
    if (!active) return;
    requestAnimationFrame(() => {
      try {
        fitRef.current?.fit();
      } catch {
        /* ignore */
      }
      termRef.current?.focus();
    });
  }, [active]);

  return (
    <div className={`term-pane${active ? "" : " hidden"}`}>
      <div ref={host} style={{ height: "100%" }} />
      {state.kind !== "open" && (
        <div className="term-status">
          <div className="box">
            {state.kind === "connecting" ? (
              <>
                <LoaderCircle size={18} className="spin" />
                {t("term.connecting", { host: tab.title })}
              </>
            ) : (
              <>
                <span>
                  {state.message ??
                    (state.code !== undefined ? t("term.closed", { code: state.code }) : t("term.lost"))}
                </span>
                <div className="row">
                  <button className="btn sm" onClick={() => closeTab(tab.id)}>
                    {t("common.close")}
                  </button>
                  <button className="btn sm primary" onClick={() => void reconnectTab(tab.id)}>
                    <RotateCw size={13} />
                    {t("term.reconnect")}
                  </button>
                </div>
              </>
            )}
          </div>
        </div>
      )}
    </div>
  );
}
