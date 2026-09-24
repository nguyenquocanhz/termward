// Typed client for the Go core (see core/internal/api). Types mirror the JSON
// the core sends.

export type AuthMethod = "key" | "password" | "agent";
export type Level = "unknown" | "ok" | "info" | "warn" | "crit" | "down";

export interface Host {
  id: string;
  name: string;
  address: string;
  port: number;
  user: string;
  group: string;
  tags: string[];
  auth: AuthMethod;
  keyId?: string;
  jumpHostId?: string;
  monitor: boolean;
  notes?: string;
  createdAt: string;
  updatedAt: string;
}

export type HostInput = Omit<Host, "id" | "createdAt" | "updatedAt"> & {
  password?: string;
  rememberPassword?: boolean;
};

export interface Key {
  id: string;
  name: string;
  type: string;
  bits: number;
  fingerprint: string;
  publicKey: string;
  comment: string;
  encrypted: boolean;
  source: string;
  createdAt: string;
}

export interface KeyCandidate {
  path: string;
  name: string;
  type: string;
  fingerprint: string;
  encrypted: boolean;
  imported: boolean;
}

export interface Snippet {
  id: string;
  name: string;
  command: string;
  createdAt: string;
}

export interface Thresholds {
  cpuWarn: number;
  cpuCrit: number;
  memWarn: number;
  memCrit: number;
  diskWarn: number;
  diskCrit: number;
  loadPerCoreWarn: number;
  loadPerCoreCrit: number;
}

export interface Settings {
  pollIntervalSec: number;
  notifications: boolean;
  thresholds: Thresholds;
}

export interface Finding {
  code: string;
  level: Level;
  subject?: string;
  value?: number;
  threshold?: number;
}

export interface Disk {
  mount: string;
  totalKb: number;
  usedKb: number;
  availKb: number;
  percent: number;
}

export interface Container {
  name: string;
  state: string;
  status: string;
  health?: string;
}

export interface Sample {
  at: string;
  latencyMs: number;
  os: string;
  kernel: string;
  hostname: string;
  cpus: number;
  uptimeSec: number;
  load: [number, number, number];
  cpuPercent: number;
  memTotalKb: number;
  memUsedKb: number;
  memPercent: number;
  swapTotalKb: number;
  swapUsedKb: number;
  disks: Disk[];
  systemd: boolean;
  failedUnits: string[];
  docker: "" | "ok" | "denied";
  containers: Container[];
  rebootRequired: boolean;
}

export interface Point {
  t: number;
  cpu: number;
  mem: number;
  load: number;
}

export interface Status {
  hostId: string;
  level: Level;
  pending?: Level;
  since: string;
  checkedAt: string;
  checking: boolean;
  findings: Finding[];
  sample?: Sample;
  error?: string;
  errorKind?: string;
  history: Point[];
  mutedUntil?: string;
}

export interface Alert {
  id: string;
  hostId: string;
  hostName: string;
  from: Level;
  to: Level;
  findings: Finding[];
  error?: string;
  at: string;
}

export interface ExecResult {
  stdout: string;
  stderr: string;
  exitCode: number;
  durationMs: number;
  truncated: boolean;
}

export interface ExecEvent {
  jobId: string;
  hostId: string;
  state: "running" | "done" | "error";
  result?: ExecResult;
  error?: string;
}

export interface ConfigCandidate {
  alias: string;
  hostName: string;
  user: string;
  port: number;
  identityFile?: string;
  proxyJump?: string;
  exists: boolean;
}

/** Host-key or credential problem the connect flow can resolve with the user. */
export interface HostKeyDetails {
  hostId: string;
  address: string;
  keyType: string;
  fingerprint: string;
}
export interface AuthDetails {
  hostId: string;
  kind: "password" | "passphrase";
  keyId?: string;
  wrong: boolean;
}

export class ApiError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
    public details?: unknown,
  ) {
    super(message);
  }
}

interface CoreInfo {
  url: string;
  wsUrl: string;
  token: string;
}

let core: CoreInfo | null = null;

/** Finds the core: from Electron, or from ?core=&token= when run in a browser. */
export async function initCore(): Promise<void> {
  if (window.termward) {
    core = await window.termward.coreInfo();
  } else {
    const q = new URLSearchParams(location.search);
    const url = q.get("core") ?? "http://127.0.0.1:7717";
    core = { url, wsUrl: url.replace(/^http/, "ws"), token: q.get("token") ?? "dev" };
  }
  if (!core) throw new Error("The Termward core is not running.");
  await req("GET", "/api/info");
}

async function req<T>(method: string, path: string, body?: unknown): Promise<T> {
  if (!core) throw new Error("core not initialised");
  let res: Response;
  try {
    res = await fetch(core.url + path, {
      method,
      headers: {
        Authorization: `Bearer ${core.token}`,
        ...(body !== undefined ? { "Content-Type": "application/json" } : {}),
      },
      body: body !== undefined ? JSON.stringify(body) : undefined,
    });
  } catch {
    throw new ApiError(0, "offline", "Cannot reach the Termward core.");
  }
  const text = await res.text();
  const data = text ? JSON.parse(text) : undefined;
  if (!res.ok) {
    const e = data?.error;
    throw new ApiError(res.status, e?.code ?? "error", e?.message ?? res.statusText, e?.details);
  }
  return data as T;
}

export function wsUrl(path: string, params: Record<string, string | number> = {}): string {
  if (!core) throw new Error("core not initialised");
  const q = new URLSearchParams({
    ...Object.fromEntries(Object.entries(params).map(([k, v]) => [k, String(v)])),
    token: core.token,
  });
  return `${core.wsUrl}${path}?${q}`;
}

export const api = {
  info: () => req<{ name: string; version: string }>("GET", "/api/info"),
  hosts: () => req<Host[]>("GET", "/api/hosts"),
  createHost: (h: HostInput) => req<Host>("POST", "/api/hosts", h),
  updateHost: (id: string, h: HostInput) => req<Host>("PUT", `/api/hosts/${id}`, h),
  deleteHost: (id: string) => req<void>("DELETE", `/api/hosts/${id}`),
  connect: (id: string, creds?: { password?: string; passphrase?: string; remember?: boolean }) =>
    req<{ connected: boolean }>("POST", `/api/hosts/${id}/connect`, creds ?? {}),
  disconnect: (id: string) => req<void>("POST", `/api/hosts/${id}/disconnect`),
  trust: (id: string, fingerprint: string) => req<void>("POST", `/api/hosts/${id}/trust`, { fingerprint }),
  check: (id: string) => req<void>("POST", `/api/hosts/${id}/check`),
  power: (id: string, action: "reboot" | "poweroff", sudoPassword?: string) =>
    req<{ action: string; mutedUntil: string }>("POST", `/api/hosts/${id}/power`, { action, sudoPassword }),

  status: () => req<{ statuses: Status[]; connected: string[] }>("GET", "/api/status"),
  alerts: () => req<Alert[]>("GET", "/api/alerts"),

  keys: () => req<Key[]>("GET", "/api/keys"),
  scanKeys: () => req<KeyCandidate[]>("GET", "/api/keys/scan"),
  generateKey: (body: {
    name: string;
    type: string;
    bits?: number;
    comment?: string;
    passphrase?: string;
    rememberPassphrase?: boolean;
  }) => req<Key>("POST", "/api/keys/generate", body),
  importKey: (body: {
    name?: string;
    path?: string;
    pem?: string;
    passphrase?: string;
    rememberPassphrase?: boolean;
  }) => req<Key>("POST", "/api/keys/import", body),
  renameKey: (id: string, name: string) => req<Key>("PATCH", `/api/keys/${id}`, { name }),
  deleteKey: (id: string) => req<void>("DELETE", `/api/keys/${id}`),
  deployKey: (id: string, hostId: string, switchAuth: boolean) =>
    req<{ alreadyPresent: boolean; host: Host }>("POST", `/api/keys/${id}/deploy`, { hostId, switchAuth }),

  snippets: () => req<Snippet[]>("GET", "/api/snippets"),
  saveSnippet: (s: { id?: string; name: string; command: string }) =>
    s.id ? req<Snippet>("PUT", `/api/snippets/${s.id}`, s) : req<Snippet>("POST", "/api/snippets", s),
  deleteSnippet: (id: string) => req<void>("DELETE", `/api/snippets/${id}`),

  settings: () => req<Settings>("GET", "/api/settings"),
  saveSettings: (s: Settings) => req<Settings>("PUT", "/api/settings", s),

  exec: (jobId: string, hostIds: string[], command: string, timeoutSec: number) =>
    req<{ jobId: string }>("POST", "/api/exec", { jobId, hostIds, command, timeoutSec }),
  cancelExec: (jobId: string) => req<void>("POST", `/api/exec/${jobId}/cancel`),

  sshConfig: () => req<ConfigCandidate[]>("GET", "/api/import/ssh-config"),
  importSshConfig: (aliases: string[], group: string) =>
    req<{ imported: Host[]; skipped: string[]; failed: Record<string, string> }>("POST", "/api/import/ssh-config", {
      aliases,
      group,
    }),
};

/** Subscribes to server events, reconnecting with backoff. Returns a stop function. */
export function subscribeEvents(
  onEvent: (type: string, data: unknown) => void,
  onState: (connected: boolean) => void,
): () => void {
  let ws: WebSocket | null = null;
  let stopped = false;
  let attempt = 0;
  let timer: number | undefined;

  const open = () => {
    ws = new WebSocket(wsUrl("/ws/events"));
    ws.onopen = () => {
      attempt = 0;
      onState(true);
    };
    ws.onmessage = (m) => {
      try {
        const msg = JSON.parse(m.data as string);
        onEvent(msg.type, msg.data);
      } catch {
        /* ignore malformed frames */
      }
    };
    ws.onclose = () => {
      onState(false);
      if (!stopped) timer = window.setTimeout(open, Math.min(500 * 2 ** attempt++, 8000));
    };
  };
  open();
  return () => {
    stopped = true;
    window.clearTimeout(timer);
    ws?.close();
  };
}
