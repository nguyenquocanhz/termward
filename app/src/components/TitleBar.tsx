import { ChevronLeft, ChevronRight, Menu, Search } from "lucide-react";
import { navigate, useApp } from "../store";
import { useT } from "../lib/i18n";
import { modKey } from "./ui";

export function TitleBar() {
  const t = useT();
  const view = useApp((s) => s.view);
  const hostName = useApp((s) =>
    s.view.name === "host" ? s.hosts.find((h) => h.id === (s.view as { hostId: string }).hostId)?.name : undefined,
  );

  const crumbs: string[] = (() => {
    switch (view.name) {
      case "overview":
        return [t("nav.overview")];
      case "host":
        return [t("nav.hosts"), hostName ?? ""];
      case "terminals":
        return [t("nav.terminals")];
      case "keys":
        return [t("nav.keys")];
      case "run":
        return [t("nav.run")];
      case "settings":
        return [t("nav.settings")];
    }
  })();

  return (
    <header className="titlebar drag">
      {/* Phones: back from a server, otherwise open the drawer. */}
      {view.name === "host" ? (
        <button className="icon-btn mobile-only" onClick={() => navigate({ name: "overview" })} aria-label="Back">
          <ChevronLeft size={20} />
        </button>
      ) : (
        <button
          className="icon-btn mobile-only"
          onClick={() => useApp.setState({ drawer: true })}
          aria-label={t("mobile.menu")}
        >
          <Menu size={19} />
        </button>
      )}
      <div className="crumb">
        {crumbs.map((c, i) => (
          <span key={i} className={`row${i < crumbs.length - 1 ? " desktop-only" : ""}`} style={{ gap: 6 }}>
            {i > 0 && <ChevronRight size={14} className="faint desktop-only" />}
            {i === crumbs.length - 1 ? <strong className="truncate">{c}</strong> : <span>{c}</span>}
          </span>
        ))}
      </div>
      <span className="spacer" />
      <button
        className="search-trigger"
        onClick={() => useApp.setState({ palette: true })}
        aria-label={t("title.search")}
      >
        <Search size={14} />
        <span className="desktop-only">{t("title.search")}</span>
        <span className="spacer desktop-only" />
        <span className="kbd desktop-only">{modKey}+K</span>
      </button>
    </header>
  );
}
