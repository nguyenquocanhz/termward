import { BellRing, CircleCheck, CircleX, Info, X } from "lucide-react";
import { dismissToast, useApp } from "../store";

const icons = { info: Info, success: CircleCheck, error: CircleX, alert: BellRing };

export function Toasts() {
  const toasts = useApp((s) => s.toasts);
  return (
    <div className="toasts" aria-live="polite">
      {toasts.map((t) => {
        const Icon = icons[t.kind];
        return (
          <div key={t.id} className={`toast ${t.kind}`}>
            <Icon size={17} className="lead" />
            <div className="grow">
              <div className="title">{t.title}</div>
              {t.body && (
                <div className="body" style={{ whiteSpace: "pre-line" }}>
                  {t.body}
                </div>
              )}
              {t.action && (
                <button
                  className="btn sm"
                  style={{ marginTop: 8 }}
                  onClick={() => {
                    t.action!.run();
                    dismissToast(t.id);
                  }}
                >
                  {t.action.label}
                </button>
              )}
            </div>
            <button className="icon-btn" style={{ width: 22, height: 22 }} onClick={() => dismissToast(t.id)}>
              <X size={13} />
            </button>
          </div>
        );
      })}
    </div>
  );
}
