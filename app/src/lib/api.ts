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

export type HardwareInterval = "off" | "daily" | "weekly";

export interface Settings {
  pollIntervalSec: number;
  notifications: boolean;
  thresholds: Thresholds;
  /** Scheduled hardware checks of monitored servers. */
  hardwareInterval: HardwareInterval;
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
  /** "hardware" for alerts raised by a hardware check (with their own text). */
  kind?: "hardware";
  title?: LText;
  body?: LText;
  hardware?: HardwareChange[];
}

/** A Diagward finding that appeared, got worse or was resolved since the last check. */
export interface HardwareChange {
  change: "new" | "worse" | "resolved";
  id: string;
  target?: string;
  component?: string;
  from?: Severity;
  to: Severity;
  title: LText;
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

/** Which file an import read and what state it is in (never its contents). */
export interface ImportSource {
  path: string;
  /** false: there is no such file. */
  exists: boolean;
  /** Why an existing file could not be used. */
  error?: "parse" | "not_a_file" | "too_large" | "unreadable";
  errorLine?: number;
}

export interface SshConfigImport extends ImportSource {
  entries: ConfigCandidate[];
}

/** A server from known_hosts: where the user connected, not as whom. */
export interface KnownCandidate {
  name: string;
  address: string;
  port: number;
  exists: boolean;
}

export interface KnownHostsImport extends ImportSource {
  entries: KnownCandidate[];
  /** Lines written with HashKnownHosts: their servers cannot be listed. */
  hashed: number;
  /** Lines that are not known_hosts entries. */
  invalid: number;
  /** The local account name, suggested as the user to sign in with. */
  defaultUser: string;
}

export interface ImportResult {
  imported: Host[];
  skipped: string[];
  failed: Record<string, string>;
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

// ------------------------------------------------------------ hardware (Diagward)

/** Diagward severity of a finding or component. */
export type Severity = "ok" | "info" | "warn" | "crit";
/** A message in both languages. */
export interface LText {
  en: string;
  vi: string;
}

export interface HwPart {
  kind: string;
  vendor?: string;
  model?: string;
  serial?: string;
  location?: string;
  firmware?: string;
  size?: string;
}

export interface HwFinding {
  id: string;
  component: string;
  severity: Severity;
  target?: string;
  title: LText;
  detail: LText;
  action: LText;
  evidence?: string[];
  part?: HwPart;
}

export interface HwComponent {
  component: string;
  name: LText;
  severity: Severity;
  crit: number;
  warn: number;
  info: number;
  checked: boolean;
  partial?: boolean;
}

export interface HwCoverage {
  id: string;
  component: string;
  name: LText;
  state: "ran" | "partial" | "skipped" | "failed";
  reason?: LText;
  fix?: LText;
  cmd?: string;
  /** Cannot exist on this platform or covered elsewhere: not a gap. */
  notApplicable?: boolean;
}

/** The subset of Diagward's model.Report the UI shows. */
export interface HwReport {
  tool: string;
  version: string;
  host: {
    hostname: string;
    os?: string;
    kernel?: string;
    arch?: string;
    vendor?: string;
    model?: string;
    serial?: string;
    bios?: string;
    cpu?: string;
    memBytes?: number;
    virtual?: string;
  };
  env: { os: string; root: boolean; virtual?: string; container?: boolean; distro?: string; sinceDays: number };
  collected: string;
  seconds: number;
  verdict: Severity;
  summary: HwComponent[];
  findings: HwFinding[];
  coverage: HwCoverage[];
  notes?: LText[];
}

/** One line of the "parts to replace" list (merged per physical part). */
export interface HwPartRow {
  kind: LText;
  vendor?: string;
  model?: string;
  serial?: string;
  location?: string;
  firmware?: string;
  size?: string;
  severity: Severity;
  why: LText;
  target?: string;
}

export interface HardwareResult {
  report: HwReport;
  ranAs: "root" | "sudo" | "user" | "admin";
  savedAt: string;
  partial?: boolean;
  parts: HwPartRow[];
  /** The parts list as plain text for a warranty email ("" when nothing to replace). */
  rma: LText;
}

export type HwReportFormat = "html" | "md" | "json";

/** The gist of a host's last hardware result (for badges). */
export interface HardwareSummary {
  verdict: Severity;
  headline: "crit" | "warn" | "ok" | "guest" | "none";
  savedAt: string;
  ranAs: HardwareResult["ranAs"];
  partial?: boolean;
  crit: number;
  warn: number;
  info: number;
  top?: LText;
}

/** Fleet view of one host: last result, schedule and unattended-check state. */
export interface HardwareHost {
  hostId: string;
  summary?: HardwareSummary;
  /** queued/running: unattended check; manual: someone runs one by hand. */
  state?: "queued" | "running" | "manual";
  nextRun?: string;
  /** Code of the last failed unattended check, e.g. "sudo_required". */
  error?: string;
  errorMsg?: string;
  errorAt?: string;
}

/** One "check hardware on all servers" run. */
export interface HardwareRun {
  id: string;
  active: boolean;
  startedAt: string;
  finishedAt?: string;
  hosts: string[];
  done: number;
  ok: string[];
  needsSudo: string[];
  failed: Record<string, string>;
  skipped: string[];
}

export interface HardwareFleet {
  interval: HardwareInterval;
  hosts: HardwareHost[];
  run?: HardwareRun;
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

/** Fetches a file from the core with the session token (for downloads). */
async function fetchFile(path: string): Promise<{ blob: Blob; filename: string }> {
  if (!core) throw new Error("core not initialised");
  let res: Response;
  try {
    res = await fetch(core.url + path, { headers: { Authorization: `Bearer ${core.token}` } });
  } catch {
    throw new ApiError(0, "offline", "Cannot reach the Termward core.");
  }
  if (!res.ok) {
    const data = await res.json().catch(() => undefined);
    const e = data?.error;
    throw new ApiError(res.status, e?.code ?? "error", e?.message ?? res.statusText, e?.details);
  }
  const cd = res.headers.get("Content-Disposition") ?? "";
  const filename = /filename="([^"]+)"/.exec(cd)?.[1] ?? "diagward-report";
  return { blob: await res.blob(), filename };
}

export function hardwareReportPath(hostId: string, format: HwReportFormat, lang: "en" | "vi"): string {
  return `/api/hosts/${encodeURIComponent(hostId)}/hardware/report?format=${format}&lang=${lang}`;
}

/** Downloads the last hardware report through a Blob (works in Electron and browsers). */
export async function downloadHardwareReport(hostId: string, format: HwReportFormat, lang: "en" | "vi") {
  const { blob, filename } = await fetchFile(hardwareReportPath(hostId, format, lang));
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = filename;
  a.rel = "noopener";
  document.body.appendChild(a);
  a.click();
  a.remove();
  window.setTimeout(() => URL.revokeObjectURL(url), 30_000);
  return filename;
}

/** The last hardware report rendered as text (Markdown for chat apps). */
export async function hardwareReportText(hostId: string, format: HwReportFormat, lang: "en" | "vi") {
  const { blob } = await fetchFile(hardwareReportPath(hostId, format, lang));
  return blob.text();
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

  hardware: (id: string, body: { sudoPassword?: string; allowNoRoot?: boolean; sinceDays?: number }) =>
    req<HardwareResult>("POST", `/api/hosts/${id}/hardware`, body),
  lastHardware: (id: string) => req<HardwareResult>("GET", `/api/hosts/${id}/hardware`),
  hardwareFleet: () => req<HardwareFleet>("GET", "/api/hardware"),
  hardwareRunAll: (hostIds?: string[]) => req<HardwareRun>("POST", "/api/hardware/run-all", hostIds ? { hostIds } : {}),
  hardwareRunCancel: () => req<HardwareRun>("POST", "/api/hardware/run-all/cancel"),

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

  /** ~/.ssh/config, or the file the user picked. */
  sshConfig: (path?: string) =>
    path
      ? req<SshConfigImport>("POST", "/api/import/ssh-config/read", { path })
      : req<SshConfigImport>("GET", "/api/import/ssh-config"),
  importSshConfig: (aliases: string[], group: string, path?: string) =>
    req<ImportResult>("POST", "/api/import/ssh-config", { aliases, group, path }),
  /** ~/.ssh/known_hosts, or the file the user picked. */
  knownHosts: (path?: string) =>
    path
      ? req<KnownHostsImport>("POST", "/api/import/known-hosts/read", { path })
      : req<KnownHostsImport>("GET", "/api/import/known-hosts"),
  importKnownHosts: (hosts: { name: string; address: string; port: number; user: string; group: string }[]) =>
    req<ImportResult>("POST", "/api/import/known-hosts", { hosts }),
};

// ------------------------------------------------------------ Termward Pro

export type ChannelKind = "telegram" | "zalo" | "discord" | "slack";

export interface CloudDevice {
  id: string;
  name: string;
  platform: string;
  appVersion: string;
  createdAt?: string;
  lastSeen?: string;
  current: boolean;
}

export interface CloudChannel {
  id: string;
  kind: ChannelKind;
  name: string;
  createdAt?: string;
  lastResult: { ok: boolean; at?: string; error?: string } | null;
}

export interface CloudPlan {
  active: boolean;
  paidUntil?: string;
  maxDevices?: number;
}

export interface CloudPrice {
  months: number;
  amount: number;
  vat: number;
  total: number;
  currency: string;
}

export interface CloudForwarding {
  critical: boolean;
  warnings: boolean;
  recoveries: boolean;
  lang: "vi" | "en";
}

export type OrderStatus = "pending" | "paid" | "cancelled" | "expired";

export interface CloudOrder {
  orderCode: number;
  status?: OrderStatus;
  months?: number;
  amount?: number;
  checkoutUrl?: string;
  expiresAt?: string;
  paidAt?: string;
  plan?: CloudPlan;
}

export interface CloudStatus {
  signedIn: boolean;
  email?: string;
  deviceId?: string;
  deviceName?: string;
  plan?: CloudPlan;
  planInactive: boolean;
  devices: CloudDevice[];
  channels: CloudChannel[];
  prices: CloudPrice[];
  limits?: { alertsPerDay: number; usedToday: number };
  refreshedAt?: string;
  lastError?: string;
  forwarding: CloudForwarding;
  queue: { pending: number; lastSentAt?: string; lastError?: string; retryAt?: string };
  order?: CloudOrder & { startedAt: string; waiting: boolean };
  signedOutReason?: "" | "revoked" | "mismatch" | "key_lost";
  machineSource?: "native" | "system" | "generated";
  server: string;
}

export interface ChannelInput {
  kind: ChannelKind;
  name: string;
  botToken?: string;
  chatId?: string;
  webhookUrl?: string;
}

/** Details of a 409 device_limit answer to sign-in. */
export interface DeviceLimit {
  max: number;
  devices: CloudDevice[];
  replaceToken: string;
}

export const cloudApi = {
  status: () => req<CloudStatus>("GET", "/api/cloud/status"),
  refresh: () => req<CloudStatus>("POST", "/api/cloud/refresh"),
  signInStart: (email: string, lang: string) =>
    req<{ ok: boolean }>("POST", "/api/cloud/signin/start", { email, lang }),
  signInVerify: (email: string, code: string) => req<CloudStatus>("POST", "/api/cloud/signin/verify", { email, code }),
  signInReplace: (replaceToken: string, revokeDeviceId: string) =>
    req<CloudStatus>("POST", "/api/cloud/signin/replace", { replaceToken, revokeDeviceId }),
  signOut: () => req<{ revoked: boolean; status: CloudStatus }>("POST", "/api/cloud/signout"),
  dismissNotice: () => req<CloudStatus>("POST", "/api/cloud/notice/dismiss"),
  checkout: (months: number) =>
    req<{ order: CloudOrder; status: CloudStatus }>("POST", "/api/cloud/checkout", { months }),
  order: (code: number) => req<CloudOrder>("GET", `/api/cloud/orders/${code}`),
  stopWaiting: () => req<CloudStatus>("DELETE", "/api/cloud/order"),
  revokeDevice: (id: string) => req<CloudStatus>("DELETE", `/api/cloud/devices/${encodeURIComponent(id)}`),
  addChannel: (c: ChannelInput) =>
    req<{ channel: CloudChannel; status: CloudStatus }>("POST", "/api/cloud/channels", c),
  deleteChannel: (id: string) => req<CloudStatus>("DELETE", `/api/cloud/channels/${encodeURIComponent(id)}`),
  testChannel: (id: string, lang: string) =>
    req<{ ok: boolean; error?: string }>("POST", `/api/cloud/channels/${encodeURIComponent(id)}/test`, { lang }),
  setForwarding: (f: CloudForwarding) => req<CloudStatus>("PUT", "/api/cloud/forwarding", f),
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
