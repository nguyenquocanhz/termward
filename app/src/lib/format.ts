import { currentLang } from "./i18n";

export function pct(v: number | undefined, digits = 0): string {
  if (v === undefined || v < 0) return "—";
  return `${v.toFixed(digits)}%`;
}

export function bytesKb(kb: number): string {
  const units = ["KB", "MB", "GB", "TB", "PB"];
  let v = kb;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v >= 100 || i === 0 ? v.toFixed(0) : v.toFixed(1)} ${units[i]}`;
}

export function duration(sec: number): string {
  const vi = currentLang() === "vi";
  const d = Math.floor(sec / 86400);
  const h = Math.floor((sec % 86400) / 3600);
  const m = Math.floor((sec % 3600) / 60);
  const u = vi ? { d: " ngày", h: " giờ", m: " phút" } : { d: "d", h: "h", m: "m" };
  if (d > 0) return `${d}${u.d} ${h}${u.h}`;
  if (h > 0) return `${h}${u.h} ${m}${u.m}`;
  return `${Math.max(m, 0)}${u.m}`;
}

export function ago(iso: string | number | undefined): string {
  if (!iso) return "—";
  const t = typeof iso === "number" ? iso : Date.parse(iso);
  if (!t || t < 0) return "—";
  const s = Math.max(0, Math.round((Date.now() - t) / 1000));
  const rtf = new Intl.RelativeTimeFormat(currentLang(), { numeric: "auto" });
  if (s < 45) return rtf.format(-s, "second");
  if (s < 3600) return rtf.format(-Math.round(s / 60), "minute");
  if (s < 86400) return rtf.format(-Math.round(s / 3600), "hour");
  return rtf.format(-Math.round(s / 86400), "day");
}

export function clock(iso: string): string {
  return new Date(iso).toLocaleTimeString(currentLang(), { hour: "2-digit", minute: "2-digit" });
}

export function levelClass(value: number, warn: number, crit: number): "" | "warn" | "crit" {
  if (value >= crit) return "crit";
  if (value >= warn) return "warn";
  return "";
}

export function keyTypeLabel(type: string, bits: number): string {
  const name = { ed25519: "Ed25519", rsa: "RSA", ecdsa: "ECDSA" }[type] ?? type.toUpperCase();
  return type === "ed25519" ? name : `${name} ${bits}`;
}
