import { create } from "zustand";
import {
  api,
  ApiError,
  initCore,
  subscribeEvents,
  type Alert,
  type AuthDetails,
  type ExecEvent,
  type Host,
  type HostKeyDetails,
  type Key,
  type Level,
  type Settings,
  type Snippet,
  type Status,
} from "./lib/api";
import { currentLang, t } from "./lib/i18n";
import { findingText } from "./lib/findings";
import { installBridge } from "./lib/platform";

export type View =
  | { name: "overview" }
  | { name: "host"; hostId: string }
  | { name: "terminals" }
  | { name: "keys" }
  | { name: "run" }
  | { name: "settings" };

export interface TermTab {
  id: string;
  hostId: string;
  title: string;
  /** Bumped to force a fresh session (reconnect). */
  nonce: number;
}

export interface ExecJob {
  id: string;
  command: string;
  hostIds: string[];
  startedAt: number;
  results: Record<string, ExecEvent>;
  done: boolean;
}

export type Dialog =
  | { kind: "host"; host?: Host }
  | { kind: "generateKey"; onCreated?: (k: Key) => void }
  | { kind: "importKey" }
  | { kind: "deployKey"; keyId: string; hostId?: string }
  | { kind: "importConfig" }
  | { kind: "power"; hostId: string; action: "reboot" | "poweroff" }
  | {
      kind: "confirm";
      title: string;
      body: string;
      confirm: string;
      danger?: boolean;
      run: () => Promise<void> | void;
    };

export type Prompt =
  | {
      kind: "trust";
      details: HostKeyDetails;
      changed: boolean;
      hostName: string;
      resolve: (ok: boolean) => void;
    }
  | {
      kind: "credential";
      details: AuthDetails;
      hostName: string;
      target: string;
      keyName?: string;
      resolve: (v: { value: string; remember: boolean } | null) => void;
    };

export interface Toast {
  id: string;
  kind: "info" | "success" | "error" | "alert";
  title: string;
  body?: string;
  action?: { label: string; run: () => void };
}

export type ThemePref = "system" | "light" | "dark";
export type LangPref = "auto" | "en" | "vi";

interface AppState {
  booted: boolean;
  bootError?: string;
  online: boolean;
  hosts: Host[];
  keys: Key[];
  snippets: Snippet[];
  settings: Settings | null;
  statuses: Record<string, Status>;
  connected: Record<string, boolean>;
  alerts: Alert[];
  view: View;
  tabs: TermTab[];
  activeTab: string | null;
  jobs: Record<string, ExecJob>;
  lastJob: string | null;
  theme: ThemePref;
  lang: LangPref;
  dialog: Dialog | null;
  prompt: Prompt | null;
  toasts: Toast[];
  palette: boolean;
  /** Mobile: the sidebar is shown as a drawer. */
  drawer: boolean;
}

const PREFS_KEY = "termward.prefs";

function loadPrefs(): { theme: ThemePref; lang: LangPref } {
  try {
    const p = JSON.parse(localStorage.getItem(PREFS_KEY) ?? "{}");
    return {
      theme: ["system", "light", "dark"].includes(p.theme) ? p.theme : "system",
      lang: ["auto", "en", "vi"].includes(p.lang) ? p.lang : "auto",
    };
  } catch {
    return { theme: "system", lang: "auto" };
  }
}

export const useApp = create<AppState>(() => ({
  booted: false,
  online: false,
  hosts: [],
  keys: [],
  snippets: [],
  settings: null,
  statuses: {},
  connected: {},
  alerts: [],
  view: { name: "overview" },
  tabs: [],
  activeTab: null,
  jobs: {},
  lastJob: null,
  ...loadPrefs(),
  dialog: null,
  prompt: null,
  toasts: [],
  palette: false,
  drawer: false,
}));

const set = useApp.setState;
const get = useApp.getState;

export const uid = () => crypto.randomUUID();

export function errorText(e: unknown): string {
  if (e instanceof ApiError || e instanceof Error) return e.message;
  return String(e);
}

// ------------------------------------------------------------------ boot

let booting = false;

export async function boot(): Promise<void> {
  if (booting) return; // StrictMode runs effects twice in development
  booting = true;
  try {
    await installBridge();
    await initCore();
    const [hosts, keys, snippets, settings, status, alerts] = await Promise.all([
      api.hosts(),
      api.keys(),
      api.snippets(),
      api.settings(),
      api.status(),
      api.alerts(),
    ]);
    set({
      hosts: hosts ?? [],
      keys: keys ?? [],
      snippets: snippets ?? [],
      settings,
      alerts: alerts ?? [],
      statuses: Object.fromEntries(status.statuses.map((s) => [s.hostId, s])),
      connected: Object.fromEntries(status.connected.map((id) => [id, true])),
      booted: true,
    });
    subscribeEvents(onEvent, (online) => {
      set({ online });
      if (online) void refreshStatus();
    });
    window.termward?.onNotificationClick((hostId) => navigate({ name: "host", hostId }));
  } catch (e) {
    set({ bootError: errorText(e) });
  }
}

export async function refreshHosts() {
  set({ hosts: (await api.hosts()) ?? [] });
}
export async function refreshKeys() {
  set({ keys: (await api.keys()) ?? [] });
}
export async function refreshSnippets() {
  set({ snippets: (await api.snippets()) ?? [] });
}
export async function refreshStatus() {
  const s = await api.status();
  set({
    statuses: Object.fromEntries(s.statuses.map((x) => [x.hostId, x])),
    connected: Object.fromEntries(s.connected.map((id) => [id, true])),
  });
}

// ------------------------------------------------------------------ events

function onEvent(type: string, data: unknown) {
  switch (type) {
    case "status": {
      const s = data as Status;
      set((st) => ({ statuses: { ...st.statuses, [s.hostId]: s } }));
      break;
    }
    case "status_removed": {
      const { hostId } = data as { hostId: string };
      set((st) => {
        const next = { ...st.statuses };
        delete next[hostId];
        return { statuses: next };
      });
      break;
    }
    case "alert": {
      const a = data as Alert;
      set((st) => ({ alerts: [a, ...st.alerts].slice(0, 200) }));
      queueNotification(a);
      break;
    }
    case "exec": {
      const ev = data as ExecEvent;
      set((st) => {
        const job = st.jobs[ev.jobId];
        if (!job) return {};
        return { jobs: { ...st.jobs, [ev.jobId]: { ...job, results: { ...job.results, [ev.hostId]: ev } } } };
      });
      break;
    }
    case "hosts_changed":
      void refreshHosts();
      break;
    case "keys_changed":
      void refreshKeys();
      break;
    case "exec_done": {
      const { jobId } = data as { jobId: string };
      set((st) => (st.jobs[jobId] ? { jobs: { ...st.jobs, [jobId]: { ...st.jobs[jobId], done: true } } } : {}));
      break;
    }
  }
}

// Alerts arriving together (e.g. a network outage) become one notification.
let pending: Alert[] = [];
let flushTimer: number | undefined;

function queueNotification(a: Alert) {
  pending.push(a);
  window.clearTimeout(flushTimer);
  flushTimer = window.setTimeout(flushNotifications, 1500);
}

function alertTitle(a: Alert): string {
  const key = ({ down: "notif.down", crit: "notif.crit", warn: "notif.warn", ok: "notif.ok" } as const)[
    a.to as "down" | "crit" | "warn" | "ok"
  ];
  return key ? t(key, { host: a.hostName }) : a.hostName;
}

export function alertBody(a: Alert): string {
  const top = a.findings.find((f) => f.level !== "info");
  if (top) return findingText(top);
  return a.error ?? "";
}

function flushNotifications() {
  const batch = pending;
  pending = [];
  if (batch.length === 0) return;
  const notify = get().settings?.notifications ?? true;

  if (batch.length > 3) {
    if (notify) window.termward?.notify({ title: t("notif.many", { n: batch.length }), body: t("notif.manyBody") });
    toast("alert", t("notif.many", { n: batch.length }), t("notif.manyBody"));
    return;
  }
  for (const a of batch) {
    if (notify) window.termward?.notify({ title: alertTitle(a), body: alertBody(a), hostId: a.hostId });
    toast(a.to === "ok" ? "success" : "alert", alertTitle(a), alertBody(a), {
      label: t("common.open"),
      run: () => navigate({ name: "host", hostId: a.hostId }),
    });
  }
}

// ------------------------------------------------------------------ ui

export function navigate(view: View) {
  set({ view, palette: false, drawer: false });
}

/** Desktop "Quit" (the window's × may only hide to the tray). */
export function confirmQuit() {
  confirmAction({
    title: t("app.quitTitle"),
    body: t("app.quitBody"),
    confirm: t("app.quit"),
    danger: true,
    run: () => window.termward?.quit?.(),
  });
}

/**
 * Android back button: close the topmost layer, else return to Overview.
 * Returns false when there is nothing left to close (the app then minimizes).
 */
export function goBack(): boolean {
  const s = get();
  if (s.prompt) {
    if (s.prompt.kind === "trust") s.prompt.resolve(false);
    else s.prompt.resolve(null);
    return true;
  }
  if (s.dialog) return (closeDialog(), true);
  if (s.palette) return (set({ palette: false }), true);
  if (s.drawer) return (set({ drawer: false }), true);
  if (s.view.name !== "overview") return (navigate({ name: "overview" }), true);
  return false;
}

export function toast(kind: Toast["kind"], title: string, body?: string, action?: Toast["action"]) {
  const id = uid();
  set((s) => ({ toasts: [...s.toasts.slice(-3), { id, kind, title, body, action }] }));
  window.setTimeout(() => dismissToast(id), kind === "error" ? 9000 : 6000);
}

export function dismissToast(id: string) {
  set((s) => ({ toasts: s.toasts.filter((x) => x.id !== id) }));
}

export function openDialog(d: Dialog) {
  set({ dialog: d, palette: false });
}
export function closeDialog() {
  set({ dialog: null });
}

export function confirmAction(opts: Omit<Extract<Dialog, { kind: "confirm" }>, "kind">) {
  openDialog({ kind: "confirm", ...opts });
}

export function setPrefs(p: Partial<{ theme: ThemePref; lang: LangPref }>) {
  set(p);
  const { theme, lang } = get();
  if (p.lang) window.termward?.setLanguage?.(currentLang());
  try {
    localStorage.setItem(PREFS_KEY, JSON.stringify({ theme, lang }));
  } catch {
    /* private mode: keep in memory */
  }
}

// ------------------------------------------------------------------ connect flow

export function hostById(id: string): Host | undefined {
  return get().hosts.find((h) => h.id === id);
}

/**
 * Connects to a host, walking the user through host-key verification and
 * password/passphrase prompts as the core asks for them (also for jump hosts).
 * Resolves false if the user cancels or the connection fails.
 */
export async function ensureConnected(
  hostId: string,
  creds?: { password?: string; passphrase?: string; remember?: boolean },
): Promise<boolean> {
  for (let attempt = 0; attempt < 6; attempt++) {
    try {
      await api.connect(hostId, attempt === 0 ? creds : undefined);
      set((s) => ({ connected: { ...s.connected, [hostId]: true } }));
      return true;
    } catch (e) {
      if (!(e instanceof ApiError)) {
        toast("error", t("err.generic"), errorText(e));
        return false;
      }
      if (e.code === "unknown_host" || e.code === "host_key_changed") {
        const d = e.details as HostKeyDetails;
        const ok = await new Promise<boolean>((resolve) =>
          set({
            prompt: {
              kind: "trust",
              details: d,
              changed: e.code === "host_key_changed",
              hostName: hostById(d.hostId)?.name ?? d.address,
              resolve,
            },
          }),
        );
        set({ prompt: null });
        if (!ok) return false;
        try {
          await api.trust(d.hostId, d.fingerprint);
        } catch (err) {
          toast("error", t("err.generic"), errorText(err));
          return false;
        }
        continue;
      }
      if (e.code === "auth_required") {
        const d = e.details as AuthDetails;
        const h = hostById(d.hostId);
        const key = d.keyId ? get().keys.find((k) => k.id === d.keyId) : undefined;
        const answer = await new Promise<{ value: string; remember: boolean } | null>((resolve) =>
          set({
            prompt: {
              kind: "credential",
              details: d,
              hostName: h?.name ?? hostId,
              target: h ? `${h.user}@${h.address}` : hostId,
              keyName: key?.name,
              resolve,
            },
          }),
        );
        set({ prompt: null });
        if (!answer) return false;
        const c = d.kind === "password" ? { password: answer.value } : { passphrase: answer.value };
        if (d.hostId !== hostId) {
          // The prompt was for a jump host: unlock it first, then retry ours.
          if (!(await ensureConnected(d.hostId, { ...c, remember: answer.remember }))) return false;
          continue;
        }
        creds = { ...c, remember: answer.remember };
        attempt = -1; // next iteration sends the credentials
        continue;
      }
      toast("error", hostById(hostId)?.name ?? t("err.generic"), e.message);
      return false;
    }
  }
  return false;
}

// ------------------------------------------------------------------ terminals

export async function openTerminal(hostId: string) {
  const h = hostById(hostId);
  if (!h) return;
  if (!(await ensureConnected(hostId))) return;
  const sameHost = get().tabs.filter((x) => x.hostId === hostId).length;
  const tab: TermTab = { id: uid(), hostId, title: sameHost ? `${h.name} (${sameHost + 1})` : h.name, nonce: 0 };
  set((s) => ({ tabs: [...s.tabs, tab], activeTab: tab.id, view: { name: "terminals" }, palette: false }));
}

export function closeTab(id: string) {
  set((s) => {
    const idx = s.tabs.findIndex((x) => x.id === id);
    const tabs = s.tabs.filter((x) => x.id !== id);
    const activeTab = s.activeTab === id ? (tabs[Math.max(0, idx - 1)]?.id ?? null) : s.activeTab;
    return { tabs, activeTab };
  });
}

export async function reconnectTab(id: string) {
  const tab = get().tabs.find((x) => x.id === id);
  if (!tab || !(await ensureConnected(tab.hostId))) return;
  set((s) => ({ tabs: s.tabs.map((x) => (x.id === id ? { ...x, nonce: x.nonce + 1 } : x)) }));
}

// ------------------------------------------------------------------ exec

export async function runExec(hostIds: string[], command: string, timeoutSec: number) {
  const id = uid();
  const job: ExecJob = { id, command, hostIds, startedAt: Date.now(), results: {}, done: false };
  set((s) => ({ jobs: { ...s.jobs, [id]: job }, lastJob: id }));
  try {
    await api.exec(id, hostIds, command, timeoutSec);
  } catch (e) {
    set((s) => ({ jobs: { ...s.jobs, [id]: { ...job, done: true } } }));
    toast("error", t("err.generic"), errorText(e));
  }
}

// ------------------------------------------------------------------ helpers

export function levelOf(hostId: string): Level | "off" {
  const h = hostById(hostId);
  if (h && !h.monitor) return "off";
  return get().statuses[hostId]?.level ?? "unknown";
}
