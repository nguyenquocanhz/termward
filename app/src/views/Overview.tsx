import { useMemo, useState } from "react";
import { useShallow } from "zustand/react/shallow";
import {
  AlertTriangle,
  ChevronRight,
  FileInput,
  HardDrive,
  KeyRound,
  LoaderCircle,
  Lock,
  Plus,
  RefreshCw,
  Server,
  SquareTerminal,
  X,
} from "lucide-react";
import { api, type HardwareRun, type Host, type Level, type Status, type Thresholds } from "../lib/api";
import { resolveLang, useT, type TKey } from "../lib/i18n";
import { findingText, severity } from "../lib/findings";
import { lt } from "../lib/hardware";
import { ago, duration } from "../lib/format";
import { caps } from "../lib/platform";
import {
  alertBody,
  alertView,
  cancelFleetCheck,
  dismissFleetRun,
  navigate,
  openDialog,
  openTerminal,
  startFleetCheck,
  startHardwareCheck,
  useApp,
} from "../store";
import { LevelBadge, Meter, StatusDot } from "../components/ui";
import { HwBadge } from "../components/HwBadge";

type Filter = "all" | "ok" | "attention" | "down" | "off";

export function Overview() {
  const t = useT();
  const { hosts, statuses, alerts, settings, hwRun, hwRunDismissed } = useApp(
    useShallow((s) => ({
      hosts: s.hosts,
      statuses: s.statuses,
      alerts: s.alerts,
      settings: s.settings,
      hwRun: s.hwRun,
      hwRunDismissed: s.hwRunDismissed,
    })),
  );
  const [filter, setFilter] = useState<Filter>("all");

  const levelOf = (h: Host) => (h.monitor ? (statuses[h.id]?.level ?? "unknown") : "off");
  const counts = useMemo(() => {
    const c = { ok: 0, attention: 0, down: 0, off: 0 };
    for (const h of hosts) {
      const l = levelOf(h);
      if (l === "ok") c.ok++;
      else if (l === "warn" || l === "crit") c.attention++;
      else if (l === "down") c.down++;
      else if (l === "off") c.off++;
    }
    return c;
  }, [hosts, statuses]);

  if (hosts.length === 0) return <EmptyOverview />;

  const monitored = hosts.filter((h) => h.monitor).length;
  const needAttention = counts.attention + counts.down;
  const hour = new Date().getHours();
  const greeting = t(hour < 12 ? "greet.morning" : hour < 18 ? "greet.afternoon" : "greet.evening");
  const subtitle =
    monitored === 0
      ? t("overview.noneMonitored")
      : needAttention
        ? t("overview.needAttention", { n: needAttention, total: monitored })
        : t("overview.allGood", { n: monitored });

  const visible = hosts
    .filter((h) => {
      const l = levelOf(h);
      switch (filter) {
        case "ok":
          return l === "ok";
        case "attention":
          return l === "warn" || l === "crit";
        case "down":
          return l === "down";
        case "off":
          return l === "off";
      }
      return true;
    })
    .sort((a, b) => severity[levelOf(b)] - severity[levelOf(a)] || a.name.localeCompare(b.name));

  const chips: { id: Filter; label: string; n: number; level?: "ok" | "warn" | "down" | "off" }[] = [
    { id: "all", label: t("overview.all"), n: hosts.length },
    { id: "ok", label: t("level.ok"), n: counts.ok, level: "ok" },
    { id: "attention", label: t("host.attention"), n: counts.attention, level: "warn" },
    { id: "down", label: t("level.down"), n: counts.down, level: "down" },
    { id: "off", label: t("level.off"), n: counts.off, level: "off" },
  ];

  return (
    <div className="page">
      <div className="page-head">
        <div>
          <h1>{greeting}</h1>
          <p>{subtitle}</p>
        </div>
        <div className="actions">
          {caps.sshDir && (
            <button className="btn" onClick={() => openDialog({ kind: "importConfig" })}>
              <FileInput size={15} />
              {t("overview.importConfig")}
            </button>
          )}
          {monitored > 0 && (
            <button
              className="btn"
              title={t("fleet.checkAllHint")}
              disabled={hwRun?.active}
              onClick={() => void startFleetCheck()}
            >
              {hwRun?.active ? <LoaderCircle size={15} className="spin" /> : <HardDrive size={15} />}
              {hwRun?.active ? `${hwRun.done}/${hwRun.hosts.length}` : t("fleet.checkAll")}
            </button>
          )}
          <button className="btn primary" onClick={() => openDialog({ kind: "host" })}>
            <Plus size={15} />
            {t("sidebar.newHost")}
          </button>
        </div>
      </div>

      <div className="summary">
        {chips
          .filter((c) => c.id === "all" || c.n > 0)
          .map((c) => (
            <button key={c.id} className={`chip${filter === c.id ? " on" : ""}`} onClick={() => setFilter(c.id)}>
              {c.level && <StatusDot level={c.level} />}
              {c.label}
              <b>{c.n}</b>
            </button>
          ))}
      </div>

      {hwRun && (hwRun.active || hwRunDismissed !== hwRun.id) && <FleetRun run={hwRun} />}

      {visible.length ? (
        <div className="host-grid">
          {visible.map((h) => (
            <HostCard
              key={h.id}
              host={h}
              status={statuses[h.id]}
              level={levelOf(h)}
              thresholds={settings?.thresholds}
            />
          ))}
        </div>
      ) : (
        <div className="card empty" style={{ padding: 36 }}>
          {t("overview.noMatch")}
        </div>
      )}

      <h2 className="section">{t("overview.recentAlerts")}</h2>
      <div className="card alert-list">
        {alerts.length === 0 ? (
          <div className="empty" style={{ padding: 28 }}>
            {t("overview.noAlerts")}
          </div>
        ) : (
          alerts.slice(0, 8).map((a) => (
            <div key={a.id} className="alert" onClick={() => navigate(alertView(a))}>
              <StatusDot level={a.to} />
              <div style={{ flex: 1, minWidth: 0 }}>
                <div className="row">
                  <strong>{a.hostName}</strong>
                  <LevelBadge level={a.to} />
                  {a.kind === "hardware" && (
                    <span className="badge">
                      <HardDrive size={11} />
                      {t("alert.hardware")}
                    </span>
                  )}
                </div>
                <div className="muted truncate" style={{ fontSize: 12.5 }}>
                  {alertBody(a) || " "}
                </div>
              </div>
              <span className="faint" style={{ fontSize: 12 }}>
                {ago(a.at)}
              </span>
            </div>
          ))
        )}
      </div>
    </div>
  );
}

function HostCard({
  host,
  status,
  level,
  thresholds: th,
}: {
  host: Host;
  status?: Status;
  level: Level | "off";
  thresholds?: Thresholds;
}) {
  const t = useT();
  const s = status?.sample;
  const top = status?.findings.find((f) => f.level !== "info");
  const disk = s?.disks.reduce((m, d) => Math.max(m, d.percent), 0) ?? -1;

  return (
    <div className={`host-card ${level}`} onClick={() => navigate({ name: "host", hostId: host.id })}>
      <div className="head">
        <div style={{ paddingTop: 5 }}>
          <StatusDot level={level} checking={status?.checking} />
        </div>
        <div style={{ flex: 1, minWidth: 0 }}>
          <div className="title truncate">{host.name}</div>
          <div className="sub truncate">
            {host.user}@{host.address}
            {host.group && ` · ${host.group}`}
          </div>
        </div>
        <div className="quick" onClick={(e) => e.stopPropagation()}>
          <button className="icon-btn" title={t("card.checkNow")} onClick={() => void api.check(host.id)}>
            <RefreshCw size={14} className={status?.checking ? "spin" : ""} />
          </button>
          <button className="icon-btn" title={t("card.openTerminal")} onClick={() => void openTerminal(host.id)}>
            <SquareTerminal size={15} />
          </button>
        </div>
      </div>

      {s && th ? (
        <div className="metrics">
          <Mini label={t("m.cpu")} value={s.cpuPercent} warn={th.cpuWarn} crit={th.cpuCrit} />
          <Mini label={t("m.mem")} value={s.memPercent} warn={th.memWarn} crit={th.memCrit} />
          <Mini label={t("m.disk")} value={disk} warn={th.diskWarn} crit={th.diskCrit} />
        </div>
      ) : (
        <div
          className={`muted${status?.error ? " mono" : ""}`}
          style={{
            fontSize: 12,
            minHeight: 32,
            overflow: "hidden",
            display: "-webkit-box",
            WebkitLineClamp: 2,
            WebkitBoxOrient: "vertical",
          }}
        >
          {!host.monitor
            ? t("host.notMonitored")
            : status?.error
              ? shortError(status.error)
              : status?.errorKind
                ? t(`ek.${status.errorKind}` as TKey)
                : t("level.checking")}
        </div>
      )}

      <div className="foot">
        {top ? (
          <span className={`issue ${top.level} truncate`}>
            <AlertTriangle size={13} />
            <span className="truncate">{findingText(top)}</span>
          </span>
        ) : s ? (
          <span className="truncate">
            {t("m.uptime")} {duration(s.uptimeSec)} · {t("m.load")} {s.load[0].toFixed(2)}
          </span>
        ) : (
          <span />
        )}
        <span className="spacer" />
        <HwBadge hostId={host.id} />
        {status?.checkedAt && status.checkedAt !== "0001-01-01T00:00:00Z" && (
          <span className="faint">{ago(status.checkedAt)}</span>
        )}
      </div>
    </div>
  );
}

/** Progress, then the outcome, of "check hardware on all servers". */
function FleetRun({ run }: { run: HardwareRun }) {
  const t = useT();
  const lang = resolveLang(useApp((s) => s.lang));
  const { hosts, fleet } = useApp(useShallow((s) => ({ hosts: s.hosts, fleet: s.hwFleet })));
  const name = (id: string) => hosts.find((h) => h.id === id)?.name ?? id;
  const total = run.hosts.length;

  if (run.active) {
    const now = run.hosts.filter((id) => fleet[id]?.state === "running").map(name);
    return (
      <div className="card fleet-run">
        <div className="fleet-run-head">
          <LoaderCircle size={18} className="spin" style={{ color: "var(--accent)" }} />
          <div className="grow">
            <strong>{t("fleet.running")}</strong>
            <div className="muted">
              {t("fleet.progress", { done: run.done, total })}
              {now.length > 0 && ` · ${t("fleet.now", { hosts: now.join(", ") })}`}
            </div>
          </div>
          <button className="btn sm" onClick={() => void cancelFleetCheck()}>
            {t("fleet.stop")}
          </button>
        </div>
        <Meter value={total ? (run.done / total) * 100 : 0} warn={101} crit={101} />
      </div>
    );
  }

  const problems = run.ok
    .map((id) => ({ id, sum: fleet[id]?.summary }))
    .filter((x) => x.sum && (x.sum.headline === "crit" || x.sum.headline === "warn"))
    .sort((a, b) => (a.sum!.headline === b.sum!.headline ? 0 : a.sum!.headline === "crit" ? -1 : 1));
  const failed = Object.entries(run.failed);
  return (
    <div className="card fleet-run">
      <div className="fleet-run-head">
        <HardDrive size={18} style={{ color: problems.length ? "var(--warn)" : "var(--ok)" }} />
        <div className="grow">
          <strong>{t("fleet.doneTitle", { n: run.ok.length, total })}</strong>
          {run.finishedAt && <div className="muted">{ago(run.finishedAt)}</div>}
        </div>
        <button className="icon-btn" aria-label={t("common.close")} onClick={dismissFleetRun}>
          <X size={15} />
        </button>
      </div>

      {problems.length > 0 ? (
        <div className="fleet-group">
          <div className="fleet-group-title">{t("fleet.problems")}</div>
          {problems.map(({ id, sum }) => (
            <button key={id} className="fleet-row" onClick={() => navigate({ name: "hardware", hostId: id })}>
              <StatusDot level={sum!.headline as "crit" | "warn"} />
              <strong className="truncate">{name(id)}</strong>
              <span className="muted truncate grow">
                {sum!.top ? lt(sum!.top, lang) : t(`hw.verdict.${sum!.headline}`)}
              </span>
              <ChevronRight size={14} className="faint" />
            </button>
          ))}
        </div>
      ) : (
        run.ok.length > 0 && <p className="muted fleet-note">{t("fleet.allHealthy")}</p>
      )}

      {run.needsSudo.length > 0 && (
        <div className="fleet-group">
          <div className="fleet-group-title">
            <Lock size={12} />
            {t("fleet.needsSudo")}
          </div>
          <p className="muted fleet-note">{t("fleet.needsSudoHint")}</p>
          {run.needsSudo.map((id) => (
            <div key={id} className="fleet-row static">
              <StatusDot level="unknown" />
              <strong className="truncate grow">{name(id)}</strong>
              <button
                className="btn sm"
                onClick={() => {
                  navigate({ name: "hardware", hostId: id });
                  void startHardwareCheck(id);
                }}
              >
                {t("fleet.checkByHand")}
              </button>
            </div>
          ))}
        </div>
      )}

      {failed.length > 0 && (
        <div className="fleet-group">
          <div className="fleet-group-title">{t("fleet.couldNot")}</div>
          {failed.map(([id, code]) => (
            <button key={id} className="fleet-row" onClick={() => navigate({ name: "host", hostId: id })}>
              <StatusDot level="down" />
              <strong className="truncate">{name(id)}</strong>
              <span className="muted truncate grow">{t(`hwerr.${code}` as TKey)}</span>
              <ChevronRight size={14} className="faint" />
            </button>
          ))}
        </div>
      )}

      {run.skipped.length > 0 && (
        <p className="faint fleet-note">
          {t("fleet.skipped")}: {run.skipped.map(name).join(", ")}
        </p>
      )}
    </div>
  );
}

/** "ssh: handshake failed: dial tcp 1.2.3.4:22: i/o timeout" → "dial tcp 1.2.3.4:22: i/o timeout" */
function shortError(msg: string): string {
  return msg.replace(/^(ssh: )?handshake failed: /, "").replace(/^context deadline exceeded$/, "timeout");
}

function Mini({ label, value, warn, crit }: { label: string; value: number; warn: number; crit: number }) {
  return (
    <div className="metric-mini">
      <div className="label">
        <span>{label}</span>
        <b>{value >= 0 ? `${Math.round(value)}%` : "—"}</b>
      </div>
      <Meter value={Math.max(value, 0)} warn={warn} crit={crit} />
    </div>
  );
}

function EmptyOverview() {
  const t = useT();
  return (
    <div className="page">
      <div className="empty" style={{ paddingTop: "12vh" }}>
        <div className="empty-art">
          <Server size={26} />
        </div>
        <h3>{t("empty.title")}</h3>
        <p>{t("empty.body")}</p>
        <div className="actions">
          <button className="btn primary" onClick={() => openDialog({ kind: "host" })}>
            <Plus size={15} />
            {t("form.add")}
          </button>
          {caps.sshDir && (
            <button className="btn" onClick={() => openDialog({ kind: "importConfig" })}>
              <FileInput size={15} />
              {t("overview.importConfig")}
            </button>
          )}
          <button className="btn" onClick={() => openDialog({ kind: "generateKey" })}>
            <KeyRound size={15} />
            {t("empty.generateKey")}
          </button>
        </div>
      </div>
    </div>
  );
}
