import { useShallow } from "zustand/react/shallow";
import { KeyRound, LayoutGrid, Settings2, SquareTerminal, Zap } from "lucide-react";
import { navigate, useApp, type View } from "../store";
import { useT } from "../lib/i18n";

/** Bottom tab bar on phones (hidden on wider screens by CSS). */
export function MobileNav() {
  const t = useT();
  const { view, tabs, attention } = useApp(
    useShallow((s) => ({
      view: s.view,
      tabs: s.tabs.length,
      attention: s.hosts.filter((h) => h.monitor && ["warn", "crit", "down"].includes(s.statuses[h.id]?.level ?? ""))
        .length,
    })),
  );
  const items: { view: View; icon: typeof LayoutGrid; label: string; badge?: number; alert?: boolean }[] = [
    { view: { name: "overview" }, icon: LayoutGrid, label: t("nav.overview"), badge: attention, alert: true },
    { view: { name: "terminals" }, icon: SquareTerminal, label: t("nav.terminals"), badge: tabs },
    { view: { name: "run" }, icon: Zap, label: t("nav.run") },
    { view: { name: "keys" }, icon: KeyRound, label: t("nav.keys") },
    { view: { name: "settings" }, icon: Settings2, label: t("nav.settings") },
  ];
  const current = view.name === "host" ? "overview" : view.name;

  return (
    <nav className="mobile-nav">
      {items.map((it) => (
        <button
          key={it.view.name}
          className={current === it.view.name ? "on" : ""}
          onClick={() => navigate(it.view)}
          aria-label={it.label}
        >
          <span className="ico">
            <it.icon size={20} />
            {!!it.badge && <span className={`pip${it.alert ? " alert" : ""}`}>{it.badge}</span>}
          </span>
          <span className="lbl">{it.label}</span>
        </button>
      ))}
    </nav>
  );
}
