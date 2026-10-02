// Helpers for showing Diagward hardware reports.
import type { HwComponent, HwReport, LText, Severity } from "./api";
import type { TKey } from "./i18n";

/** Picks the UI language from a bilingual Diagward text. */
export function lt(text: LText | undefined, lang: "en" | "vi"): string {
  if (!text) return "";
  return (lang === "vi" ? text.vi || text.en : text.en || text.vi) ?? "";
}

export const sevRank: Record<Severity, number> = { ok: 0, info: 1, warn: 2, crit: 3 };

export type Headline = "crit" | "warn" | "ok" | "guest" | "none";

/**
 * What the verdict banner says. Like Diagward's own report, a healthy VM or
 * container is not called "no hardware problems" (its hardware belongs to the
 * host), and a report where nothing could be checked does not reassure.
 */
export function headline(r: HwReport): Headline {
  if (r.verdict === "crit" || r.verdict === "warn") return r.verdict;
  if (!r.summary?.some((c) => c.checked)) return "none";
  if (r.env.os && r.env.os !== "bmc" && (r.env.virtual || r.env.container)) return "guest";
  return "ok";
}

/** The status colour for a headline. */
export function headlineLevel(h: Headline): "crit" | "warn" | "ok" | "info" {
  return h === "guest" || h === "none" ? "info" : h;
}

export type ComponentState = "crit" | "warn" | "partial" | "info" | "ok" | "none";

export function componentState(c: HwComponent): ComponentState {
  if (!c.checked) return "none";
  if (c.severity === "crit" || c.severity === "warn") return c.severity;
  if (c.partial) return "partial";
  return c.severity === "info" ? "info" : "ok";
}

const sectionDomains = [
  "meta",
  "system",
  "cpu",
  "memory",
  "disk",
  "raid",
  "sensors",
  "ipmi",
  "network",
  "filesystem",
  "logs",
] as const;

/** Friendly label key for a collector section name such as "disk.smart.sda". */
export function sectionLabelKey(section: string | undefined): TKey {
  const domain = (section ?? "meta").split(".")[0];
  return (sectionDomains as readonly string[]).includes(domain) ? (`hw.sec.${domain}` as TKey) : "hw.sec.other";
}

/** mm:ss */
export function elapsed(ms: number): string {
  const s = Math.max(0, Math.floor(ms / 1000));
  return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, "0")}`;
}

export function counts(r: HwReport): { crit: number; warn: number; info: number } {
  const c = { crit: 0, warn: 0, info: 0 };
  for (const f of r.findings ?? []) if (f.severity !== "ok") c[f.severity]++;
  return c;
}
