import { useEffect, useState } from "react";
import { ExternalLink } from "lucide-react";
import { api, type Settings as SettingsT, type Thresholds } from "../lib/api";
import { useT, type TKey } from "../lib/i18n";
import { errorText, setPrefs, toast, useApp, type LangPref, type ThemePref } from "../store";
import { Segmented, Switch } from "../components/ui";
import { isMobile, platform } from "../lib/platform";

const REPO = "https://github.com/nguyenquocanhz/termward";

export function Settings() {
  const t = useT();
  const saved = useApp((s) => s.settings);
  const theme = useApp((s) => s.theme);
  const lang = useApp((s) => s.lang);
  const [draft, setDraft] = useState<SettingsT | null>(saved);
  const [version, setVersion] = useState("");

  useEffect(() => setDraft(saved), [saved]);
  useEffect(() => {
    void fetchVersion().then(setVersion);
  }, []);

  if (!draft) return null;
  const dirty = JSON.stringify(draft) !== JSON.stringify(saved);

  const save = async (next = draft) => {
    try {
      const s = await api.saveSettings(next);
      useApp.setState({ settings: s });
      toast("success", t("common.saved"));
    } catch (e) {
      toast("error", errorText(e));
    }
  };

  const setTh = (k: keyof Thresholds, v: number) => setDraft({ ...draft, thresholds: { ...draft.thresholds, [k]: v } });

  const rows: { label: TKey; warn: keyof Thresholds; crit: keyof Thresholds; step: number }[] = [
    { label: "set.cpu", warn: "cpuWarn", crit: "cpuCrit", step: 1 },
    { label: "set.mem", warn: "memWarn", crit: "memCrit", step: 1 },
    { label: "set.disk", warn: "diskWarn", crit: "diskCrit", step: 1 },
    { label: "set.load", warn: "loadPerCoreWarn", crit: "loadPerCoreCrit", step: 0.1 },
  ];

  return (
    <div className="page" style={{ maxWidth: 820 }}>
      <div className="page-head">
        <div>
          <h1>{t("nav.settings")}</h1>
        </div>
        <div className="actions">
          <button className="btn primary" disabled={!dirty} onClick={() => void save()}>
            {t("set.save")}
          </button>
        </div>
      </div>

      <h2 className="section" style={{ marginTop: 0 }}>
        {t("set.monitoring")}
      </h2>
      <div className="card">
        <SettingRow title={t("set.interval")} hint={t("set.intervalHint")}>
          <select
            className="select"
            style={{ width: 160 }}
            value={draft.pollIntervalSec}
            onChange={(e) => setDraft({ ...draft, pollIntervalSec: Number(e.target.value) })}
          >
            {[10, 15, 30, 60, 120, 300, 600].map((s) => (
              <option key={s} value={s}>
                {s < 60 ? t("set.seconds", { n: s }) : t("set.minutes", { n: s / 60 })}
              </option>
            ))}
          </select>
        </SettingRow>
        <SettingRow title={t("set.notifications")} hint={t("set.notificationsHint")} last>
          <Switch on={draft.notifications} onChange={(v) => setDraft({ ...draft, notifications: v })} />
        </SettingRow>
      </div>

      <h2 className="section">{t("set.thresholds")}</h2>
      <p className="muted" style={{ margin: "-4px 0 12px" }}>
        {t("set.thresholdsHint")}
      </p>
      <div className="card">
        <table className="table">
          <thead>
            <tr>
              <th />
              <th style={{ width: 160 }}>{t("set.warn")}</th>
              <th style={{ width: 160 }}>{t("set.crit")}</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((r) => (
              <tr key={r.label}>
                <td>{t(r.label)}</td>
                <td>
                  <input
                    className="input"
                    type="number"
                    step={r.step}
                    min={0}
                    value={draft.thresholds[r.warn]}
                    onChange={(e) => setTh(r.warn, Number(e.target.value))}
                  />
                </td>
                <td>
                  <input
                    className="input"
                    type="number"
                    step={r.step}
                    min={0}
                    value={draft.thresholds[r.crit]}
                    onChange={(e) => setTh(r.crit, Number(e.target.value))}
                  />
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      <PlatformSettings />

      <h2 className="section">{t("set.appearance")}</h2>
      <div className="card">
        <SettingRow title={t("set.theme")}>
          <Segmented<ThemePref>
            value={theme}
            onChange={(v) => setPrefs({ theme: v })}
            options={[
              { value: "system", label: t("set.themeSystem") },
              { value: "light", label: t("set.themeLight") },
              { value: "dark", label: t("set.themeDark") },
            ]}
          />
        </SettingRow>
        <SettingRow title={t("set.language")} last>
          <Segmented<LangPref>
            value={lang}
            onChange={(v) => setPrefs({ lang: v })}
            options={[
              { value: "auto", label: t("set.langAuto") },
              { value: "en", label: "English" },
              { value: "vi", label: "Tiếng Việt" },
            ]}
          />
        </SettingRow>
      </div>

      <h2 className="section">{t("set.about")}</h2>
      <div className="card card-pad row">
        <div style={{ flex: 1 }}>
          <strong>Termward {version && <span className="muted">{version}</span>}</strong>
          <div className="muted">{t("set.aboutBody")}</div>
        </div>
        <button
          className="btn"
          onClick={() => (window.termward ? window.termward.openExternal(REPO) : window.open(REPO))}
        >
          <ExternalLink size={14} />
          {t("set.github")}
        </button>
      </div>
    </div>
  );
}

/** Settings owned by the shell: tray behavior on desktop, background mode on mobile. */
function PlatformSettings() {
  const t = useT();
  const bridge = window.termward;
  const [closeToTray, setCloseToTray] = useState<boolean | null>(null);
  const [background, setBackground] = useState<boolean | null>(null);

  useEffect(() => {
    void bridge?.getCloseToTray?.().then(setCloseToTray);
    void bridge?.getBackground?.().then(setBackground);
  }, [bridge]);

  if (bridge?.getCloseToTray && closeToTray !== null) {
    return (
      <>
        <h2 className="section">{t("set.desktop")}</h2>
        <div className="card">
          <SettingRow title={t("set.closeToTray")} hint={t("set.closeToTrayHint")} last>
            <Switch
              on={closeToTray}
              onChange={(v) => {
                setCloseToTray(v);
                bridge.setCloseToTray?.(v);
              }}
            />
          </SettingRow>
        </div>
      </>
    );
  }
  if (isMobile()) {
    const ios = platform() === "ios";
    return (
      <>
        <h2 className="section">{t("set.mobile")}</h2>
        <div className="card">
          <SettingRow title={t("set.background")} hint={ios ? t("set.backgroundIos") : t("set.backgroundHint")} last>
            {!ios && background !== null && (
              <Switch
                on={background}
                onChange={async (v) => {
                  setBackground(v);
                  try {
                    await bridge?.setBackground?.(v);
                  } catch (e) {
                    setBackground(!v);
                    toast("error", errorText(e));
                  }
                }}
              />
            )}
          </SettingRow>
        </div>
      </>
    );
  }
  return null;
}

function SettingRow({
  title,
  hint,
  children,
  last,
}: {
  title: string;
  hint?: string;
  children: React.ReactNode;
  last?: boolean;
}) {
  return (
    <div className="row" style={{ padding: "14px 18px", borderBottom: last ? 0 : "1px solid var(--border)", gap: 20 }}>
      <div style={{ flex: 1 }}>
        <div style={{ fontWeight: 540 }}>{title}</div>
        {hint && (
          <div className="muted" style={{ fontSize: 12.5 }}>
            {hint}
          </div>
        )}
      </div>
      {children}
    </div>
  );
}

async function fetchVersion(): Promise<string> {
  try {
    return (await api.info()).version;
  } catch {
    return "";
  }
}
