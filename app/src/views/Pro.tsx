import { useEffect, useRef, useState, type FormEvent } from "react";
import {
  BadgeCheck,
  Check,
  CircleAlert,
  CircleCheck,
  ExternalLink,
  Info,
  LoaderCircle,
  LogOut,
  Mail,
  Plus,
  RefreshCw,
  Send,
  Sparkles,
  Trash2,
} from "lucide-react";
import {
  ApiError,
  cloudApi,
  type ChannelInput,
  type ChannelKind,
  type CloudChannel,
  type CloudDevice,
  type CloudForwarding,
  type CloudPrice,
  type CloudStatus,
  type DeviceLimit,
} from "../lib/api";
import { useT, resolveLang, type Lang, type TKey } from "../lib/i18n";
import { ago } from "../lib/format";
import { confirmAction, errorText, refreshCloud, setCloud, toast, useApp } from "../store";
import { Field, Modal, Segmented, SettingRow, Switch } from "../components/ui";

type T = ReturnType<typeof useT>;

const DEFAULT_SERVER = "https://cloud.nguyenquocanh.io.vn";

/** Shown before the first sign-in; /v1/me prices replace it afterwards. */
const FALLBACK_PRICES: CloudPrice[] = [1, 3, 6, 12].map((m) => ({
  months: m,
  amount: 260000 * m,
  vat: 26000 * m,
  total: 286000 * m,
  currency: "VND",
}));

const KINDS: { kind: ChannelKind; label: string }[] = [
  { kind: "telegram", label: "Telegram" },
  { kind: "zalo", label: "Zalo Bot" },
  { kind: "discord", label: "Discord" },
  { kind: "slack", label: "Slack" },
];
const kindLabel = (k: string) => KINDS.find((x) => x.kind === k)?.label ?? k;

type ChannelField = "botToken" | "chatId" | "webhookUrl";
const isChannelField = (f: string): f is ChannelField => f === "botToken" || f === "chatId" || f === "webhookUrl";

// The same shapes as core/internal/cloud/validate.go, so every wrong field is
// shown at once before anything is sent. Webhooks are checked loosely here:
// the core canonicalises and checks them exactly.
const CHANNEL_RULES: Record<ChannelKind, Partial<Record<ChannelField, RegExp>>> = {
  telegram: {
    botToken: /^\d{5,20}:[A-Za-z0-9_-]{30,80}$/,
    chatId: /^(-?\d{1,20}|@[A-Za-z][A-Za-z0-9_]{3,31})$/,
  },
  zalo: {
    botToken: /^\d{1,30}:[A-Za-z0-9_-]{8,200}$/,
    chatId: /^[A-Za-z0-9_-]{1,64}$/,
  },
  discord: { webhookUrl: /^https:\/\/(discord|discordapp)\.com\/api\/(v\d{1,2}\/)?webhooks\/\d+\/[A-Za-z0-9_-]+/i },
  slack: { webhookUrl: /^https:\/\/hooks\.slack\.com\/services\/T[A-Z0-9]+\/B[A-Z0-9]+\/[A-Za-z0-9]+$/i },
};

function checkChannel(kind: ChannelKind, v: Record<ChannelField, string>): ChannelField[] {
  const rules = CHANNEL_RULES[kind];
  return (Object.keys(rules) as ChannelField[]).filter((f) => !rules[f]!.test(v[f]));
}

const PLATFORMS: Record<string, string> = {
  windows: "Windows",
  macos: "macOS",
  linux: "Linux",
  android: "Android",
  ios: "iOS",
};

/** Server error codes with a translated message. */
const ERR_CODES = new Set([
  "invalid_email",
  "invalid_code",
  "code_expired",
  "too_many_attempts",
  "rate_limited",
  "email_unavailable",
  "cloud_unreachable",
  "secret_store_unavailable",
  "device_mismatch",
  "plan_inactive",
  "channel_limit",
  "invalid_replace_token",
  "payment_unavailable",
  "not_signed_in",
  "device_revoked",
]);

function proError(e: unknown, t: T): string {
  if (e instanceof ApiError && ERR_CODES.has(e.code)) {
    const retry = (e.details as { retryAfter?: number } | undefined)?.retryAfter ?? 60;
    return t(`pro.err.${e.code}` as TKey, { min: Math.max(1, Math.ceil(retry / 60)) });
  }
  return errorText(e);
}

/** The queue's last error: a server/core code (translated when known) or a channel's short reason. */
function queueErrorText(err: string, t: T): string {
  if (err === "plan_inactive") return t("pro.queuePaused");
  if (ERR_CODES.has(err)) return t("pro.queueError", { err: t(`pro.err.${err}` as TKey, { min: 1 }) });
  return t("pro.queueError", { err });
}

export function vnd(n: number, lang: Lang): string {
  return `${new Intl.NumberFormat(lang === "vi" ? "vi-VN" : "en-US").format(n)} ₫`;
}

function day(iso: string | undefined, lang: Lang): string {
  if (!iso) return "—";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "—";
  // 08/01/2027 reads as 8 January in Vietnam but as August elsewhere: spell the month out in English.
  return lang === "vi"
    ? d.toLocaleDateString("vi-VN", { day: "2-digit", month: "2-digit", year: "numeric" })
    : d.toLocaleDateString("en-GB", { day: "numeric", month: "short", year: "numeric" });
}

/** A channel's delivery error without the "telegram: " prefix the kind badge already shows. */
function channelError(err: string | undefined): string {
  return (err ?? "").replace(/^(telegram|zalo|discord|slack):\s*/i, "");
}

const LAST_EMAIL = "termward.pro.email";

function lastEmail(): string {
  try {
    return localStorage.getItem(LAST_EMAIL) ?? "";
  } catch {
    return "";
  }
}

function rememberEmail(email: string) {
  try {
    localStorage.setItem(LAST_EMAIL, email);
  } catch {
    /* private window: nothing to remember */
  }
}

/** "3 minutes ago", fit to follow a word ("Delivered just now", "Hoạt động hôm kia"). */
function since(iso: string | undefined, t: T): string {
  const ms = iso ? Date.now() - Date.parse(iso) : NaN;
  if (ms >= 0 && ms < 45_000) return t("pro.justNow");
  const s = ago(iso);
  return s.charAt(0).toLowerCase() + s.slice(1);
}

/** The core's last refresh error, translated when it is a network problem. */
function refreshErrorText(err: string, t: T): string {
  if (/reach|in time|unreachable/i.test(err)) return t("pro.err.cloud_unreachable");
  return t("pro.refreshFailed", { err });
}

function openExternal(url: string) {
  if (window.termward) window.termward.openExternal(url);
  else window.open(url, "_blank", "noopener");
}

function useLang(): Lang {
  return resolveLang(useApp((s) => s.lang));
}

/** "286.000 ₫/tháng (đã gồm VAT 10%)" from the 1-month price. */
function monthlyPrice(prices: CloudPrice[], lang: Lang, t: T): string {
  const p = prices.find((x) => x.months === 1) ?? FALLBACK_PRICES[0];
  const vat = p.amount > 0 ? Math.round((p.vat / p.amount) * 100) : 10;
  return t("pro.price", { price: vnd(p.total, lang), vat });
}

// ------------------------------------------------------------ section

/** The "Termward Pro" section of Settings. */
export function ProSection() {
  const t = useT();
  const cloud = useApp((s) => s.cloud);
  const view = useApp((s) => s.view);
  const ref = useRef<HTMLHeadingElement>(null);

  useEffect(() => {
    if (view.name === "settings" && view.section === "pro") {
      ref.current?.scrollIntoView({ behavior: "smooth", block: "start" });
    }
  }, [view]);

  if (!cloud) return null;

  return (
    <>
      <h2 className="section" id="pro" ref={ref}>
        <Sparkles size={17} style={{ color: "var(--accent)" }} />
        {t("pro.title")}
      </h2>
      {cloud.signedOutReason && <SignedOutNotice reason={cloud.signedOutReason} />}
      {cloud.signedIn ? <Account cloud={cloud} /> : <SignedOut cloud={cloud} />}
    </>
  );
}

function SignedOutNotice({ reason }: { reason: string }) {
  const t = useT();
  const key = `pro.reason.${reason}` as TKey;
  return (
    <div className="callout warn" style={{ marginBottom: 12 }}>
      <CircleAlert size={16} style={{ color: "var(--warn)" }} />
      <div className="grow">{t(key)}</div>
      <button
        className="btn sm ghost"
        onClick={() =>
          void cloudApi
            .dismissNotice()
            .then(setCloud)
            .catch(() => undefined)
        }
      >
        {t("pro.dismiss")}
      </button>
    </div>
  );
}

// ------------------------------------------------------------ signed out

function SignedOut({ cloud }: { cloud: CloudStatus }) {
  const t = useT();
  const lang = useLang();
  const prices = cloud.prices.length ? cloud.prices : FALLBACK_PRICES;
  return (
    <div className="card pro-hero">
      <div className="pro-hero-main">
        <div className="pro-tagline">{t("pro.tagline")}</div>
        <p className="muted pro-pitch">{t("pro.pitch")}</p>
        <ul className="pro-features">
          {(["pro.feature1", "pro.feature2", "pro.feature3", "pro.feature4"] as const).map((k) => (
            <li key={k}>
              <Check size={15} />
              {t(k, { n: 3 })}
            </li>
          ))}
        </ul>
        <div className="pro-price">{monthlyPrice(prices, lang, t)}</div>
      </div>
      <div className="pro-hero-side">
        <div>
          <div className="pro-signin-title">{t("pro.signInTitle")}</div>
          <SignIn />
        </div>
      </div>
    </div>
  );
}

function SignIn() {
  const t = useT();
  const lang = useLang();
  const [step, setStep] = useState<"email" | "code">("email");
  const [email, setEmail] = useState(lastEmail);
  const [code, setCode] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  // The code can no longer work (expired, or too many wrong tries): the main
  // button sends a new one instead of trying again.
  const [needNew, setNeedNew] = useState(false);
  // The device-limit answer used the code up; its replace token lives 10
  // minutes, so closing the dialog keeps it and "Sign in" opens it again.
  const [limit, setLimit] = useState<{ info: DeviceLimit; at: number } | null>(null);
  const [limitOpen, setLimitOpen] = useState(false);
  const [cooldown, setCooldown] = useState(0);
  const limitExpired = () => {
    setLimit(null);
    setLimitOpen(false);
    setNeedNew(true);
    setError(t("pro.err.invalid_replace_token"));
  };
  const codeRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (step === "code") codeRef.current?.focus();
  }, [step]);

  // A new code can be asked for again after 30 s, so a double click or an
  // impatient user does not run into the server's rate limit.
  useEffect(() => {
    if (cooldown <= 0) return;
    const id = window.setTimeout(() => setCooldown((c) => c - 1), 1000);
    return () => window.clearTimeout(id);
  }, [cooldown]);

  const send = async (e?: FormEvent, again = false) => {
    e?.preventDefault();
    setBusy(true);
    setError("");
    try {
      await cloudApi.signInStart(email.trim(), lang);
      setStep("code");
      setCooldown(30);
      setNeedNew(false);
      setLimit(null);
      if (again) {
        setCode("");
        toast("info", t("pro.codeResent"));
        codeRef.current?.focus();
      }
    } catch (err) {
      setError(proError(err, t));
    } finally {
      setBusy(false);
    }
  };

  const verify = async (e: FormEvent) => {
    e.preventDefault();
    if (needNew) {
      if (cooldown <= 0) void send(undefined, true);
      return;
    }
    if (limit) {
      if (Date.now() - limit.at < 9 * 60_000) setLimitOpen(true);
      else limitExpired();
      return;
    }
    setBusy(true);
    setError("");
    try {
      setCloud(await cloudApi.signInVerify(email.trim(), code));
      rememberEmail(email.trim().toLowerCase());
      toast("success", t("pro.signedInAs", { email: email.trim().toLowerCase() }));
    } catch (err) {
      if (err instanceof ApiError && err.code === "device_limit" && err.details) {
        rememberEmail(email.trim().toLowerCase());
        setLimit({ info: err.details as DeviceLimit, at: Date.now() });
        setLimitOpen(true);
      } else {
        setError(proError(err, t));
        if (err instanceof ApiError && (err.code === "code_expired" || err.code === "too_many_attempts")) {
          setNeedNew(true);
        } else {
          // Select the wrong code so typing the right one replaces it.
          window.setTimeout(() => codeRef.current?.select(), 0);
        }
      }
    } finally {
      setBusy(false);
    }
  };

  // "We sent a code to <b>a@b.vn</b>…" with the address in bold.
  const [sentBefore, sentAfter] = t("pro.codeSent", { email: "\u0000" }).split("\u0000");

  if (step === "email") {
    return (
      <form className="stack pro-signin" onSubmit={(e) => void send(e)}>
        <Field label={t("pro.emailLabel")} hint={t("pro.signInHint")} error={error || undefined}>
          <input
            className="input"
            type="email"
            autoComplete="email"
            inputMode="email"
            placeholder={t("pro.emailPlaceholder")}
            value={email}
            onChange={(e) => {
              setEmail(e.target.value);
              setError("");
            }}
          />
        </Field>
        <button className="btn primary" type="submit" disabled={busy || !email.includes("@")}>
          {busy ? <LoaderCircle size={14} className="spin" /> : <Mail size={14} />}
          {t("pro.sendCode")}
        </button>
      </form>
    );
  }

  return (
    <>
      <form className="stack pro-signin" onSubmit={(e) => void verify(e)}>
        <p className="muted" style={{ margin: 0, fontSize: 13, overflowWrap: "anywhere" }}>
          {sentBefore}
          <strong style={{ color: "var(--text)" }}>{email.trim()}</strong>
          {sentAfter}
        </p>
        <Field label={t("pro.codeLabel")} error={error || undefined}>
          <input
            ref={codeRef}
            className="input mono pro-code"
            inputMode="numeric"
            autoComplete="one-time-code"
            maxLength={7}
            placeholder="••••••"
            aria-label={t("pro.codeLabel")}
            value={code}
            onChange={(e) => {
              setCode(e.target.value.replace(/[^\d]/g, "").slice(0, 6));
              setError("");
              setNeedNew(false);
            }}
          />
        </Field>
        {needNew ? (
          <button className="btn primary" type="submit" disabled={busy || cooldown > 0}>
            {busy ? <LoaderCircle size={14} className="spin" /> : <Mail size={14} />}
            {cooldown > 0 ? t("pro.resendIn", { s: cooldown }) : t("pro.resend")}
          </button>
        ) : limit ? (
          <button className="btn primary" type="submit">
            {t("pro.limitPick")}
          </button>
        ) : (
          <button className="btn primary" type="submit" disabled={busy || code.length !== 6}>
            {busy && <LoaderCircle size={14} className="spin" />}
            {t("pro.verify")}
          </button>
        )}
        <div className="row" style={{ gap: 4, flexWrap: "wrap" }}>
          <button
            type="button"
            className="btn sm ghost"
            onClick={() => {
              setStep("email");
              setCode("");
              setError("");
              setNeedNew(false);
              setLimit(null);
              setCooldown(0);
            }}
          >
            {t("pro.otherEmail")}
          </button>
          {!needNew && (
            <button
              type="button"
              className="btn sm ghost"
              disabled={busy || cooldown > 0}
              onClick={() => void send(undefined, true)}
            >
              {cooldown > 0 ? t("pro.resendIn", { s: cooldown }) : t("pro.resend")}
            </button>
          )}
        </div>
      </form>
      {limit && limitOpen && (
        <DeviceLimitDialog limit={limit.info} onClose={() => setLimitOpen(false)} onExpired={limitExpired} />
      )}
    </>
  );
}

/** The device unused for longest: the safest one to suggest signing out. */
function stalest(devices: CloudDevice[]): string {
  const seen = (d: CloudDevice) => (d.lastSeen ? Date.parse(d.lastSeen) || 0 : 0);
  return [...devices].sort((a, b) => seen(a) - seen(b))[0]?.id ?? "";
}

function DeviceLimitDialog({
  limit,
  onClose,
  onExpired,
}: {
  limit: DeviceLimit;
  onClose: () => void;
  /** The replace token is used up or too old: a new code is needed. */
  onExpired: () => void;
}) {
  const t = useT();
  const [pick, setPick] = useState(() => stalest(limit.devices));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const replace = async () => {
    setBusy(true);
    setError("");
    try {
      const st = await cloudApi.signInReplace(limit.replaceToken, pick);
      setCloud(st);
      onClose();
      toast("success", t("pro.signedInAs", { email: st.email ?? "" }));
    } catch (e) {
      if (e instanceof ApiError && e.code === "invalid_replace_token") onExpired();
      else setError(proError(e, t));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal
      title={t("pro.limitTitle", { n: limit.max })}
      subtitle={t("pro.limitBody")}
      onClose={onClose}
      footer={
        <>
          <button className="btn" onClick={onClose}>
            {t("common.cancel")}
          </button>
          <button className="btn primary" disabled={!pick || busy} onClick={() => void replace()}>
            {busy && <LoaderCircle size={14} className="spin" />}
            {t("pro.limitReplace")}
          </button>
        </>
      }
    >
      <div className="stack" style={{ gap: 8 }}>
        {limit.devices.map((d) => (
          <label key={d.id} className={`pro-pick${pick === d.id ? " on" : ""}`}>
            <input type="radio" name="device" checked={pick === d.id} onChange={() => setPick(d.id)} />
            <DeviceText d={d} />
          </label>
        ))}
        {error && <div className="error-text">{error}</div>}
      </div>
    </Modal>
  );
}

function DeviceText({ d, current = false }: { d: CloudDevice; current?: boolean }) {
  const t = useT();
  return (
    <div style={{ minWidth: 0, flex: 1 }}>
      <div className="truncate" style={{ fontWeight: 540 }}>
        {d.name || PLATFORMS[d.platform] || d.platform}
      </div>
      <div className="faint" style={{ fontSize: 12 }}>
        {current && <span className="badge accent pro-this">{t("pro.thisDevice")}</span>}
        {[
          PLATFORMS[d.platform] ?? d.platform,
          d.appVersion && (/^v?\d/.test(d.appVersion) ? `v${d.appVersion.replace(/^v/, "")}` : d.appVersion),
          d.lastSeen && t("pro.lastSeen", { ago: since(d.lastSeen, t) }),
        ]
          .filter(Boolean)
          .join(" · ")}
      </div>
    </div>
  );
}

// ------------------------------------------------------------ signed in

function Account({ cloud }: { cloud: CloudStatus }) {
  const t = useT();
  const [refreshing, setRefreshing] = useState(false);

  /** quiet: the warning callout already says what is wrong, so no toast repeats it. */
  const refresh = async (quiet = false) => {
    setRefreshing(true);
    const started = Date.now();
    try {
      setCloud(await cloudApi.refresh());
    } catch (e) {
      // A refused connection fails at once: keep the spinner long enough to see that something was tried.
      await new Promise((r) => window.setTimeout(r, Math.max(0, 600 - (Date.now() - started))));
      if (quiet) await refreshCloud();
      else toast("error", t("pro.title"), proError(e, t));
    } finally {
      setRefreshing(false);
    }
  };

  const signOut = () =>
    confirmAction({
      title: t("pro.signOutTitle"),
      body: t("pro.signOutBody"),
      confirm: t("pro.signOut"),
      danger: true,
      run: async () => {
        try {
          const r = await cloudApi.signOut();
          setCloud(r.status);
          toast(r.revoked ? "success" : "info", t("pro.signedOut"), r.revoked ? undefined : t("pro.signedOutOffline"));
        } catch (e) {
          toast("error", t("pro.title"), proError(e, t));
        }
      },
    });

  return (
    <div className="stack" style={{ gap: 14 }}>
      {cloud.lastError && (
        <div className="callout warn">
          <CircleAlert size={16} style={{ color: "var(--warn)" }} />
          <div className="grow" style={{ minWidth: 0 }}>
            <strong>{refreshErrorText(cloud.lastError, t)}</strong>
            <div className="muted">
              {cloud.refreshedAt ? t("pro.staleSince", { ago: since(cloud.refreshedAt, t) }) : t("pro.stale")}
            </div>
          </div>
          <button className="btn sm" disabled={refreshing} onClick={() => void refresh(true)}>
            <RefreshCw size={13} className={refreshing ? "spin" : undefined} />
            {t("pro.retry")}
          </button>
        </div>
      )}
      <Plan cloud={cloud} />
      <Channels cloud={cloud} />
      <Forwarding cloud={cloud} />
      <Devices cloud={cloud} />
      <div className="pro-account-foot">
        <div className="grow" style={{ minWidth: 0 }}>
          <div className="truncate">{t("pro.signedInAs", { email: cloud.email ?? "" })}</div>
          <div className="faint" style={{ fontSize: 12 }}>
            {cloud.refreshedAt && t("pro.updated", { ago: since(cloud.refreshedAt, t) })}
            {cloud.server && cloud.server !== DEFAULT_SERVER && <> · {t("pro.server", { url: cloud.server })}</>}
          </div>
        </div>
        <button className="btn sm ghost" disabled={refreshing} onClick={() => void refresh()}>
          <RefreshCw size={13} className={refreshing ? "spin" : undefined} />
          {t("pro.refresh")}
        </button>
        <button className="btn sm danger" onClick={signOut}>
          <LogOut size={13} />
          {t("pro.signOut")}
        </button>
      </div>
    </div>
  );
}

function Plan({ cloud }: { cloud: CloudStatus }) {
  const t = useT();
  const lang = useLang();
  const prices = cloud.prices.length ? cloud.prices : FALLBACK_PRICES;
  const plan = cloud.plan;
  const active = !!plan?.active;
  const [busy, setBusy] = useState<number | null>(null);
  const order = cloud.order;
  const waiting = !!order?.waiting;
  // With a plan running, the packs stay folded behind "Extend" so the
  // channels are not pushed off the screen; without one, they are the point.
  const [showPacks, setShowPacks] = useState(false);
  const packsOpen = !active || showPacks || (!!order && order.status !== "paid");

  const buy = async (months: number) => {
    setBusy(months);
    try {
      const r = await cloudApi.checkout(months);
      setCloud(r.status);
      if (r.order.checkoutUrl) openExternal(r.order.checkoutUrl);
    } catch (e) {
      toast("error", t("pro.buyTitle"), proError(e, t));
    } finally {
      setBusy(null);
    }
  };

  return (
    <div className="card card-pad stack" style={{ gap: 14 }}>
      <div className="pro-plan-head">
        <div className="grow" style={{ minWidth: 0 }}>
          <div className="row" style={{ gap: 8, flexWrap: "wrap" }}>
            <strong style={{ fontSize: 15 }}>Termward Pro</strong>
            {active ? (
              <span className="badge ok">
                <BadgeCheck size={12} />
                {t("pro.planActive")}
              </span>
            ) : plan?.paidUntil ? (
              <span className="badge warn">{t("pro.planExpired")}</span>
            ) : (
              <span className="badge">{t("pro.planNone")}</span>
            )}
          </div>
          <div className="muted" style={{ fontSize: 13, marginTop: 2 }}>
            {active
              ? t("pro.activeUntil", { date: day(plan?.paidUntil, lang) })
              : plan?.paidUntil
                ? t("pro.expiredOn", { date: day(plan.paidUntil, lang) })
                : t("pro.notActive")}
          </div>
          {cloud.limits && active && (
            <div className="faint" style={{ fontSize: 12, marginTop: 2 }}>
              {t("pro.usage", { used: cloud.limits.usedToday, limit: cloud.limits.alertsPerDay })}
            </div>
          )}
        </div>
        {!packsOpen && (
          <button className="btn sm" onClick={() => setShowPacks(true)}>
            {t("pro.extend")}
          </button>
        )}
      </div>

      {order && <OrderBanner cloud={cloud} />}

      {packsOpen && (
        <div>
          <div className="pro-packs-head">
            <span className="field-label">{active || plan?.paidUntil ? t("pro.extendTitle") : t("pro.buyTitle")}</span>
            <span className="faint">{monthlyPrice(prices, lang, t)}</span>
          </div>
          <div className="pro-packs">
            {[1, 3, 6, 12].map((m) => {
              const p = prices.find((x) => x.months === m) ?? FALLBACK_PRICES.find((x) => x.months === m)!;
              const base = prices.find((x) => x.months === 1) ?? FALLBACK_PRICES[0];
              const perMonth = Math.round(p.total / m);
              return (
                <button key={m} className="pro-pack" disabled={busy !== null || waiting} onClick={() => void buy(m)}>
                  <span className="m">{m === 1 ? t("pro.month1") : t("pro.months", { n: m })}</span>
                  <span className="v">{vnd(p.total, lang)}</span>
                  <span className="s">
                    {busy === m ? (
                      <LoaderCircle size={12} className="spin" />
                    ) : perMonth < base.total ? (
                      t("pro.perMonth", { price: vnd(perMonth, lang) })
                    ) : (
                      t("pro.inclVat", { vat: vnd(p.vat, lang) })
                    )}
                  </span>
                </button>
              );
            })}
          </div>
          <p className="faint" style={{ fontSize: 12, margin: "8px 0 0" }}>
            {t("pro.payNote")}
          </p>
        </div>
      )}
    </div>
  );
}

function OrderBanner({ cloud }: { cloud: CloudStatus }) {
  const t = useT();
  const lang = useLang();
  const o = cloud.order!;
  const amount = o.amount || (cloud.prices.find((p) => p.months === o.months)?.total ?? 0);
  const [checking, setChecking] = useState(false);
  // PayOS links live 30 minutes: once that has passed, reopening one only shows an error page.
  const expires = o.expiresAt ? Date.parse(o.expiresAt) : NaN;
  const linkOpen = o.waiting && !(expires < Date.now());
  const dismiss = () =>
    void cloudApi
      .stopWaiting()
      .then(setCloud)
      .catch((e: unknown) => toast("error", t("pro.title"), proError(e, t)));
  const check = async () => {
    setChecking(true);
    try {
      const r = await cloudApi.order(o.orderCode);
      if (!r.status || r.status === "pending") toast("info", t("pro.notPaidYet"));
    } catch (e) {
      toast("error", t("pro.title"), proError(e, t));
    } finally {
      setChecking(false);
    }
  };

  if (o.status === "paid") {
    return (
      <div className="callout pro-paid">
        <CircleCheck size={16} style={{ color: "var(--ok)" }} />
        <div className="grow">
          <strong>{t("pro.paid")}</strong>
          {cloud.plan?.active && (
            <div className="muted">
              {t("pro.paidBody", { date: day(cloud.plan.paidUntil, lang) })}
              {cloud.channels.length === 0 && ` ${t("pro.paidNext")}`}
            </div>
          )}
        </div>
        <button className="btn sm" onClick={dismiss}>
          {t("pro.dismiss")}
        </button>
      </div>
    );
  }
  if (o.status === "cancelled" || o.status === "expired") {
    return (
      <div className="callout">
        <Info size={16} />
        <div className="grow">
          <strong>{t(o.status === "cancelled" ? "pro.cancelled" : "pro.expired")}</strong>
          <div className="muted">{t("pro.pickAgain")}</div>
        </div>
        <button className="btn sm" onClick={dismiss}>
          {t("pro.dismiss")}
        </button>
      </div>
    );
  }
  return (
    <div className="callout info">
      {o.waiting ? <LoaderCircle size={16} className="spin" /> : <Info size={16} />}
      <div className="grow">
        <strong>{o.waiting ? t("pro.waiting") : t("pro.timedOut")}</strong>
        {o.months && amount > 0 ? (
          <div className="muted">
            {t("pro.orderPack", {
              pack: o.months === 1 ? t("pro.month1") : t("pro.months", { n: o.months }),
              price: vnd(amount, lang),
            })}
          </div>
        ) : null}
        <div className="muted">{t(o.waiting ? "pro.waitingBody" : "pro.timedOutBody")}</div>
        <div className="row" style={{ gap: 6, marginTop: 8, flexWrap: "wrap" }}>
          {o.checkoutUrl && linkOpen && (
            <button className="btn sm" onClick={() => openExternal(o.checkoutUrl!)}>
              <ExternalLink size={13} />
              {t("pro.reopen")}
            </button>
          )}
          <button className={`btn sm${linkOpen ? " ghost" : ""}`} disabled={checking} onClick={() => void check()}>
            {checking && <LoaderCircle size={13} className="spin" />}
            {t("pro.checkNow")}
          </button>
          {o.waiting ? (
            <button className="btn sm ghost" title={t("pro.stopWaitingHint")} onClick={dismiss}>
              {t("pro.stopWaiting")}
            </button>
          ) : (
            <button className="btn sm ghost" onClick={dismiss}>
              {t("pro.dismiss")}
            </button>
          )}
        </div>
      </div>
    </div>
  );
}

// ------------------------------------------------------------ channels

function Channels({ cloud }: { cloud: CloudStatus }) {
  const t = useT();
  const lang = useLang();
  const [adding, setAdding] = useState(false);
  const [testing, setTesting] = useState<string | null>(null);
  const canAdd = !!cloud.plan?.active && !cloud.planInactive;

  const test = async (c: CloudChannel) => {
    setTesting(c.id);
    try {
      const r = await cloudApi.testChannel(c.id, lang);
      if (r.ok) toast("success", t("pro.testOk", { name: c.name }));
      else toast("error", t("pro.testFailed", { name: c.name }), channelError(r.error));
    } catch (e) {
      toast("error", t("pro.testFailed", { name: c.name }), proError(e, t));
    } finally {
      setTesting(null);
    }
  };

  const remove = (c: CloudChannel) =>
    confirmAction({
      title: t("pro.deleteChannelTitle", { name: c.name }),
      body: t("pro.deleteChannelBody"),
      confirm: t("common.delete"),
      danger: true,
      run: async () => {
        try {
          setCloud(await cloudApi.deleteChannel(c.id));
        } catch (e) {
          toast("error", t("pro.channels"), proError(e, t));
        }
      },
    });

  return (
    <div className="card">
      <div className="pro-card-head">
        <div className="grow">
          <strong>{t("pro.channels")}</strong>
          <div className="muted" style={{ fontSize: 12.5 }}>
            {t("pro.channelsHint")}
          </div>
        </div>
        <button
          className="btn sm"
          disabled={!canAdd || cloud.channels.length >= 10}
          title={canAdd && cloud.channels.length >= 10 ? t("pro.err.channel_limit") : undefined}
          onClick={() => setAdding(true)}
        >
          <Plus size={14} />
          {t("pro.addChannel")}
        </button>
      </div>
      {!canAdd && (
        <div className="pro-row muted" style={{ fontSize: 13 }}>
          <Info size={14} style={{ flexShrink: 0 }} />
          {cloud.channels.length > 0 ? t("pro.channelsPaused") : t("pro.needPlan")}
        </div>
      )}
      {cloud.channels.length === 0 && canAdd && (
        <div className="pro-row muted" style={{ fontSize: 13 }}>
          {t("pro.noChannels")}
        </div>
      )}
      {cloud.channels.map((c) => (
        <div key={c.id} className="pro-row">
          <span className={`pro-kind ${c.kind}`}>{kindLabel(c.kind)}</span>
          <div className="grow" style={{ minWidth: 0 }}>
            <div className="truncate" style={{ fontWeight: 540 }}>
              {c.name}
            </div>
            <div className="faint" style={{ fontSize: 12 }}>
              {c.lastResult ? (
                c.lastResult.ok ? (
                  t("pro.lastOk", { ago: since(c.lastResult.at, t) })
                ) : (
                  <span style={{ color: "var(--crit)", overflowWrap: "anywhere" }}>
                    {t("pro.lastFail", { ago: since(c.lastResult.at, t), err: channelError(c.lastResult.error) })}
                  </span>
                )
              ) : (
                t("pro.neverUsed")
              )}
            </div>
          </div>
          <button
            className="btn sm"
            title={t("pro.test")}
            aria-label={t("pro.test")}
            disabled={testing !== null || !canAdd}
            onClick={() => void test(c)}
          >
            {testing === c.id ? <LoaderCircle size={13} className="spin" /> : <Send size={13} />}
            <span className="desktop-only">{t("pro.test")}</span>
          </button>
          <button
            className="icon-btn"
            title={t("common.delete")}
            aria-label={t("common.delete")}
            onClick={() => remove(c)}
          >
            <Trash2 size={15} />
          </button>
        </div>
      ))}
      {adding && <ChannelDialog onClose={() => setAdding(false)} />}
    </div>
  );
}

function ChannelDialog({ onClose }: { onClose: () => void }) {
  const t = useT();
  const [kind, setKind] = useState<ChannelKind>("telegram");
  const [name, setName] = useState("");
  const [botToken, setBotToken] = useState("");
  const [chatId, setChatId] = useState("");
  const [webhookUrl, setWebhookUrl] = useState("");
  const [busy, setBusy] = useState(false);
  const [fields, setFields] = useState<Partial<Record<ChannelField, string>>>({});
  const [error, setError] = useState("");
  const bot = kind === "telegram" || kind === "zalo";
  const ready = bot ? botToken.trim() !== "" && chatId.trim() !== "" : webhookUrl.trim() !== "";

  const fieldError = (f: ChannelField) => fields[f];
  const clearField = (f: ChannelField) =>
    setFields((prev) => {
      const next = { ...prev };
      delete next[f];
      return next;
    });

  const submit = async (e?: FormEvent) => {
    e?.preventDefault();
    if (!ready) return;
    // Point at every wrong field at once; the core checks again, exactly.
    const bad = checkChannel(kind, { botToken: botToken.trim(), chatId: chatId.trim(), webhookUrl: webhookUrl.trim() });
    if (bad.length) {
      setFields(Object.fromEntries(bad.map((f) => [f, t(`ch.err.${kind}.${f}` as TKey)])));
      return;
    }
    setBusy(true);
    setFields({});
    setError("");
    const input: ChannelInput = bot
      ? { kind, name: name.trim(), botToken: botToken.trim(), chatId: chatId.trim() }
      : { kind, name: name.trim(), webhookUrl: webhookUrl.trim() };
    try {
      const r = await cloudApi.addChannel(input);
      setCloud(r.status);
      toast("success", t("pro.channelAdded", { name: r.channel.name }), t("pro.channelAddedHint"));
      onClose();
    } catch (err) {
      const f = err instanceof ApiError ? (err.details as { field?: string } | undefined)?.field : undefined;
      if (err instanceof ApiError && err.code === "invalid_config" && f && isChannelField(f)) {
        setFields({ [f]: t(`ch.err.${kind}.${f}` as TKey) });
      } else {
        setError(proError(err, t));
      }
    } finally {
      setBusy(false);
    }
  };

  const steps = t(`ch.help.${kind}` as TKey).split("\n");

  return (
    <Modal
      title={t("ch.title")}
      onClose={onClose}
      footer={
        <>
          <button className="btn" onClick={onClose}>
            {t("common.cancel")}
          </button>
          <button className="btn primary" disabled={!ready || busy} onClick={() => void submit()}>
            {busy && <LoaderCircle size={14} className="spin" />}
            {t("ch.add")}
          </button>
        </>
      }
    >
      <form className="stack" style={{ gap: 14 }} onSubmit={(e) => void submit(e)}>
        <Field label={t("ch.kind")}>
          <Segmented<ChannelKind>
            value={kind}
            onChange={(k) => {
              // Tokens and webhooks of one service never fit another: start clean.
              if (k !== kind) {
                setBotToken("");
                setChatId("");
                setWebhookUrl("");
              }
              setKind(k);
              setFields({});
              setError("");
            }}
            options={KINDS.map((k) => ({ value: k.kind, label: k.label }))}
          />
        </Field>
        <Field label={t("ch.name")} hint={t("common.optional")}>
          <input
            className="input"
            maxLength={64}
            placeholder={t("ch.namePlaceholder")}
            value={name}
            onChange={(e) => setName(e.target.value)}
          />
        </Field>
        {bot ? (
          <>
            <Field label={t("ch.botToken")} error={fieldError("botToken")}>
              <input
                className="input mono"
                autoComplete="off"
                spellCheck={false}
                placeholder={kind === "telegram" ? "123456789:AAH…" : "1234567890:abc…"}
                value={botToken}
                onChange={(e) => {
                  setBotToken(e.target.value);
                  clearField("botToken");
                }}
              />
            </Field>
            <Field label={t("ch.chatId")} error={fieldError("chatId")}>
              <input
                className="input mono"
                autoComplete="off"
                spellCheck={false}
                placeholder={kind === "telegram" ? "-1001234567890" : "a1b2c3d4"}
                value={chatId}
                onChange={(e) => {
                  setChatId(e.target.value);
                  clearField("chatId");
                }}
              />
            </Field>
          </>
        ) : (
          <Field label={t("ch.webhookUrl")} error={fieldError("webhookUrl")}>
            <input
              className="input mono"
              autoComplete="off"
              spellCheck={false}
              inputMode="url"
              placeholder={
                kind === "discord" ? "https://discord.com/api/webhooks/…" : "https://hooks.slack.com/services/…"
              }
              value={webhookUrl}
              onChange={(e) => {
                setWebhookUrl(e.target.value);
                clearField("webhookUrl");
              }}
            />
          </Field>
        )}
        <details className="pro-help" key={kind}>
          <summary>{t("ch.help")}</summary>
          <ol>
            {steps.map((s) => (
              <li key={s}>{s}</li>
            ))}
          </ol>
        </details>
        <p className="faint" style={{ fontSize: 12, margin: 0 }}>
          {t("ch.secretNote")}
        </p>
        {error && <div className="error-text">{error}</div>}
        <button type="submit" hidden />
      </form>
    </Modal>
  );
}

// ------------------------------------------------------------ forwarding

function Forwarding({ cloud }: { cloud: CloudStatus }) {
  const t = useT();
  const f = cloud.forwarding;
  const save = async (next: CloudForwarding) => {
    setCloud({ ...cloud, forwarding: next }); // optimistic
    try {
      setCloud(await cloudApi.setForwarding(next));
    } catch (e) {
      setCloud(cloud);
      toast("error", t("pro.forwarding"), proError(e, t));
    }
  };
  const q = cloud.queue;
  return (
    <div className="card">
      <div className="pro-card-head">
        <div className="grow">
          <strong>{t("pro.forwarding")}</strong>
          <div className="muted" style={{ fontSize: 12.5 }}>
            {t("pro.fwHint")}
          </div>
          {q.pending > 0 && (
            <div style={{ fontSize: 12.5, color: "var(--warn)", marginTop: 4 }}>
              {t("pro.queued", { n: q.pending }) + (q.lastError ? ` · ${queueErrorText(q.lastError, t)}` : "")}
            </div>
          )}
        </div>
      </div>
      <SettingRow title={t("pro.fwCritical")} hint={t("pro.fwCriticalHint")}>
        <Switch label={t("pro.fwCritical")} on={f.critical} onChange={(v) => void save({ ...f, critical: v })} />
      </SettingRow>
      <SettingRow title={t("pro.fwWarnings")} hint={t("pro.fwWarningsHint")}>
        <Switch label={t("pro.fwWarnings")} on={f.warnings} onChange={(v) => void save({ ...f, warnings: v })} />
      </SettingRow>
      <SettingRow title={t("pro.fwRecoveries")} hint={t("pro.fwRecoveriesHint")}>
        <Switch label={t("pro.fwRecoveries")} on={f.recoveries} onChange={(v) => void save({ ...f, recoveries: v })} />
      </SettingRow>
      <SettingRow title={t("pro.fwLang")}>
        <Segmented<"vi" | "en">
          value={f.lang}
          onChange={(v) => void save({ ...f, lang: v })}
          options={[
            { value: "vi", label: "Tiếng Việt" },
            { value: "en", label: "English" },
          ]}
        />
      </SettingRow>
      <p className="faint pro-foot-note">{t("pro.fwMuted")}</p>
    </div>
  );
}

// ------------------------------------------------------------ devices

function Devices({ cloud }: { cloud: CloudStatus }) {
  const t = useT();
  const max = cloud.plan?.maxDevices ?? 3;

  const revoke = (d: CloudDevice) =>
    confirmAction({
      title: d.current ? t("pro.signOutTitle") : t("pro.revokeTitle", { name: d.name }),
      body: d.current ? t("pro.signOutBody") : t("pro.revokeBody"),
      confirm: t("pro.revoke"),
      danger: true,
      run: async () => {
        try {
          setCloud(await cloudApi.revokeDevice(d.id));
        } catch (e) {
          toast("error", t("pro.devices"), proError(e, t));
        }
      },
    });

  return (
    <div className="card">
      <div className="pro-card-head">
        <div className="grow">
          <strong>{t("pro.devices")}</strong>{" "}
          <span className="faint" style={{ fontSize: 12.5 }}>
            {t("pro.devicesCount", { n: cloud.devices.length, max })}
          </span>
          {cloud.machineSource === "generated" && (
            <div className="faint" style={{ fontSize: 12 }}>
              {t("pro.machineGenerated")}
            </div>
          )}
        </div>
      </div>
      {cloud.devices.map((d) => (
        <div key={d.id} className="pro-row">
          <DeviceText d={d} current={d.current} />
          <button className="btn sm ghost" onClick={() => revoke(d)}>
            {t("pro.revoke")}
          </button>
        </div>
      ))}
    </div>
  );
}
