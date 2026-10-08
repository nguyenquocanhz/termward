import { useEffect, useRef, useState } from "react";
import { create } from "zustand";
import { useShallow } from "zustand/react/shallow";
import {
  ClipboardPaste,
  Copy,
  CopyPlus,
  Eraser,
  LoaderCircle,
  Plus,
  RotateCw,
  SquareTerminal,
  TextSelect,
  X,
} from "lucide-react";
import { Terminal, type ITheme } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import { WebLinksAddon } from "@xterm/addon-web-links";
import "@xterm/xterm/css/xterm.css";
import { wsUrl } from "../lib/api";
import { t as tr, useT } from "../lib/i18n";
import { readClipboard, writeClipboard } from "../lib/clipboard";
import {
  closeOtherTabs,
  closeTab,
  errorText,
  openDialog,
  openTerminal,
  reconnectTab,
  toast,
  useApp,
  type TermTab,
} from "../store";
import { openContextMenu, type ContextItem } from "../components/ContextMenu";
import { Kbd, StatusDot, isMac, modKey } from "../components/ui";

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
/** Puts the keyboard back into a tab's terminal (after a menu, a tab click). */
const focusers = new Map<string, () => void>();
const focusActive = () => requestAnimationFrame(() => focusers.get(useApp.getState().activeTab ?? "")?.());
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

function tabMenu(tab: TermTab, count: number): ContextItem[] {
  return [
    { label: tr("term.closeTab"), icon: <X size={14} />, run: () => closeTab(tab.id) },
    { label: tr("term.closeOthers"), disabled: count < 2, run: () => closeOtherTabs(tab.id) },
    { separator: true },
    { label: tr("term.duplicate"), icon: <CopyPlus size={14} />, run: () => void openTerminal(tab.hostId) },
    { label: tr("term.reconnect"), icon: <RotateCw size={14} />, run: () => void reconnectTab(tab.id) },
  ];
}

async function copyToClipboard(text: string, okMessage?: string) {
  try {
    await writeClipboard(text);
    if (okMessage) toast("success", okMessage);
  } catch (e) {
    toast("error", tr("term.copyFailed"), errorText(e));
  }
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
          <div className="tabbar" role="tablist">
            {tabs.map((tab) => (
              <div
                key={tab.id}
                role="tab"
                aria-selected={tab.id === activeTab}
                className={`tab${tab.id === activeTab ? " active" : ""}`}
                onClick={() => {
                  useApp.setState({ activeTab: tab.id });
                  focusActive();
                }}
                onAuxClick={(e) => e.button === 1 && closeTab(tab.id)}
                onContextMenu={(e) =>
                  openContextMenu(e, tabMenu(tab, tabs.length), { label: tab.target, restoreFocus: focusActive })
                }
                title={[tab.target, tab.title, tab.via ? t("term.via", { host: tab.via }) : ""]
                  .filter(Boolean)
                  .join("\n")}
              >
                <StatusDot level={statuses[tab.hostId]?.level ?? "unknown"} />
                <span className="tab-text">
                  <span className="tab-target truncate">{tab.target}</span>
                  <span className="tab-name truncate">{tab.title}</span>
                </span>
                <span
                  className="x"
                  role="button"
                  aria-label={t("term.closeTab")}
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

const LONG_PRESS_MS = 550;

function TerminalPane({ tab, active }: { tab: TermTab; active: boolean }) {
  const t = useT();
  const host = useRef<HTMLDivElement>(null);
  const termRef = useRef<Terminal | null>(null);
  const fitRef = useRef<FitAddon | null>(null);
  const [state, setState] = useState<PaneState>({ kind: "connecting" });

  useEffect(() => {
    const el = host.current!;
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
    term.open(el);
    termRef.current = term;
    fitRef.current = fit;
    try {
      fit.fit();
    } catch {
      /* not laid out yet */
    }
    let disposed = false;
    const focus = () => {
      if (!disposed) term.focus();
    };

    // ---- copy & paste. Nothing here types into the shell except what the
    // user pastes, and that always goes through term.paste() so programs that
    // asked for bracketed paste get it.
    const copySelection = () => {
      const sel = term.getSelection();
      if (!sel) return;
      void copyToClipboard(sel);
      term.clearSelection();
    };
    const pasteText = (raw: string) => {
      if (disposed || !raw) return;
      const text = raw.replace(/\r\n?/g, "\n");
      const body = text.replace(/\n+$/, "");
      if (body === "" || !body.includes("\n")) {
        // One line: leave the Enter to the user, even when a line break was
        // copied along with it.
        term.paste(body === "" ? text : body);
        focus();
        return;
      }
      if (!useApp.getState().confirmPaste) {
        term.paste(text);
        focus();
        return;
      }
      openDialog({
        kind: "paste",
        text,
        target: tab.target,
        onPaste: () => {
          if (!disposed) term.paste(text);
        },
        onClose: () => requestAnimationFrame(focus),
      });
    };
    const pasteClipboard = async () => {
      try {
        pasteText(await readClipboard());
      } catch {
        toast("error", tr("term.pasteDenied"), tr("term.pasteDeniedBody"));
      }
    };

    // Windows/Linux: Ctrl+C copies a selection and is SIGINT without one,
    // Ctrl+V pastes. macOS: Cmd+C / Cmd+V, and Ctrl+C is always SIGINT.
    // Everywhere: Ctrl+Shift+C / Ctrl+Shift+V, Ctrl+Insert / Shift+Insert.
    term.attachCustomKeyEventHandler((e) => {
      if (e.type !== "keydown") return true;
      const isC = e.code === "KeyC";
      const isV = e.code === "KeyV";
      let action: "copy" | "paste" | null = null;
      let interrupt = false; // the key is SIGINT when there is nothing to copy
      if (e.code === "Insert" && !e.altKey && !e.metaKey) {
        if (e.ctrlKey && !e.shiftKey) action = "copy";
        else if (e.shiftKey && !e.ctrlKey) action = "paste";
      } else if ((isC || isV) && !e.altKey) {
        if (e.ctrlKey && e.shiftKey && !e.metaKey) action = isC ? "copy" : "paste";
        else if (isMac && e.metaKey && !e.ctrlKey && !e.shiftKey) {
          // Cmd+C / Cmd+V are also the Edit menu's shortcuts: leave them to
          // the browser, whose copy event xterm answers with the selection
          // and whose paste event onNativePaste below takes. Handling them
          // here as well could copy or paste twice.
          return false;
        } else if (!isMac && e.ctrlKey && !e.shiftKey && !e.metaKey) {
          action = isC ? "copy" : "paste";
          interrupt = isC;
        }
      }
      if (!action) return true;
      if (action === "copy") {
        if (interrupt && !term.hasSelection()) return true;
        e.preventDefault();
        copySelection();
        return false;
      }
      // preventDefault keeps the browser from also firing its own paste.
      e.preventDefault();
      void pasteClipboard();
      return false;
    });

    // Pastes that do not come from our shortcuts (the macOS Edit menu, the
    // paste bubble of a phone) take the same path, confirmation included.
    const onNativePaste = (e: ClipboardEvent) => {
      e.preventDefault();
      e.stopPropagation();
      pasteText(e.clipboardData?.getData("text/plain") ?? "");
    };
    el.addEventListener("paste", onNativePaste, true);

    // ---- right-click / long-press menu
    const openMenu = (x: number, y: number) => {
      const items: ContextItem[] = [
        {
          label: tr("common.copy"),
          icon: <Copy size={14} />,
          hint: isMac ? "⌘C" : "Ctrl+C",
          disabled: !term.hasSelection(),
          run: copySelection,
        },
        {
          label: tr("term.paste"),
          icon: <ClipboardPaste size={14} />,
          hint: isMac ? "⌘V" : "Ctrl+V",
          run: () => void pasteClipboard(),
        },
        { label: tr("term.selectAll"), icon: <TextSelect size={14} />, run: () => term.selectAll() },
        // xterm drops its own scrollback: no command is sent to the server.
        { label: tr("term.clear"), icon: <Eraser size={14} />, run: () => term.clear() },
        { separator: true },
        {
          label: tr("term.copyTarget", { target: tab.target }),
          run: () => void copyToClipboard(tab.target, tr("common.copied")),
        },
        { separator: true },
        { label: tr("term.reconnect"), icon: <RotateCw size={14} />, run: () => void reconnectTab(tab.id) },
        { label: tr("term.closeTab"), icon: <X size={14} />, run: () => closeTab(tab.id) },
      ];
      openContextMenu({ clientX: x, clientY: y }, items, { label: tab.target, restoreFocus: focus });
    };

    let pressTimer: number | undefined;
    let pressed = false; // a long press opened the menu: swallow the tap that ends it
    let startX = 0;
    let startY = 0;
    const cancelPress = () => {
      window.clearTimeout(pressTimer);
      pressTimer = undefined;
    };
    const onContextMenu = (e: MouseEvent) => {
      const touch = (e as PointerEvent).pointerType === "touch";
      e.preventDefault();
      // vim, tmux, htop and mc ask for the mouse: the right button is theirs
      // (xterm has already sent it). Shift+right-click still opens our menu.
      if (term.modes.mouseTrackingMode !== "none" && !e.shiftKey && !touch) return;
      if (touch) {
        cancelPress();
        pressed = true;
      }
      openMenu(e.clientX, e.clientY);
    };
    const onTouchStart = (e: TouchEvent) => {
      cancelPress();
      pressed = false;
      if (e.touches.length !== 1) return;
      startX = e.touches[0].clientX;
      startY = e.touches[0].clientY;
      // iOS never sends "contextmenu" for a long press, so time it ourselves.
      pressTimer = window.setTimeout(() => {
        pressed = true;
        openMenu(startX, startY);
      }, LONG_PRESS_MS);
    };
    const onTouchMove = (e: TouchEvent) => {
      const p = e.touches[0];
      if (!p || Math.abs(p.clientX - startX) > 10 || Math.abs(p.clientY - startY) > 10) cancelPress();
    };
    const onTouchEnd = (e: TouchEvent) => {
      cancelPress();
      if (pressed) {
        pressed = false;
        if (e.cancelable) e.preventDefault(); // no click on the menu item under the finger
      }
    };
    el.addEventListener("contextmenu", onContextMenu);
    el.addEventListener("touchstart", onTouchStart, { passive: true });
    el.addEventListener("touchmove", onTouchMove, { passive: true });
    el.addEventListener("touchend", onTouchEnd);
    el.addEventListener("touchcancel", cancelPress);

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
    focusers.set(tab.id, focus);
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
      if (el.offsetWidth > 0) {
        try {
          fit.fit();
        } catch {
          /* ignore */
        }
      }
    });
    ro.observe(el);

    return () => {
      disposed = true;
      closedByUs = true;
      cancelPress();
      el.removeEventListener("paste", onNativePaste, true);
      el.removeEventListener("contextmenu", onContextMenu);
      el.removeEventListener("touchstart", onTouchStart);
      el.removeEventListener("touchmove", onTouchMove);
      el.removeEventListener("touchend", onTouchEnd);
      el.removeEventListener("touchcancel", cancelPress);
      senders.delete(tab.id);
      if (focusers.get(tab.id) === focus) focusers.delete(tab.id);
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

  const stateText =
    state.kind === "open"
      ? t("term.connected")
      : state.kind === "connecting"
        ? t("term.stateConnecting")
        : t("term.disconnected");

  return (
    <div className={`term-pane${active ? "" : " hidden"}`}>
      {/* Who and where this session is. The shell prompt belongs to the
          server (it may well read user@user), so this is said here instead. */}
      <div className={`term-bar ${state.kind}`}>
        <StatusDot
          level={state.kind === "open" ? "ok" : state.kind === "connecting" ? "unknown" : "down"}
          checking={state.kind === "connecting"}
        />
        <span className="term-target selectable" title={tab.target}>
          {tab.target}
        </span>
        <button
          className="icon-btn"
          title={t("term.copyTarget", { target: tab.target })}
          aria-label={t("term.copyTarget", { target: tab.target })}
          onClick={() => {
            void copyToClipboard(tab.target, t("common.copied"));
            termRef.current?.focus();
          }}
        >
          <Copy size={13} />
        </button>
        <span className="term-name truncate">
          {tab.title}
          {tab.via && <span className="term-via"> · {t("term.via", { host: tab.via })}</span>}
        </span>
        <span className="term-state" role="status">
          {stateText}
        </span>
      </div>
      <div className="term-body">
        <div ref={host} style={{ height: "100%" }} />
        {state.kind !== "open" && (
          <div className="term-status">
            <div className="box">
              {state.kind === "connecting" ? (
                <>
                  <LoaderCircle size={18} className="spin" />
                  {t("term.connecting", { host: tab.target })}
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
    </div>
  );
}
