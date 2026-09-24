import { useEffect, useMemo, useRef, useState } from "react";
import { useShallow } from "zustand/react/shallow";
import {
  FileInput,
  KeyRound,
  LayoutGrid,
  Moon,
  Plus,
  Power,
  Search,
  Server,
  Settings2,
  SquareTerminal,
  Zap,
} from "lucide-react";
import { useT } from "../lib/i18n";
import { caps } from "../lib/platform";
import { confirmQuit, navigate, openDialog, openTerminal, setPrefs, useApp } from "../store";
import { StatusDot } from "./ui";

interface Item {
  id: string;
  group: "servers" | "actions";
  label: string;
  hint?: string;
  icon: React.ReactNode;
  run: () => void;
}

/** Ctrl/⌘+K: jump to any server (Enter = terminal, Shift+Enter = details) or action. */
export function Palette({ dark }: { dark: boolean }) {
  const t = useT();
  const { open, hosts, statuses } = useApp(
    useShallow((s) => ({ open: s.palette, hosts: s.hosts, statuses: s.statuses })),
  );
  const [q, setQ] = useState("");
  const [idx, setIdx] = useState(0);
  const listRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (open) {
      setQ("");
      setIdx(0);
    }
  }, [open]);

  const close = () => useApp.setState({ palette: false });

  const items = useMemo<Item[]>(() => {
    const servers: Item[] = hosts.map((h) => ({
      id: `h:${h.id}`,
      group: "servers",
      label: h.name,
      hint: `${h.user}@${h.address}`,
      icon: <StatusDot level={h.monitor ? (statuses[h.id]?.level ?? "unknown") : "off"} />,
      run: () => void openTerminal(h.id),
    }));
    const actions: Item[] = [
      {
        id: "a:new",
        group: "actions",
        label: t("sidebar.newHost"),
        icon: <Plus size={15} />,
        run: () => openDialog({ kind: "host" }),
      },
      {
        id: "a:overview",
        group: "actions",
        label: t("nav.overview"),
        icon: <LayoutGrid size={15} />,
        run: () => navigate({ name: "overview" }),
      },
      {
        id: "a:terms",
        group: "actions",
        label: t("nav.terminals"),
        icon: <SquareTerminal size={15} />,
        run: () => navigate({ name: "terminals" }),
      },
      {
        id: "a:run",
        group: "actions",
        label: t("nav.run"),
        icon: <Zap size={15} />,
        run: () => navigate({ name: "run" }),
      },
      {
        id: "a:keys",
        group: "actions",
        label: t("nav.keys"),
        icon: <KeyRound size={15} />,
        run: () => navigate({ name: "keys" }),
      },
      {
        id: "a:genkey",
        group: "actions",
        label: t("keys.generate"),
        icon: <KeyRound size={15} />,
        run: () => openDialog({ kind: "generateKey" }),
      },
      ...(caps.sshDir
        ? [
            {
              id: "a:import",
              group: "actions" as const,
              label: t("overview.importConfig"),
              icon: <FileInput size={15} />,
              run: () => openDialog({ kind: "importConfig" }),
            },
          ]
        : []),
      {
        id: "a:settings",
        group: "actions",
        label: t("nav.settings"),
        icon: <Settings2 size={15} />,
        run: () => navigate({ name: "settings" }),
      },
      {
        id: "a:theme",
        group: "actions",
        label: t("pal.toggleTheme"),
        icon: <Moon size={15} />,
        run: () => setPrefs({ theme: dark ? "light" : "dark" }),
      },
      ...(window.termward?.quit
        ? [
            {
              id: "a:quit",
              group: "actions" as const,
              label: t("app.quit"),
              icon: <Power size={15} />,
              run: confirmQuit,
            },
          ]
        : []),
    ];
    const needle = q.trim().toLowerCase();
    const score = (it: Item) => {
      if (!needle) return 1;
      const hay = `${it.label} ${it.hint ?? ""}`.toLowerCase();
      if (hay.startsWith(needle)) return 3;
      if (hay.includes(needle)) return 2;
      // loose subsequence match: "wb1" finds "web-01"
      let i = 0;
      for (const ch of hay) if (ch === needle[i]) i++;
      return i === needle.length ? 1 : 0;
    };
    return [...servers, ...actions]
      .map((it) => ({ it, s: score(it) }))
      .filter((x) => x.s > 0)
      .sort((a, b) => (a.it.group === b.it.group ? b.s - a.s : a.it.group === "servers" ? -1 : 1))
      .map((x) => x.it);
  }, [hosts, statuses, q, t, dark]);

  useEffect(() => setIdx(0), [q]);
  useEffect(() => {
    listRef.current?.querySelector(".palette-item.on")?.scrollIntoView({ block: "nearest" });
  }, [idx]);

  if (!open) return null;

  const choose = (it: Item | undefined, details = false) => {
    if (!it) return;
    close();
    if (details && it.id.startsWith("h:")) navigate({ name: "host", hostId: it.id.slice(2) });
    else it.run();
  };

  let lastGroup = "";
  return (
    <div className="palette" onMouseDown={(e) => e.target === e.currentTarget && close()}>
      <div className="palette-box">
        <div className="palette-input">
          <Search size={17} className="muted" />
          <input
            autoFocus
            value={q}
            placeholder={t("pal.placeholder")}
            onChange={(e) => setQ(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "ArrowDown") {
                e.preventDefault();
                setIdx((i) => Math.min(i + 1, items.length - 1));
              } else if (e.key === "ArrowUp") {
                e.preventDefault();
                setIdx((i) => Math.max(i - 1, 0));
              } else if (e.key === "Enter") {
                choose(items[idx], e.shiftKey);
              } else if (e.key === "Escape") {
                close();
              }
            }}
          />
        </div>
        <div className="palette-list" ref={listRef}>
          {items.length === 0 && (
            <div className="empty" style={{ padding: 24 }}>
              {t("pal.empty")}
            </div>
          )}
          {items.map((it, i) => {
            const header = it.group !== lastGroup ? (lastGroup = it.group) : null;
            return (
              <div key={it.id}>
                {header && (
                  <div className="palette-group">{header === "servers" ? t("pal.servers") : t("pal.actions")}</div>
                )}
                <div
                  className={`palette-item${i === idx ? " on" : ""}`}
                  onMouseMove={() => setIdx(i)}
                  onClick={() => choose(it)}
                >
                  <span style={{ width: 18, display: "grid", placeItems: "center" }}>{it.icon}</span>
                  <span className="truncate">{it.label}</span>
                  {it.hint && (
                    <span className="mono faint truncate" style={{ fontSize: 12 }}>
                      {it.hint}
                    </span>
                  )}
                  {it.group === "servers" && i === idx && (
                    <span className="hint row" style={{ gap: 4 }}>
                      <SquareTerminal size={12} /> ↵ · <Server size={12} /> ⇧↵
                    </span>
                  )}
                </div>
              </div>
            );
          })}
        </div>
      </div>
    </div>
  );
}
