import { useShallow } from "zustand/react/shallow";
import {
  AlertTriangle,
  BellOff,
  Info,
  Pencil,
  PlugZap,
  Power,
  RefreshCw,
  RotateCw,
  SquareTerminal,
  Trash2,
  Unplug,
  WifiOff,
} from "lucide-react";
import { api } from "../lib/api";
import { useT, type TKey } from "../lib/i18n";
import { findingText } from "../lib/findings";
import { stripHost } from "../lib/util";
import { ago, bytesKb, clock, duration, pct } from "../lib/format";
import {
  confirmAction,
  ensureConnected,
  errorText,
  navigate,
  openDialog,
  openTerminal,
  refreshHosts,
  toast,
  useApp,
} from "../store";
import { LevelBadge, Menu, Meter, Sparkline, StatusDot } from "../components/ui";

export function HostDetail({ hostId }: { hostId: string }) {
  const t = useT();
  const { host, status, connected, settings, keys, hosts } = useApp(
    useShallow((s) => ({
      host: s.hosts.find((h) => h.id === hostId),
      status: s.statuses[hostId],
      connected: !!s.connected[hostId],
      settings: s.settings,
      keys: s.keys,
      hosts: s.hosts,
    })),
  );
  if (!host) {
    return (
      <div className="page">
        <div className="empty">{t("overview.noMatch")}</div>
      </div>
    );
  }

  const s = status?.sample;
  const th = settings?.thresholds;
  const level = host.monitor ? (status?.level ?? "unknown") : "off";
  const findings = (status?.findings ?? []).filter((f) => f.code !== "unreachable");
  const history = status?.history ?? [];
  const key = keys.find((k) => k.id === host.keyId);
  const jump = hosts.find((h) => h.id === host.jumpHostId);

  const connectAndCheck = async () => {
    if (await ensureConnected(host.id)) await api.check(host.id);
  };

  const remove = () =>
    confirmAction({
      title: t("host.confirmDelete", { name: host.name }),
      body: t("host.confirmDeleteBody"),
      confirm: t("common.delete"),
      danger: true,
      run: async () => {
        await api.deleteHost(host.id);
        await refreshHosts();
        navigate({ name: "overview" });
      },
    });

  const enableMonitoring = async () => {
    try {
      await api.updateHost(host.id, { ...stripHost(host), monitor: true });
      await refreshHosts();
    } catch (e) {
      toast("error", t("err.generic"), errorText(e));
    }
  };

  return (
    <div className="page">
      <div className="page-head">
        <div style={{ minWidth: 0 }}>
          <div className="row" style={{ gap: 12 }}>
            <h1 className="truncate">{host.name}</h1>
            <LevelBadge level={level} pending={status?.pending} checking={status?.checking} />
          </div>
          <p className="truncate">
            <span className="mono selectable">
              {host.user}@{host.address}
              {host.port !== 22 && `:${host.port}`}
            </span>
            {host.group && ` · ${host.group}`}
            {s?.os && ` · ${s.os}`}
          </p>
        </div>
        <div className="actions">
          {connected && (
            <button
              className="icon-btn"
              title={t("host.disconnect")}
              onClick={async () => {
                await api.disconnect(host.id);
                useApp.setState((st) => ({ connected: { ...st.connected, [host.id]: false } }));
              }}
            >
              <Unplug size={16} />
            </button>
          )}
          <button className="icon-btn" title={t("host.delete")} onClick={remove}>
            <Trash2 size={16} />
          </button>
          <Menu
            label={t("power.menu")}
            icon={<Power size={16} />}
            items={[
              {
                label: t("power.reboot"),
                icon: <RotateCw size={14} />,
                run: () => openDialog({ kind: "power", hostId: host.id, action: "reboot" }),
              },
              {
                label: t("power.poweroff"),
                icon: <Power size={14} />,
                danger: true,
                run: () => openDialog({ kind: "power", hostId: host.id, action: "poweroff" }),
              },
            ]}
          />
          <button className="btn" onClick={() => openDialog({ kind: "host", host })}>
            <Pencil size={14} />
            {t("host.edit")}
          </button>
          {host.monitor && (
            <button className="btn" onClick={() => void api.check(host.id)} disabled={status?.checking}>
              <RefreshCw size={14} className={status?.checking ? "spin" : ""} />
              {t("card.checkNow")}
            </button>
          )}
          <button className="btn primary" onClick={() => void openTerminal(host.id)}>
            <SquareTerminal size={15} />
            {t("card.openTerminal")}
          </button>
        </div>
      </div>

      <div className="stack">
        {status?.mutedUntil && Date.parse(status.mutedUntil) > Date.now() && (
          <div className="callout info">
            <BellOff size={16} />
            <div className="grow">{t("power.muted", { time: clock(status.mutedUntil) })}</div>
          </div>
        )}
        {!host.monitor && (
          <div className="callout info">
            <Info size={16} />
            <div className="grow">{t("host.notMonitored")}</div>
            <button className="btn sm" onClick={enableMonitoring}>
              {t("host.enableMonitor")}
            </button>
          </div>
        )}
        {status?.errorKind && status.errorKind !== "network" && (
          <div className="callout warn">
            <PlugZap size={16} />
            <div className="grow">
              <strong>{t(`ek.${status.errorKind}` as TKey)}</strong>
              {status.error && <div className="muted selectable">{status.error}</div>}
            </div>
            <button className="btn sm" onClick={connectAndCheck}>
              {t("ek.action")}
            </button>
          </div>
        )}
        {level === "down" && (
          <div className="callout crit">
            <WifiOff size={16} />
            <div className="grow">
              <strong>{t("f.unreachable")}</strong>
              {status?.error && <div className="muted selectable">{status.error}</div>}
            </div>
            <button className="btn sm" onClick={() => void api.check(host.id)}>
              {t("common.retry")}
            </button>
          </div>
        )}

        {findings.length > 0 && (
          <div className="card findings">
            {findings.map((f, i) => (
              <div className="finding" key={i}>
                {f.level === "info" ? (
                  <Info size={15} className="muted" />
                ) : (
                  <AlertTriangle size={15} style={{ color: `var(--${f.level})` }} />
                )}
                <span style={{ flex: 1 }}>{findingText(f)}</span>
                <LevelBadge level={f.level} />
              </div>
            ))}
          </div>
        )}
      </div>

      {s && th ? (
        <>
          <div className="stat-grid" style={{ marginTop: 16 }}>
            <div className="card stat">
              <span className="k">{t("m.cpu")}</span>
              <span className="v">
                {s.cpuPercent >= 0 ? Math.round(s.cpuPercent) : "—"}
                <small>%</small>
              </span>
              <span className="s">{t("m.cores", { n: s.cpus })}</span>
              <Sparkline values={history.map((p) => p.cpu)} color={colorFor(s.cpuPercent, th.cpuWarn, th.cpuCrit)} />
            </div>
            <div className="card stat">
              <span className="k">{t("m.mem")}</span>
              <span className="v">
                {s.memPercent >= 0 ? Math.round(s.memPercent) : "—"}
                <small>%</small>
              </span>
              <span className="s">
                {t("m.of", { used: bytesKb(s.memUsedKb), total: bytesKb(s.memTotalKb) })}
                {s.swapTotalKb > 0 && ` · ${t("m.swap", { v: pct((s.swapUsedKb / s.swapTotalKb) * 100) })}`}
              </span>
              <Sparkline values={history.map((p) => p.mem)} color={colorFor(s.memPercent, th.memWarn, th.memCrit)} />
            </div>
            <div className="card stat">
              <span className="k">{t("m.load")}</span>
              <span className="v">{s.load[0].toFixed(2)}</span>
              <span className="s">
                5m {s.load[1].toFixed(2)} · 15m {s.load[2].toFixed(2)} ·{" "}
                {t("m.perCore", { v: s.cpus ? (s.load[1] / s.cpus).toFixed(2) : "—" })}
              </span>
              <Sparkline values={history.map((p) => p.load)} max={Math.max(s.cpus, 1)} color="var(--info)" />
            </div>
            <div className="card stat">
              <span className="k">{t("m.uptime")}</span>
              <span className="v" style={{ fontSize: 24 }}>
                {duration(s.uptimeSec)}
              </span>
              <span className="s">
                {t("m.latency")} {s.latencyMs} ms
              </span>
              <span className="s" style={{ marginTop: "auto" }}>
                {t("card.checked", { ago: ago(status?.checkedAt) })}
              </span>
            </div>
          </div>

          {s.disks.length > 0 && (
            <>
              <h2 className="section">{t("host.disks")}</h2>
              <div className="card">
                {s.disks.map((d) => (
                  <div className="disk-row" key={d.mount}>
                    <span className="mono truncate">{d.mount}</span>
                    <Meter value={d.percent} warn={th.diskWarn} crit={th.diskCrit} />
                    <span
                      className="muted"
                      style={{ fontVariantNumeric: "tabular-nums", minWidth: 150, textAlign: "right" }}
                    >
                      {t("m.of", { used: bytesKb(d.usedKb), total: bytesKb(d.totalKb) })} · <b>{pct(d.percent)}</b>
                    </span>
                  </div>
                ))}
              </div>
            </>
          )}

          {s.docker === "denied" && (
            <div className="callout" style={{ marginTop: 16 }}>
              <Info size={16} />
              <div className="grow">{t("host.dockerDenied")}</div>
            </div>
          )}
          {s.containers.length > 0 && (
            <>
              <h2 className="section">
                {t("host.containers")} <span className="muted">{s.containers.length}</span>
              </h2>
              <div className="card">
                <table className="table">
                  <thead>
                    <tr>
                      <th>{t("host.name")}</th>
                      <th>{t("host.state")}</th>
                      <th>{t("host.health")}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {s.containers.map((c) => (
                      <tr key={c.name}>
                        <td className="mono">{c.name}</td>
                        <td>
                          <span className="row">
                            <StatusDot
                              level={c.state === "running" ? "ok" : c.state === "restarting" ? "crit" : "off"}
                            />
                            <span className="muted">{c.status}</span>
                          </span>
                        </td>
                        <td>
                          {c.health ? (
                            <span
                              className={`badge ${c.health === "healthy" ? "ok" : c.health === "unhealthy" ? "crit" : "warn"}`}
                            >
                              {c.health}
                            </span>
                          ) : (
                            <span className="faint">—</span>
                          )}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </>
          )}

          {s.failedUnits.length > 0 && (
            <>
              <h2 className="section">{t("host.failedUnits")}</h2>
              <div className="card">
                {s.failedUnits.map((u) => (
                  <div className="finding" key={u}>
                    <StatusDot level="crit" />
                    <span className="mono selectable">{u}</span>
                  </div>
                ))}
              </div>
            </>
          )}
        </>
      ) : (
        host.monitor &&
        !status?.errorKind && (
          <div className="card empty" style={{ marginTop: 16, padding: 36 }}>
            <RefreshCw size={18} className="spin" />
            <div style={{ marginTop: 8 }}>{t("host.noData")}</div>
          </div>
        )
      )}

      <h2 className="section">{t("host.system")}</h2>
      <div className="card card-pad">
        <dl className="kv">
          {s && (
            <>
              <dt>{t("host.os")}</dt>
              <dd className="selectable">{s.os || "—"}</dd>
              <dt>{t("host.kernel")}</dt>
              <dd className="mono selectable">{s.kernel || "—"}</dd>
              <dt>{t("host.hostname")}</dt>
              <dd className="mono selectable">{s.hostname || "—"}</dd>
            </>
          )}
          <dt>{t("host.address")}</dt>
          <dd className="mono selectable">
            {host.address}:{host.port}
          </dd>
          <dt>{t("host.auth")}</dt>
          <dd>
            {t(`auth.${host.auth}` as TKey)}
            {key && <span className="muted"> · {key.name}</span>}
          </dd>
          {jump && (
            <>
              <dt>{t("host.jump")}</dt>
              <dd>
                <button
                  className="btn ghost sm"
                  style={{ padding: 0, height: "auto" }}
                  onClick={() => navigate({ name: "host", hostId: jump.id })}
                >
                  {jump.name}
                </button>
              </dd>
            </>
          )}
          {host.tags.length > 0 && (
            <>
              <dt>{t("form.tags")}</dt>
              <dd className="row" style={{ flexWrap: "wrap", gap: 6 }}>
                {host.tags.map((tag) => (
                  <span key={tag} className="badge">
                    {tag}
                  </span>
                ))}
              </dd>
            </>
          )}
          {host.notes && (
            <>
              <dt>{t("form.notes")}</dt>
              <dd className="selectable" style={{ whiteSpace: "pre-wrap" }}>
                {host.notes}
              </dd>
            </>
          )}
        </dl>
      </div>
    </div>
  );
}

function colorFor(v: number, warn: number, crit: number): string {
  if (v >= crit) return "var(--crit)";
  if (v >= warn) return "var(--warn)";
  return "var(--accent)";
}
