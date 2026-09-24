import { useEffect, useState } from "react";
import { useShallow } from "zustand/react/shallow";
import { LoaderCircle, RotateCw } from "lucide-react";
import logo from "../assets/logo-64.png";
import { boot, goBack, navigate, openDialog, useApp, type ThemePref } from "./store";
import { resolveLang, useT } from "./lib/i18n";
import { isMobile, platform } from "./lib/platform";
import { MobileNav } from "./components/MobileNav";
import { Sidebar } from "./components/Sidebar";
import { TitleBar } from "./components/TitleBar";
import { DialogHost } from "./components/Dialogs";
import { Palette } from "./components/Palette";
import { Toasts } from "./components/Toasts";
import { ErrorBoundary } from "./components/ErrorBoundary";
import { Overview } from "./views/Overview";
import { HostDetail } from "./views/HostDetail";
import { Terminals } from "./views/Terminals";
import { Keys } from "./views/Keys";
import { Run } from "./views/Run";
import { Settings } from "./views/Settings";

function useDark(pref: ThemePref): boolean {
  const mq = window.matchMedia("(prefers-color-scheme: dark)");
  const [system, setSystem] = useState(mq.matches);
  useEffect(() => {
    const on = (e: MediaQueryListEvent) => setSystem(e.matches);
    mq.addEventListener("change", on);
    return () => mq.removeEventListener("change", on);
  }, [mq]);
  return pref === "system" ? system : pref === "dark";
}

export function App() {
  const t = useT();
  const { booted, bootError, view, theme, lang, drawer } = useApp(
    useShallow((s) => ({
      booted: s.booted,
      bootError: s.bootError,
      view: s.view,
      theme: s.theme,
      lang: s.lang,
      drawer: s.drawer,
    })),
  );
  const dark = useDark(theme);

  useEffect(() => {
    void boot();
  }, []);

  useEffect(() => {
    document.documentElement.dataset.theme = dark ? "dark" : "light";
    window.termward?.setTheme(dark ? "dark" : "light");
  }, [dark]);

  useEffect(() => {
    document.documentElement.lang = resolveLang(lang);
  }, [lang]);

  // The mobile bridge is installed during boot, so the platform is known only then.
  useEffect(() => {
    if (!booted) return;
    document.documentElement.classList.add(`platform-${platform()}`);
    if (isMobile()) void import("./lib/mobile").then((m) => m.handleBackButton(goBack));
  }, [booted]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const mod = e.ctrlKey || e.metaKey;
      if (!mod || e.altKey) return;
      if (e.key.toLowerCase() === "k" && !e.shiftKey) {
        e.preventDefault();
        useApp.setState((s) => ({ palette: !s.palette }));
      } else if (e.key.toLowerCase() === "n" && !e.shiftKey) {
        e.preventDefault();
        openDialog({ kind: "host" });
      } else if (e.key === ",") {
        e.preventDefault();
        navigate({ name: "settings" });
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  if (!booted) {
    return (
      <div className="boot">
        <div className="box">
          <img src={logo} width={44} height={44} alt="" style={{ borderRadius: 12 }} />
          {bootError ? (
            <>
              <strong>{t("boot.failed")}</strong>
              <span className="mono muted selectable">{bootError}</span>
              <button className="btn" onClick={() => location.reload()}>
                <RotateCw size={14} />
                {t("common.retry")}
              </button>
            </>
          ) : (
            <span className="row">
              <LoaderCircle size={15} className="spin" />
              {t("boot.starting")}
            </span>
          )}
        </div>
      </div>
    );
  }

  return (
    <div className={`app view-${view.name}`}>
      <Sidebar dark={dark} />
      {drawer && <div className="drawer-backdrop" onClick={() => useApp.setState({ drawer: false })} />}
      <main className="main">
        <TitleBar />
        {view.name !== "terminals" && (
          <div className="content">
            <ErrorBoundary resetKey={view.name === "host" ? view.hostId : view.name}>
              {view.name === "overview" && <Overview />}
              {view.name === "host" && <HostDetail key={view.hostId} hostId={view.hostId} />}
              {view.name === "keys" && <Keys />}
              {view.name === "run" && <Run />}
              {view.name === "settings" && <Settings />}
            </ErrorBoundary>
          </div>
        )}
        <ErrorBoundary>
          <Terminals visible={view.name === "terminals"} />
        </ErrorBoundary>
        <MobileNav />
      </main>
      <ErrorBoundary resetKey={JSON.stringify(view)}>
        <DialogHost />
      </ErrorBoundary>
      <Palette dark={dark} />
      <Toasts />
    </div>
  );
}
