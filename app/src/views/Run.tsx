import { useMemo, useState } from "react";
import { useShallow } from "zustand/react/shallow";
import {
  Bookmark,
  Check,
  ChevronDown,
  ChevronRight,
  CircleX,
  LoaderCircle,
  Play,
  Square,
  Trash2,
  TriangleAlert,
} from "lucide-react";
import { api, type ExecEvent, type Host } from "../lib/api";
import { useT } from "../lib/i18n";
import { errorText, refreshSnippets, runExec, toast, useApp } from "../store";
import { StatusDot, modKey } from "../components/ui";

export function Run() {
  const t = useT();
  const { hosts, statuses, snippets, jobs, lastJob } = useApp(
    useShallow((s) => ({
      hosts: s.hosts,
      statuses: s.statuses,
      snippets: s.snippets,
      jobs: s.jobs,
      lastJob: s.lastJob,
    })),
  );
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [command, setCommand] = useState("");
  const [timeoutSec, setTimeoutSec] = useState(60);
  const [naming, setNaming] = useState<string | null>(null);

  const job = lastJob ? jobs[lastJob] : null;
  const running = !!job && !job.done;

  const groups = useMemo(() => {
    const map = new Map<string, Host[]>();
    for (const h of [...hosts].sort((a, b) => a.name.localeCompare(b.name))) {
      map.set(h.group, [...(map.get(h.group) ?? []), h]);
    }
    return [...map.entries()].sort(([a], [b]) => (a === "" ? 1 : b === "" ? -1 : a.localeCompare(b)));
  }, [hosts]);

  const toggle = (ids: string[], on: boolean) =>
    setSelected((prev) => {
      const next = new Set(prev);
      ids.forEach((id) => (on ? next.add(id) : next.delete(id)));
      return next;
    });

  const run = () => {
    if (!command.trim()) return;
    if (selected.size === 0) {
      toast("info", t("run.pickHint"));
      return;
    }
    void runExec([...selected], command, timeoutSec);
  };

  const saveSnippet = async () => {
    if (!naming?.trim()) return;
    try {
      await api.saveSnippet({ name: naming.trim(), command });
      await refreshSnippets();
      setNaming(null);
    } catch (e) {
      toast("error", errorText(e));
    }
  };

  return (
    <div className="page">
      <div className="page-head">
        <div>
          <h1>{t("nav.run")}</h1>
          <p>{t("run.subtitle")}</p>
        </div>
      </div>

      <div className="run-layout">
        <div className="card">
          <div className="row" style={{ padding: "12px 14px 6px" }}>
            <strong>{t("run.servers")}</strong>
            <span className="muted">{selected.size}</span>
            <span className="spacer" />
            <button className="btn ghost sm" onClick={() => setSelected(new Set(hosts.map((h) => h.id)))}>
              {t("run.selectAll")}
            </button>
            <button className="btn ghost sm" onClick={() => setSelected(new Set())}>
              {t("run.selectNone")}
            </button>
          </div>
          <div className="pick-list">
            {groups.map(([group, list]) => {
              const ids = list.map((h) => h.id);
              const all = ids.every((id) => selected.has(id));
              return (
                <div key={group || "_"}>
                  {(groups.length > 1 || group) && (
                    <label className="pick" style={{ fontSize: 12, color: "var(--muted)", fontWeight: 600 }}>
                      <input type="checkbox" checked={all} onChange={(e) => toggle(ids, e.target.checked)} />
                      {group || t("sidebar.ungrouped")}
                    </label>
                  )}
                  {list.map((h) => (
                    <label key={h.id} className="pick" style={{ paddingLeft: groups.length > 1 || group ? 26 : 8 }}>
                      <input
                        type="checkbox"
                        checked={selected.has(h.id)}
                        onChange={(e) => toggle([h.id], e.target.checked)}
                      />
                      <StatusDot level={h.monitor ? (statuses[h.id]?.level ?? "unknown") : "off"} />
                      <span className="truncate">{h.name}</span>
                    </label>
                  ))}
                </div>
              );
            })}
          </div>
        </div>

        <div className="stack">
          <div className="card card-pad stack">
            <textarea
              className="textarea mono"
              rows={4}
              value={command}
              placeholder={t("run.commandPh")}
              onChange={(e) => setCommand(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter" && (e.ctrlKey || e.metaKey)) {
                  e.preventDefault();
                  run();
                }
              }}
              spellCheck={false}
            />
            <div className="row" style={{ flexWrap: "wrap" }}>
              <label className="row muted" style={{ fontSize: 12.5 }}>
                {t("run.timeout")}
                <select
                  className="select"
                  style={{ width: 110, height: 30 }}
                  value={timeoutSec}
                  onChange={(e) => setTimeoutSec(Number(e.target.value))}
                >
                  {[15, 60, 300, 900, 3600].map((s) => (
                    <option key={s} value={s}>
                      {s < 60 ? t("set.seconds", { n: s }) : t("set.minutes", { n: s / 60 })}
                    </option>
                  ))}
                </select>
              </label>
              {naming === null ? (
                <button className="btn ghost sm" disabled={!command.trim()} onClick={() => setNaming("")}>
                  <Bookmark size={13} />
                  {t("run.saveSnippet")}
                </button>
              ) : (
                <span className="row">
                  <input
                    className="input"
                    style={{ height: 30, width: 200 }}
                    autoFocus
                    placeholder={t("run.snippetName")}
                    value={naming}
                    onChange={(e) => setNaming(e.target.value)}
                    onKeyDown={(e) => {
                      if (e.key === "Enter") void saveSnippet();
                      if (e.key === "Escape") setNaming(null);
                    }}
                  />
                  <button className="btn sm" onClick={saveSnippet}>
                    {t("form.save")}
                  </button>
                </span>
              )}
              <span className="spacer" />
              <span className="faint" style={{ fontSize: 12 }}>
                {modKey}+Enter
              </span>
              {running ? (
                <button className="btn" onClick={() => void api.cancelExec(job.id)}>
                  <Square size={13} />
                  {t("run.cancel")}
                </button>
              ) : (
                <button className="btn primary" disabled={!command.trim() || selected.size === 0} onClick={run}>
                  <Play size={14} />
                  {t("run.run", { n: selected.size })}
                </button>
              )}
            </div>
          </div>

          {snippets.length > 0 && (
            <div className="card">
              <div style={{ padding: "12px 14px 4px" }}>
                <strong>{t("run.snippets")}</strong>
              </div>
              <div style={{ padding: 6 }}>
                {snippets.map((s) => (
                  <div key={s.id} className="pick" onClick={() => setCommand(s.command)}>
                    <Bookmark size={13} className="muted" />
                    <span style={{ fontWeight: 540 }}>{s.name}</span>
                    <span className="mono muted truncate" style={{ flex: 1 }}>
                      {s.command}
                    </span>
                    <button
                      className="icon-btn"
                      onClick={async (e) => {
                        e.stopPropagation();
                        await api.deleteSnippet(s.id);
                        await refreshSnippets();
                      }}
                    >
                      <Trash2 size={13} />
                    </button>
                  </div>
                ))}
              </div>
            </div>
          )}

          {job && <Results jobHostIds={job.hostIds} results={job.results} done={job.done} hosts={hosts} />}
        </div>
      </div>
    </div>
  );
}

function Results({
  jobHostIds,
  results,
  done,
  hosts,
}: {
  jobHostIds: string[];
  results: Record<string, ExecEvent>;
  done: boolean;
  hosts: Host[];
}) {
  const t = useT();
  const [open, setOpen] = useState<Record<string, boolean>>({});
  const evs = jobHostIds.map((id) => ({ id, ev: results[id], host: hosts.find((h) => h.id === id) }));
  const ok = evs.filter((x) => x.ev?.state === "done" && x.ev.result?.exitCode === 0).length;
  const fail = evs.filter(
    (x) => x.ev?.state === "error" || (x.ev?.state === "done" && x.ev.result?.exitCode !== 0),
  ).length;
  const expandDefault = jobHostIds.length <= 6;

  return (
    <div className="card">
      <div className="row" style={{ padding: "12px 14px" }}>
        <strong>{t("run.results")}</strong>
        <span className="spacer" />
        {!done && <LoaderCircle size={14} className="spin muted" />}
        <span className="muted" style={{ fontSize: 12.5 }}>
          {t("run.summary", { ok, fail })}
        </span>
      </div>
      {evs.map(({ id, ev, host }) => {
        const failed = ev?.state === "error" || (ev?.state === "done" && ev.result?.exitCode !== 0);
        const isOpen = open[id] ?? (expandDefault || failed);
        const r = ev?.result;
        return (
          <div className="result" key={id}>
            <div className="result-head" onClick={() => setOpen((o) => ({ ...o, [id]: !isOpen }))}>
              {isOpen ? <ChevronDown size={14} className="muted" /> : <ChevronRight size={14} className="muted" />}
              {!ev || ev.state === "running" ? (
                <LoaderCircle size={15} className="spin muted" />
              ) : ev.state === "error" ? (
                <CircleX size={15} style={{ color: "var(--crit)" }} />
              ) : r?.exitCode === 0 ? (
                <Check size={15} style={{ color: "var(--ok)" }} />
              ) : (
                <TriangleAlert size={15} style={{ color: "var(--warn)" }} />
              )}
              <strong className="truncate">{host?.name ?? id}</strong>
              <span className="spacer" />
              {r && (
                <>
                  <span className={`badge ${r.exitCode === 0 ? "ok" : "warn"}`}>
                    {t("run.exit", { code: r.exitCode })}
                  </span>
                  <span className="faint" style={{ fontSize: 12, minWidth: 54, textAlign: "right" }}>
                    {(r.durationMs / 1000).toFixed(2)}s
                  </span>
                </>
              )}
            </div>
            {isOpen && ev && ev.state !== "running" && (
              <pre>
                {ev.error ? (
                  <span className="err">{ev.error}</span>
                ) : (
                  <>
                    {r?.stdout}
                    {r?.stderr && <span className="err">{r.stderr}</span>}
                    {!r?.stdout && !r?.stderr && <span className="faint">{t("run.noOutput")}</span>}
                    {r?.truncated && <span className="faint">{"\n" + t("run.truncated")}</span>}
                  </>
                )}
              </pre>
            )}
          </div>
        );
      })}
    </div>
  );
}
