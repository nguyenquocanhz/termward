import { useEffect, useMemo, useState, type ReactNode } from "react";
import { useShallow } from "zustand/react/shallow";
import {
  ArrowLeft,
  ChevronRight,
  CircleCheck,
  CircuitBoard,
  CircleDashed,
  ClipboardCopy,
  Copy,
  Cpu,
  Download,
  Fan,
  FolderTree,
  HardDrive,
  Info,
  Layers,
  LoaderCircle,
  MemoryStick,
  Network,
  OctagonAlert,
  Plug,
  RefreshCw,
  ScrollText,
  Server,
  Thermometer,
  TriangleAlert,
  Wrench,
} from "lucide-react";
import {
  downloadHardwareReport,
  hardwareReportText,
  type HardwareResult,
  type HwComponent,
  type HwCoverage,
  type HwFinding,
  type Severity,
} from "../lib/api";
import { resolveLang, useT, type TKey } from "../lib/i18n";
import {
  componentState,
  counts,
  elapsed,
  headline,
  headlineLevel,
  lt,
  sectionLabelKey,
  sevRank,
  type ComponentState,
} from "../lib/hardware";
import { ago } from "../lib/format";
import { copyText } from "../lib/util";
import { isMobile } from "../lib/platform";
import { errorText, loadHardware, navigate, startHardwareCheck, toast, useApp, type HwState } from "../store";

type Lang = "en" | "vi";

function useLang(): Lang {
  return resolveLang(useApp((s) => s.lang));
}

function useHw(hostId: string): HwState {
  const hw = useApp((s) => s.hardware[hostId]);
  useEffect(() => {
    if (hw?.result === undefined) void loadHardware(hostId);
  }, [hostId]);
  return hw ?? {};
}

/** Re-renders every second while `on`. */
function useTick(on: boolean): number {
  const [now, setNow] = useState(Date.now());
  useEffect(() => {
    if (!on) return;
    const id = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(id);
  }, [on]);
  return now;
}

const optInTests = /\.(bench|memtest)$/;

const componentIcons: Record<string, typeof Server> = {
  system: Server,
  cpu: Cpu,
  memory: MemoryStick,
  disk: HardDrive,
  raid: Layers,
  filesystem: FolderTree,
  thermal: Thermometer,
  fan: Fan,
  power: Plug,
  network: Network,
  bmc: CircuitBoard,
  logs: ScrollText,
};

function SevIcon({ sev, size = 16 }: { sev: Severity | ComponentState; size?: number }) {
  switch (sev) {
    case "crit":
      return <OctagonAlert size={size} style={{ color: "var(--crit)" }} />;
    case "warn":
      return <TriangleAlert size={size} style={{ color: "var(--warn)" }} />;
    case "info":
      return <Info size={size} style={{ color: "var(--info)" }} />;
    case "partial":
    case "none":
      return <CircleDashed size={size} className="faint" />;
    default:
      return <CircleCheck size={size} style={{ color: "var(--ok)" }} />;
  }
}

function sevBadgeClass(s: Severity | ComponentState): string {
  return s === "partial" || s === "none" ? "" : s;
}

// ---------------------------------------------------------------- progress

function Progress({ run, compact }: { run: NonNullable<HwState["running"]>; compact?: boolean }) {
  const t = useT();
  const now = useTick(true);
  return (
    <div className={`hw-progress${compact ? " compact" : ""}`}>
      <LoaderCircle size={compact ? 18 : 22} className="spin" style={{ color: "var(--accent)" }} />
      <div className="grow">
        <strong>{t("hw.running")}</strong>
        <div className="muted">
          {t(sectionLabelKey(run.section))}
          {run.section && <span className="mono faint hw-section"> · {run.section}</span>}
        </div>
        {!compact && <div className="faint hw-progress-hint">{t("hw.runningHint")}</div>}
      </div>
      <div className="hw-progress-meta">
        <span className="mono">{elapsed(now - run.startedAt)}</span>
        {run.count > 0 && <span className="faint">{t("hw.sections", { n: run.count })}</span>}
      </div>
    </div>
  );
}

// ---------------------------------------------------------------- host card

/** "Hardware" card on the server page: last result and the check button. */
export function HardwareCard({ hostId }: { hostId: string }) {
  const t = useT();
  const lang = useLang();
  const hw = useHw(hostId);
  const res = hw.result;

  let body: ReactNode;
  if (hw.running) {
    body = <Progress run={hw.running} compact />;
  } else if (res) {
    const hl = headline(res.report);
    const lvl = headlineLevel(hl);
    const c = counts(res.report);
    body = (
      <div className="hw-card-row">
        <SevIcon sev={lvl} size={20} />
        <div className="grow">
          <strong>{t(`hw.verdict.${hl}`)}</strong>
          <div className="muted hw-card-meta">
            <span>{t("hw.lastCheck", { ago: ago(res.savedAt) })}</span>
            <span>{t("hw.counts", c)}</span>
            {res.ranAs === "user" && <span style={{ color: "var(--warn)" }}>{t("hw.ranAs.user")}</span>}
          </div>
          {res.parts.length > 0 && (
            <div className="hw-card-parts">
              <Wrench size={13} />
              {t("hw.parts")}: {res.parts.map((p) => lt(p.kind, lang)).join(", ")}
            </div>
          )}
        </div>
      </div>
    );
  } else if (res === null) {
    body = <p className="muted hw-intro">{t("hw.intro")}</p>;
  } else {
    body = <LoaderCircle size={16} className="spin faint" />;
  }

  return (
    <>
      <h2 className="section">{t("hw.title")}</h2>
      <div className="card card-pad hw-card">
        {body}
        <div className="hw-card-actions">
          {(res || hw.running) && (
            <button className="btn sm" onClick={() => navigate({ name: "hardware", hostId })}>
              {t("hw.viewResults")}
              <ChevronRight size={14} />
            </button>
          )}
          <button className="btn sm primary" disabled={!!hw.running} onClick={() => void startHardwareCheck(hostId)}>
            <HardDrive size={14} />
            {res ? t("hw.checkAgain") : t("hw.check")}
          </button>
        </div>
      </div>
    </>
  );
}

// ---------------------------------------------------------------- results view

export function HardwareView({ hostId }: { hostId: string }) {
  const t = useT();
  const lang = useLang();
  const host = useApp(useShallow((s) => s.hosts.find((h) => h.id === hostId)));
  const hw = useHw(hostId);
  const res = hw.result;
  const [busy, setBusy] = useState<"" | "html" | "md">("");

  if (!host) {
    return (
      <div className="page">
        <div className="empty">{t("overview.noMatch")}</div>
      </div>
    );
  }

  const saveHtml = async () => {
    setBusy("html");
    try {
      const file = await downloadHardwareReport(hostId, "html", lang);
      toast("success", t("hw.saved", { file }));
    } catch (e) {
      toast("error", t("err.generic"), errorText(e));
    } finally {
      setBusy("");
    }
  };
  const copyMd = async () => {
    setBusy("md");
    try {
      await copyText(await hardwareReportText(hostId, "md", lang), t("hw.mdCopied"));
    } catch (e) {
      toast("error", t("err.generic"), errorText(e));
    } finally {
      setBusy("");
    }
  };

  const r = res?.report;
  const sub = r
    ? [r.host.hostname, r.host.os, [r.host.vendor, r.host.model].filter(Boolean).join(" ")].filter(Boolean).join(" · ")
    : `${host.user}@${host.address}`;

  return (
    <div className="page">
      <div className="page-head">
        <div style={{ minWidth: 0 }}>
          <button className="btn ghost sm hw-back desktop-only" onClick={() => navigate({ name: "host", hostId })}>
            <ArrowLeft size={14} />
            {t("hw.back", { host: host.name })}
          </button>
          <h1 className="truncate">{t("hw.title")}</h1>
          <p className="truncate selectable">{sub}</p>
        </div>
        <div className="actions">
          {res && !isMobile() && (
            <button className="btn" disabled={!!busy} onClick={saveHtml}>
              <Download size={14} />
              {t("hw.saveHtml")}
            </button>
          )}
          {res && (
            <button className="btn" disabled={!!busy} onClick={copyMd}>
              <ClipboardCopy size={14} />
              {t("hw.copyMd")}
            </button>
          )}
          <button className="btn primary" disabled={!!hw.running} onClick={() => void startHardwareCheck(hostId)}>
            <RefreshCw size={14} className={hw.running ? "spin" : ""} />
            {res ? t("hw.checkAgain") : t("hw.check")}
          </button>
        </div>
      </div>

      {hw.running && (
        <div className="card card-pad" style={{ marginBottom: 16 }}>
          <Progress run={hw.running} />
        </div>
      )}

      {res === undefined && !hw.running && (
        <div className="empty">
          <LoaderCircle size={18} className="spin" />
        </div>
      )}

      {res === null && !hw.running && (
        <div className="card empty">
          <div className="empty-art">
            <HardDrive size={24} />
          </div>
          <h3>{t("hw.empty")}</h3>
          <p>{t("hw.intro")}</p>
          <div className="actions">
            <button className="btn primary" onClick={() => void startHardwareCheck(hostId)}>
              <HardDrive size={14} />
              {t("hw.check")}
            </button>
          </div>
        </div>
      )}

      {res && <Results res={res} lang={lang} />}
    </div>
  );
}

function Results({ res, lang }: { res: HardwareResult; lang: Lang }) {
  const t = useT();
  const r = res.report;
  const hl = headline(r);
  const lvl = headlineLevel(hl);
  const c = counts(r);
  const findings = useMemo(
    () => [...(r.findings ?? [])].sort((a, b) => sevRank[b.severity] - sevRank[a.severity]),
    [r.findings],
  );
  const problems = findings.filter((f) => f.severity !== "ok");
  const passed = findings.filter((f) => f.severity === "ok");
  // The opt-in disk and RAM tests are not offered here, so they are not gaps.
  const gaps = (r.coverage ?? []).filter((g) => g.state !== "ran" && !g.notApplicable && !optInTests.test(g.id));
  const checkedAt = new Date(res.savedAt).toLocaleString(lang, { dateStyle: "medium", timeStyle: "short" });

  return (
    <>
      <div className={`hw-verdict ${lvl}`}>
        <SevIcon sev={lvl} size={26} />
        <div className="grow">
          <h2>{t(`hw.verdict.${hl}`)}</h2>
          <p>{t(`hw.verdictSub.${hl}`)}</p>
          <div className="hw-verdict-meta">
            <span>{t("hw.checkedAt", { date: checkedAt })}</span>
            <span>{t(`hw.ranAs.${res.ranAs}`)}</span>
            <span>{t("hw.counts", c)}</span>
          </div>
        </div>
      </div>

      <div className="stack" style={{ marginTop: 12 }}>
        {res.partial && (
          <div className="callout warn">
            <TriangleAlert size={16} />
            <div className="grow">{t("hw.partial")}</div>
          </div>
        )}
        {res.ranAs === "user" && (
          <div className="callout info">
            <Info size={16} />
            <div className="grow">{t("hw.noRoot")}</div>
          </div>
        )}
      </div>

      <h2 className="section">{t("hw.components")}</h2>
      <div className="hw-tiles">
        {(r.summary ?? []).map((s) => (
          <ComponentTile key={s.component} c={s} lang={lang} />
        ))}
      </div>

      <h2 className="section">
        {t("hw.findings")} {problems.length > 0 && <span className="muted">{problems.length}</span>}
      </h2>
      <div className="card hw-findings">
        {problems.length === 0 && (
          <div className="hw-finding">
            <CircleCheck size={16} style={{ color: "var(--ok)" }} />
            <span className="muted">{t("hw.noFindings")}</span>
          </div>
        )}
        {problems.map((f, i) => (
          <FindingRow key={`${f.id}:${f.target ?? ""}:${i}`} f={f} lang={lang} />
        ))}
        {passed.length > 0 && (
          <details className="hw-passed">
            <summary>
              <ChevronRight size={14} className="hw-chevron" />
              {t("hw.passed", { n: passed.length })}
            </summary>
            {passed.map((f, i) => (
              <FindingRow key={`${f.id}:${f.target ?? ""}:${i}`} f={f} lang={lang} />
            ))}
          </details>
        )}
      </div>

      {res.parts.length > 0 && (
        <>
          <h2 className="section">{t("hw.parts")}</h2>
          <p className="hw-section-hint">{t("hw.partsHint")}</p>
          <div className="card hw-parts">
            {res.parts.map((p, i) => (
              <div className="hw-part" key={i}>
                <SevIcon sev={p.severity} />
                <div className="grow">
                  <strong>
                    {lt(p.kind, lang)}
                    {[p.vendor, p.model, p.size].filter(Boolean).length > 0 && (
                      <span className="hw-part-model"> · {[p.vendor, p.model, p.size].filter(Boolean).join(" ")}</span>
                    )}
                  </strong>
                  <dl className="hw-part-kv">
                    {p.serial && (
                      <>
                        <dt>{t("hw.serial")}</dt>
                        <dd className="mono selectable">{p.serial}</dd>
                      </>
                    )}
                    {p.location && (
                      <>
                        <dt>{t("hw.location")}</dt>
                        <dd className="mono selectable">{p.location}</dd>
                      </>
                    )}
                    {p.firmware && (
                      <>
                        <dt>{t("hw.firmware")}</dt>
                        <dd className="mono selectable">{p.firmware}</dd>
                      </>
                    )}
                  </dl>
                  <div className="muted">{lt(p.why, lang)}</div>
                </div>
              </div>
            ))}
            <div className="hw-parts-foot">
              <button className="btn sm" onClick={() => void copyText(lt(res.rma, lang), t("hw.partsCopied"))}>
                <Copy size={13} />
                {t("hw.copyParts")}
              </button>
            </div>
          </div>
        </>
      )}

      {gaps.length > 0 && (
        <>
          <h2 className="section">{t("hw.coverage")}</h2>
          <p className="hw-section-hint">{t("hw.coverageHint")}</p>
          <div className="card hw-gaps">
            {gaps.map((g) => (
              <GapRow key={g.id} g={g} lang={lang} />
            ))}
          </div>
        </>
      )}

      {(r.notes ?? []).length > 0 && (
        <>
          <h2 className="section">{t("hw.notes")}</h2>
          <div className="card card-pad hw-notes">
            <ul>
              {(r.notes ?? []).map((n, i) => (
                <li key={i}>{lt(n, lang)}</li>
              ))}
            </ul>
          </div>
        </>
      )}

      <p className="faint hw-foot">
        Diagward {r.version}
        {r.seconds > 0 && ` · ${Math.round(r.seconds)} s`}
      </p>
    </>
  );
}

function ComponentTile({ c, lang }: { c: HwComponent; lang: Lang }) {
  const t = useT();
  const state = componentState(c);
  const Icon = componentIcons[c.component] ?? Server;
  const n = [
    c.crit > 0 && `${t("hw.state.crit")}: ${c.crit}`,
    c.warn > 0 && `${t("hw.state.warn")}: ${c.warn}`,
    c.info > 0 && `${t("hw.state.info")}: ${c.info}`,
  ].filter(Boolean);
  return (
    <div className={`card hw-tile ${state}`}>
      <div className="hw-tile-head">
        <Icon size={16} />
        <span>{lt(c.name, lang)}</span>
      </div>
      <span className={`badge ${sevBadgeClass(state)}`}>
        <SevIcon sev={state} size={12} />
        {t(`hw.state.${state}` as TKey)}
      </span>
      {n.length > 0 && <span className="faint hw-tile-counts">{n.join(" · ")}</span>}
    </div>
  );
}

function FindingRow({ f, lang }: { f: HwFinding; lang: Lang }) {
  const t = useT();
  const detail = lt(f.detail, lang);
  const action = lt(f.action, lang);
  return (
    <div className="hw-finding">
      <SevIcon sev={f.severity} />
      <div className="grow">
        <div className="hw-finding-title">
          <strong>{lt(f.title, lang)}</strong>
          {f.target && <span className="mono muted selectable">{f.target}</span>}
        </div>
        {detail && <p className="selectable">{detail}</p>}
        {action && f.severity !== "ok" && (
          <div className="hw-action selectable">
            <Wrench size={13} />
            <div>
              <b>{t("hw.action")}: </b>
              {action}
            </div>
          </div>
        )}
        {f.evidence && f.evidence.length > 0 && (
          <details className="hw-evidence">
            <summary>
              <ChevronRight size={13} className="hw-chevron" />
              {t("hw.evidence", { n: f.evidence.length })}
            </summary>
            <pre className="mono selectable">{f.evidence.join("\n")}</pre>
          </details>
        )}
      </div>
    </div>
  );
}

function GapRow({ g, lang }: { g: HwCoverage; lang: Lang }) {
  const t = useT();
  const reason = lt(g.reason, lang);
  const fix = lt(g.fix, lang);
  return (
    <div className="hw-gap">
      <div className="hw-gap-head">
        <strong>{lt(g.name, lang)}</strong>
        <span className={`badge ${g.state === "failed" ? "warn" : ""}`}>{t(`hw.cov.${g.state}` as TKey)}</span>
      </div>
      {reason && <div className="muted">{reason}</div>}
      {fix && <div>{fix}</div>}
      {g.cmd && (
        <div className="hw-cmd">
          <code className="mono selectable">{g.cmd}</code>
          <button className="icon-btn" title={t("hw.copyCmd")} onClick={() => void copyText(g.cmd!, t("hw.cmdCopied"))}>
            <Copy size={14} />
          </button>
        </div>
      )}
    </div>
  );
}
