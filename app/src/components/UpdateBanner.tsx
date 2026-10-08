import { Download, RotateCw } from "lucide-react";
import { useShallow } from "zustand/react/shallow";
import { useT } from "../lib/i18n";
import { dismissUpdate, installUpdate, startUpdateDownload, useApp } from "../store";

/**
 * Proposes an app update. It never installs on its own: the user chooses
 * "Update now" (or "Open download page" where self-install is impossible) and,
 * once downloaded, "Restart now" — or "Later" to dismiss it for this run.
 */
export function UpdateBanner() {
  const t = useT();
  const u = useApp(useShallow((s) => s.update));
  if (u.status === "idle" || u.status === "checking") return null;

  // A stable, visually-hidden live region announces each state once. The visible
  // percentage is NOT in a live region, so it does not re-announce every tick.
  const announce =
    u.status === "available"
      ? t("upd.available", { version: u.version ?? "" })
      : u.status === "downloading"
        ? t("upd.downloadingSr")
        : t("upd.ready", { version: u.version ?? "" });

  return (
    <div className="update-banner">
      <span className="sr-only" role="status">
        {announce}
      </span>
      <Download size={16} className="lead" aria-hidden />
      <div className="grow">
        {u.status === "available" && (
          <>
            <strong>{t("upd.available", { version: u.version ?? "" })}</strong>
            <span className="muted">{u.canInstall === false ? t("upd.availableManual") : t("upd.availableBody")}</span>
          </>
        )}
        {u.status === "downloading" && (
          <>
            <strong>{t("upd.downloading", { percent: u.percent ?? 0 })}</strong>
            <div className="update-progress" aria-hidden>
              <span style={{ width: `${u.percent ?? 0}%` }} />
            </div>
          </>
        )}
        {u.status === "ready" && (
          <>
            <strong>{t("upd.ready", { version: u.version ?? "" })}</strong>
            <span className="muted">{t("upd.readyBody")}</span>
          </>
        )}
      </div>
      <div className="update-actions">
        {u.status === "available" && (
          <button className="btn primary sm" onClick={startUpdateDownload}>
            {u.canInstall === false ? (
              t("upd.openPage")
            ) : (
              <>
                <Download size={14} />
                {t("upd.update")}
              </>
            )}
          </button>
        )}
        {u.status === "ready" && (
          <button className="btn primary sm" onClick={installUpdate}>
            <RotateCw size={14} />
            {t("upd.restart")}
          </button>
        )}
        {u.status !== "downloading" && (
          <button className="btn sm" onClick={dismissUpdate}>
            {t("upd.later")}
          </button>
        )}
      </div>
    </div>
  );
}
