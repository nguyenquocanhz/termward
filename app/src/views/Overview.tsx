import { useMemo, useState } from "react";
import { useShallow } from "zustand/react/shallow";
import { AlertTriangle, FileInput, KeyRound, Plus, RefreshCw, Server, SquareTerminal } from "lucide-react";
import { api, type Host, type Level, type Status, type Thresholds } from "../lib/api";
import { useT, type TKey } from "../lib/i18n";
import { findingText, severity } from "../lib/findings";
import { ago, duration } from "../lib/format";
import { caps } from "../lib/platform";
import { alertBody, navigate, openDialog, openTerminal, useApp } from "../store";
import { LevelBadge, Meter, StatusDot } from "../components/ui";

type Filter = "all" | "ok" | "attention" | "down" | "off";

export function Overview() {
  const t = useT();
  const { hosts, statuses, alerts, settings } = useApp(
    useShallow((s) => ({ hosts: s.hosts, statuses: s.statuses, alerts: s.alerts, settings: s.settings })),
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
            <div key={a.id} className="alert" onClick={() => navigate({ name: "host", hostId: a.hostId })}>
              <StatusDot level={a.to} />
              <div style={{ flex: 1, minWidth: 0 }}>
                <div className="row">
                  <strong>{a.hostName}</strong>
                  <LevelBadge level={a.to} />
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
        {status?.checkedAt && status.checkedAt !== "0001-01-01T00:00:00Z" && (
          <span className="faint">{ago(status.checkedAt)}</span>
        )}
      </div>
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
