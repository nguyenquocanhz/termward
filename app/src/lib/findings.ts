import type { Finding, Level } from "./api";
import { t, type TKey } from "./i18n";

/** Human sentence for a health finding, in the current language. */
export function findingText(f: Finding): string {
  const key = `f.${f.code}` as TKey;
  const vars = {
    s: f.subject ?? "",
    v: f.code === "load" ? (f.value ?? 0).toFixed(2) : Math.round(f.value ?? 0),
    t: f.code === "load" ? (f.threshold ?? 0).toFixed(1) : Math.round(f.threshold ?? 0),
  };
  const s = t(key, vars);
  return s === key ? f.code : s;
}

export function levelLabelKey(level: Level | "off"): TKey {
  return `level.${level}` as TKey;
}

export const severity: Record<Level | "off", number> = {
  down: 5,
  crit: 4,
  warn: 3,
  unknown: 2,
  info: 1,
  ok: 1,
  off: 0,
};
