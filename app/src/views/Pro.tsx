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
import { confirmAction, errorText, setCloud, toast, useApp } from "../store";
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
  return d.toLocaleDateString(lang === "vi" ? "vi-VN" : "en-GB", { day: "2-digit", month: "2-digit", year: "numeric" });
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
      <button className="btn sm ghost" onClick={() => void cloudApi.dismissNotice().then(setCloud)}>
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
        <SignIn />
      </div>
    </div>
  );
}

function SignIn() {
  const t = useT();
  const lang = useLang();
  const [step, setStep] = useState<"email" | "code">("email");
  const [email, setEmail] = useState("");
  const [code, setCode] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [limit, setLimit] = useState<DeviceLimit | null>(null);
  const [cooldown, setCooldown] = useState(0);
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
    setBusy(true);
    setError("");
    try {
      setCloud(await cloudApi.signInVerify(email.trim(), code));
      toast("success", t("pro.signedInAs", { email: email.trim().toLowerCase() }));
    } catch (err) {
      if (err instanceof ApiError && err.code === "device_limit" && err.details) {
        setLimit(err.details as DeviceLimit);
      } else {
        setError(proError(err, t));
        // Select the wrong code so typing the right one replaces it.
        window.setTimeout(() => codeRef.current?.select(), 0);
      }
    } finally {
      setBusy(false);
    }
  };

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
          {t("pro.codeSent", { email: email.trim() })}
        </p>
        <Field label={t("pro.codeLabel")} error={error || undefined}>
          <input
            ref={codeRef}
            className="input mono pro-code"
            inputMode="numeric"
            autoComplete="one-time-code"
            maxLength={7}
            placeholder="123456"
            value={code}
            onChange={(e) => {
              setCode(e.target.value.replace(/[^\d]/g, "").slice(0, 6));
              setError("");
            }}
          />
        </Field>
        <button className="btn primary" type="submit" disabled={busy || code.length !== 6}>
          {busy && <LoaderCircle size={14} className="spin" />}
          {t("pro.verify")}
        </button>
        <div className="row" style={{ gap: 4, flexWrap: "wrap" }}>
          <button
            type="button"
            className="btn sm ghost"
            onClick={() => {
              setStep("email");
              setCode("");
              setError("");
              setCooldown(0);
            }}
          >
            {t("pro.otherEmail")}
          </button>
          <button
            type="button"
            className="btn sm ghost"
            disabled={busy || cooldown > 0}
            onClick={() => void send(undefined, true)}
          >
            {cooldown > 0 ? t("pro.resendIn", { s: cooldown }) : t("pro.resend")}
          </button>
        </div>
      </form>
      {limit && <DeviceLimitDialog limit={limit} onClose={() => setLimit(null)} />}
    </>
  );
}

/** The device unused for longest: the safest one to suggest signing out. */
function stalest(devices: CloudDevice[]): string {
  const seen = (d: CloudDevice) => (d.lastSeen ? Date.parse(d.lastSeen) || 0 : 0);
  return [...devices].sort((a, b) => seen(a) - seen(b))[0]?.id ?? "";
}

function DeviceLimitDialog({ limit, onClose }: { limit: DeviceLimit; onClose: () => void }) {
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
      setError(proError(e, t));
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

  const refresh = async () => {
    setRefreshing(true);
    try {
      setCloud(await cloudApi.refresh());
    } catch (e) {
      toast("error", t("pro.title"), proError(e, t));
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
          <button className="btn sm" disabled={refreshing} onClick={() => void refresh()}>
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
      </div>

      {order && <OrderBanner cloud={cloud} />}

      <div>
        <div className="field-label" style={{ marginBottom: 8 }}>
          {active || plan?.paidUntil ? t("pro.extendTitle") : t("pro.buyTitle")}
          <span className="faint" style={{ fontWeight: 400 }}>
            {" "}
            · {monthlyPrice(prices, lang, t)}
          </span>
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
    </div>
  );
}

function OrderBanner({ cloud }: { cloud: CloudStatus }) {
  const t = useT();
  const lang = useLang();
  const o = cloud.order!;
  const amount = o.amount || (cloud.prices.find((p) => p.months === o.months)?.total ?? 0);
  const [checking, setChecking] = useState(false);
  const dismiss = () => void cloudApi.stopWaiting().then(setCloud);
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
            <div className="muted">{t("pro.paidBody", { date: day(cloud.plan.paidUntil, lang) })}</div>
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
        {o.waiting && <div className="muted">{t("pro.waitingBody")}</div>}
        <div className="row" style={{ gap: 6, marginTop: 8, flexWrap: "wrap" }}>
          {o.checkoutUrl && (
            <button className="btn sm" onClick={() => openExternal(o.checkoutUrl!)}>
              <ExternalLink size={13} />
              {t("pro.reopen")}
            </button>
          )}
          <button className="btn sm ghost" disabled={checking} onClick={() => void check()}>
            {checking && <LoaderCircle size={13} className="spin" />}
            {t("pro.checkNow")}
          </button>
          <button className="btn sm ghost" title={t("pro.stopWaitingHint")} onClick={dismiss}>
            {t("pro.stopWaiting")}
          </button>
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
      else toast("error", t("pro.testFailed", { name: c.name }), r.error);
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
        <button className="btn sm" disabled={!canAdd || cloud.channels.length >= 10} onClick={() => setAdding(true)}>
          <Plus size={14} />
          {t("pro.addChannel")}
        </button>
      </div>
      {!canAdd && (
        <div className="pro-row muted" style={{ fontSize: 13 }}>
          <Info size={14} />
          {t("pro.needPlan")}
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
                    {t("pro.lastFail", { ago: since(c.lastResult.at, t), err: c.lastResult.error ?? "" })}
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
  const [field, setField] = useState<{ name: string; text: string } | null>(null);
  const [error, setError] = useState("");
  const bot = kind === "telegram" || kind === "zalo";
  const ready = bot ? botToken.trim() !== "" && chatId.trim() !== "" : webhookUrl.trim() !== "";

  const fieldError = (f: string) => (field?.name === f ? field.text : undefined);

  const submit = async (e?: FormEvent) => {
    e?.preventDefault();
    if (!ready) return;
    setBusy(true);
    setField(null);
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
      if (
        err instanceof ApiError &&
        err.code === "invalid_config" &&
        f &&
        ["botToken", "chatId", "webhookUrl"].includes(f)
      ) {
        setField({ name: f, text: t(`ch.err.${kind}.${f}` as TKey) });
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
              setField(null);
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
                  setField(null);
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
                  setField(null);
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
                setField(null);
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
            {t("pro.fwMuted")}
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
      <SettingRow title={t("pro.fwLang")} last>
        <Segmented<"vi" | "en">
          value={f.lang}
          onChange={(v) => void save({ ...f, lang: v })}
          options={[
            { value: "vi", label: "Tiếng Việt" },
            { value: "en", label: "English" },
          ]}
        />
      </SettingRow>
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
