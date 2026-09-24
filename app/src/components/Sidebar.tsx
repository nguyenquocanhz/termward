import { useMemo, useState } from "react";
import { useShallow } from "zustand/react/shallow";
import {
  Activity,
  KeyRound,
  LayoutGrid,
  LoaderCircle,
  Moon,
  Plus,
  Power,
  Search,
  Settings2,
  SquareTerminal,
  Sun,
  Zap,
} from "lucide-react";
import logo from "../../assets/logo-64.png";
import { confirmQuit, navigate, openDialog, openTerminal, setPrefs, useApp, type View } from "../store";
import { useT } from "../lib/i18n";
import { severity } from "../lib/findings";
import { StatusDot, modKey } from "./ui";
import type { Host } from "../lib/api";

export function Sidebar({ dark }: { dark: boolean }) {
  const t = useT();
  const { hosts, statuses, view, tabs, settings, online, drawer } = useApp(
    useShallow((s) => ({
      hosts: s.hosts,
      statuses: s.statuses,
      view: s.view,
      tabs: s.tabs,
      settings: s.settings,
      online: s.online,
      drawer: s.drawer,
    })),
  );
  const [q, setQ] = useState("");

  const monitored = hosts.filter((h) => h.monitor);
  const attention = monitored.filter((h) => ["warn", "crit", "down"].includes(statuses[h.id]?.level ?? "")).length;

  const groups = useMemo(() => {
    const needle = q.trim().toLowerCase();
    const match = (h: Host) =>
      !needle || [h.name, h.address, h.user, h.group, ...h.tags].some((x) => x.toLowerCase().includes(needle));
    const lvl = (h: Host) => (h.monitor ? (statuses[h.id]?.level ?? "unknown") : "off");
    const map = new Map<string, Host[]>();
    for (const h of hosts.filter(match)) {
      const g = h.group || "";
      map.set(g, [...(map.get(g) ?? []), h]);
    }
    for (const list of map.values()) {
      list.sort((a, b) => severity[lvl(b)] - severity[lvl(a)] || a.name.localeCompare(b.name));
    }
    return [...map.entries()].sort(([a], [b]) => (a === "" ? 1 : b === "" ? -1 : a.localeCompare(b)));
  }, [hosts, statuses, q]);

  const nav: { view: View; icon: typeof LayoutGrid; label: string; count?: number; attention?: boolean }[] = [
    {
      view: { name: "overview" },
      icon: LayoutGrid,
      label: t("nav.overview"),
      count: attention || undefined,
      attention: attention > 0,
    },
    { view: { name: "terminals" }, icon: SquareTerminal, label: t("nav.terminals"), count: tabs.length || undefined },
    { view: { name: "run" }, icon: Zap, label: t("nav.run") },
    { view: { name: "keys" }, icon: KeyRound, label: t("nav.keys") },
    { view: { name: "settings" }, icon: Settings2, label: t("nav.settings") },
  ];

  const selectedHost = view.name === "host" ? view.hostId : null;

  return (
    <aside className={`sidebar${drawer ? " open" : ""}`}>
      <div className="sidebar-top drag">
        <div className="brand">
          <img src={logo} alt="" />
          Termward
        </div>
      </div>

      <div className="sidebar-body">
        <button className="new-host" onClick={() => openDialog({ kind: "host" })}>
          <Plus size={16} />
          {t("sidebar.newHost")}
          <span className="kbd">{modKey}+N</span>
        </button>

        {nav.map((n) => (
          <button
            key={n.view.name}
            className={`nav-item${view.name === n.view.name ? " active" : ""}`}
            onClick={() => navigate(n.view)}
          >
            <n.icon size={16} />
            {n.label}
            {n.count !== undefined && <span className={`count${n.attention ? " attention" : ""}`}>{n.count}</span>}
          </button>
        ))}

        <div className="sidebar-section">
          <span>{t("nav.hosts")}</span>
          <span className="faint">{hosts.length}</span>
        </div>
        {hosts.length > 6 && (
          <div className="sidebar-search">
            <Search size={14} />
            <input value={q} onChange={(e) => setQ(e.target.value)} placeholder={t("sidebar.search")} />
          </div>
        )}

        <div className="host-list">
          {groups.map(([group, list]) => (
            <div key={group || "_"}>
              {(groups.length > 1 || group) && <div className="host-group">{group || t("sidebar.ungrouped")}</div>}
              {list.map((h) => {
                const st = statuses[h.id];
                const cpu = st?.sample?.cpuPercent;
                return (
                  <button
                    key={h.id}
                    className={`host-row${selectedHost === h.id ? " active" : ""}`}
                    onClick={() => navigate({ name: "host", hostId: h.id })}
                    onDoubleClick={() => void openTerminal(h.id)}
                    title={`${h.user}@${h.address}`}
                  >
                    <StatusDot level={h.monitor ? (st?.level ?? "unknown") : "off"} checking={st?.checking} />
                    <span className="name truncate">{h.name}</span>
                    {h.monitor && cpu !== undefined && cpu >= 0 && <span className="metric">{Math.round(cpu)}%</span>}
                  </button>
                );
              })}
            </div>
          ))}
        </div>
      </div>

      <div className="sidebar-foot">
        {online ? (
          <>
            <Activity size={14} />
            <span className="truncate">
              {t("sidebar.watching", { n: monitored.length, s: settings?.pollIntervalSec ?? 30 })}
            </span>
          </>
        ) : (
          <>
            <LoaderCircle size={14} className="spin" />
            <span>{t("sidebar.offline")}</span>
          </>
        )}
        <span className="spacer" />
        <button
          className="icon-btn"
          title={t("pal.toggleTheme")}
          onClick={() => setPrefs({ theme: dark ? "light" : "dark" })}
        >
          {dark ? <Sun size={15} /> : <Moon size={15} />}
        </button>
        {window.termward?.quit && (
          <button className="icon-btn" title={t("app.quit")} onClick={confirmQuit}>
            <Power size={15} />
          </button>
        )}
      </div>
    </aside>
  );
}
