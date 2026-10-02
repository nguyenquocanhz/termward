import { Clock, HardDrive, LoaderCircle } from "lucide-react";
import { resolveLang, useT, type TKey } from "../lib/i18n";
import { lt } from "../lib/hardware";
import { ago, relTime } from "../lib/format";
import { navigate, useApp } from "../store";

type Tone = "crit" | "warn" | "ok" | "info" | "idle";

/**
 * A server's hardware verdict as a small badge with a tooltip (verdict, when
 * it was checked, the worst problem, the schedule). Clicking opens the
 * hardware report. `compact` (sidebar) shows only problems and running checks.
 */
export function HwBadge({ hostId, compact }: { hostId: string; compact?: boolean }) {
  const t = useT();
  const lang = resolveLang(useApp((s) => s.lang));
  const fleet = useApp((s) => s.hwFleet[hostId]);
  const running = useApp((s) => !!s.hardware[hostId]?.running);
  const sum = fleet?.summary;
  const busy = running || fleet?.state === "running" || fleet?.state === "manual";
  const queued = !busy && fleet?.state === "queued";

  let tone: Tone = "idle";
  if (sum) tone = sum.headline === "guest" || sum.headline === "none" ? "info" : sum.headline;
  if (compact && !busy && tone !== "crit" && tone !== "warn") return null;
  if (!sum && !busy && !queued && !fleet?.error && !fleet?.nextRun) return null;

  const lines: string[] = [];
  if (busy) lines.push(t("hw.running"));
  else if (queued) lines.push(t("hwb.queued"));
  if (sum) {
    lines.push(t("hwb.title", { verdict: t(`hw.verdict.${sum.headline}`) }));
    if (sum.top) lines.push(lt(sum.top, lang));
    lines.push(t("hwb.checked", { ago: ago(sum.savedAt) }));
  } else if (!busy) {
    lines.push(t("hwb.never"));
  }
  if (fleet?.error) lines.push(t("hwb.lastError", { err: t(`hwerr.${fleet.error}` as TKey) }));
  if (fleet?.nextRun && !busy) lines.push(t("hwb.next", { when: relTime(fleet.nextRun) }));

  const n = tone === "crit" ? sum?.crit : tone === "warn" ? sum?.warn : undefined;
  const sudo = !sum && fleet?.error === "sudo_required";
  const Icon = busy ? LoaderCircle : queued ? Clock : HardDrive;
  const label = lines.join("\n");
  const open = () => navigate({ name: "hardware", hostId });
  // In the sidebar the badge sits inside the row's button: not a second control.
  const interactive = compact
    ? { "aria-label": label }
    : {
        role: "button",
        tabIndex: 0,
        "aria-label": label,
        onClick: (e: React.MouseEvent) => {
          e.stopPropagation();
          open();
        },
        onKeyDown: (e: React.KeyboardEvent) => {
          if (e.key === "Enter" || e.key === " ") {
            e.preventDefault();
            e.stopPropagation();
            open();
          }
        },
      };

  return (
    <span className={`hw-badge ${busy ? "busy" : tone}${compact ? " compact" : ""}`} {...interactive}>
      <Icon size={compact ? 12 : 13} className={busy ? "spin" : undefined} />
      {n !== undefined && n > 0 && <b>{n}</b>}
      {sudo && !compact && <span className="mono">sudo</span>}
      <span className="hw-tip" role="tooltip">
        {lines.map((l, i) => (
          <span key={i} className={i === 0 ? "hw-tip-head" : undefined}>
            {l}
          </span>
        ))}
      </span>
    </span>
  );
}
