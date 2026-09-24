import { useEffect, useId, useRef, useState, type ReactNode } from "react";
import { X } from "lucide-react";
import type { Level } from "../lib/api";
import { useT } from "../lib/i18n";
import { levelLabelKey } from "../lib/findings";

export interface MenuItem {
  label: string;
  icon?: ReactNode;
  danger?: boolean;
  run: () => void;
}

/** Icon button with a small dropdown; closes on outside click or Escape. */
export function Menu({ label, icon, items }: { label: string; icon: ReactNode; items: MenuItem[] }) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => !ref.current?.contains(e.target as Node) && setOpen(false);
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && setOpen(false);
    window.addEventListener("mousedown", onDown);
    window.addEventListener("keydown", onKey);
    return () => {
      window.removeEventListener("mousedown", onDown);
      window.removeEventListener("keydown", onKey);
    };
  }, [open]);
  return (
    <div className="menu" ref={ref}>
      <button
        className="icon-btn"
        title={label}
        aria-haspopup="menu"
        aria-expanded={open}
        onClick={() => setOpen(!open)}
      >
        {icon}
      </button>
      {open && (
        <div className="menu-list" role="menu">
          {items.map((it) => (
            <button
              key={it.label}
              role="menuitem"
              className={`menu-item${it.danger ? " danger" : ""}`}
              onClick={() => {
                setOpen(false);
                it.run();
              }}
            >
              {it.icon}
              {it.label}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

export function StatusDot({ level, checking }: { level: Level | "off"; checking?: boolean }) {
  return <span className={`dot ${level}${checking ? " checking" : ""}`} aria-hidden />;
}

export function LevelBadge({
  level,
  pending,
  checking,
}: {
  level: Level | "off";
  pending?: Level;
  checking?: boolean;
}) {
  const t = useT();
  const cls = level === "unknown" || level === "off" ? "" : level;
  return (
    <span className={`badge ${cls}`}>
      <StatusDot level={level} checking={checking} />
      {t(levelLabelKey(level))}
      {pending && pending !== level && <span className="faint">· {t("level.pending")}</span>}
    </span>
  );
}

export function Meter({ value, warn, crit }: { value: number; warn: number; crit: number }) {
  const cls = value >= crit ? "crit" : value >= warn ? "warn" : "";
  return (
    <div className={`meter ${cls}`}>
      <span style={{ width: `${Math.max(0, Math.min(100, value))}%` }} />
    </div>
  );
}

/** Tiny area chart. Values outside 0..max are clamped; negatives are gaps. */
export function Sparkline({
  values,
  max = 100,
  height = 36,
  color = "var(--accent)",
}: {
  values: number[];
  max?: number;
  height?: number;
  color?: string;
}) {
  const id = "spark" + useId().replace(/[^a-zA-Z0-9]/g, "");
  const pts = values.filter((v) => v >= 0);
  if (pts.length < 2) return <div style={{ height }} />;
  const w = 200;
  const top = Math.max(max, ...pts);
  const step = w / (pts.length - 1);
  const y = (v: number) => height - 2 - (Math.min(v, top) / top) * (height - 4);
  const line = pts.map((v, i) => `${i === 0 ? "M" : "L"}${(i * step).toFixed(1)},${y(v).toFixed(1)}`).join(" ");
  const area = `${line} L${w},${height} L0,${height} Z`;
  return (
    <svg className="spark" viewBox={`0 0 ${w} ${height}`} preserveAspectRatio="none" width="100%" height={height}>
      <defs>
        <linearGradient id={id} x1="0" y1="0" x2="0" y2="1">
          <stop offset="0" stopColor={color} stopOpacity="0.22" />
          <stop offset="1" stopColor={color} stopOpacity="0" />
        </linearGradient>
      </defs>
      <path d={area} fill={`url(#${id})`} />
      <path d={line} fill="none" stroke={color} strokeWidth="1.5" vectorEffect="non-scaling-stroke" />
    </svg>
  );
}

export function Switch({ on, onChange, label }: { on: boolean; onChange: (v: boolean) => void; label?: string }) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={on}
      aria-label={label}
      className={`switch${on ? " on" : ""}`}
      onClick={() => onChange(!on)}
    />
  );
}

export function Segmented<T extends string>({
  value,
  options,
  onChange,
}: {
  value: T;
  options: { value: T; label: ReactNode }[];
  onChange: (v: T) => void;
}) {
  return (
    <div className="segmented" role="tablist">
      {options.map((o) => (
        <button
          key={o.value}
          type="button"
          role="tab"
          aria-selected={o.value === value}
          className={o.value === value ? "on" : ""}
          onClick={() => onChange(o.value)}
        >
          {o.label}
        </button>
      ))}
    </div>
  );
}

export function Field({
  label,
  hint,
  error,
  children,
}: {
  label: ReactNode;
  hint?: ReactNode;
  error?: ReactNode;
  children: ReactNode;
}) {
  return (
    <div className="field">
      <label>{label}</label>
      {children}
      {error ? <div className="error">{error}</div> : hint ? <div className="hint">{hint}</div> : null}
    </div>
  );
}

export function Kbd({ children }: { children: ReactNode }) {
  return <span className="kbd">{children}</span>;
}

export const isMac = window.termward ? window.termward.platform === "darwin" : /Mac/.test(navigator.userAgent);
export const modKey = isMac ? "⌘" : "Ctrl";

export function Modal({
  title,
  subtitle,
  icon,
  wide,
  onClose,
  children,
  footer,
}: {
  title: ReactNode;
  subtitle?: ReactNode;
  icon?: ReactNode;
  wide?: boolean;
  onClose: () => void;
  children: ReactNode;
  footer?: ReactNode;
}) {
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    const first = ref.current?.querySelector<HTMLElement>("input:not([type=checkbox]), textarea, select");
    first?.focus();
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  return (
    <div className="overlay" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className={`dialog${wide ? " wide" : ""}`} ref={ref} role="dialog" aria-modal>
        <div className="dialog-head">
          {icon}
          <div style={{ flex: 1, minWidth: 0 }}>
            <h2>{title}</h2>
            {subtitle && <p>{subtitle}</p>}
          </div>
          <button className="icon-btn" onClick={onClose} aria-label="Close">
            <X size={16} />
          </button>
        </div>
        <div className="dialog-body">{children}</div>
        {footer && <div className="dialog-foot">{footer}</div>}
      </div>
    </div>
  );
}
