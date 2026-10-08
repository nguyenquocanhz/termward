import { useEffect, useState, type ReactNode } from "react";
import { useShallow } from "zustand/react/shallow";
import {
  ClipboardPaste,
  FileInput,
  FileQuestion,
  FileUp,
  FolderOpen,
  HardDrive,
  Info,
  KeyRound,
  Lock,
  Power,
  RotateCw,
  Send,
  ShieldAlert,
  ShieldCheck,
} from "lucide-react";
import {
  api,
  ApiError,
  type ImportResult,
  type ImportSource,
  type Key,
  type KeyCandidate,
  type KnownCandidate,
  type KnownHostsImport,
  type SshConfigImport,
} from "../lib/api";
import { useT, type TKey } from "../lib/i18n";
import { hostTarget } from "../lib/target";
import { keyTypeLabel } from "../lib/format";
import {
  closeDialog,
  ensureConnected,
  errorText,
  refreshHosts,
  refreshKeys,
  setPrefs,
  startHardwareCheck,
  toast,
  useApp,
  type Dialog,
  type Prompt,
} from "../store";
import { caps } from "../lib/platform";
import { HostForm } from "./HostForm";
import { Field, Modal, Segmented } from "./ui";

export function DialogHost() {
  const { dialog, prompt } = useApp(useShallow((s) => ({ dialog: s.dialog, prompt: s.prompt })));
  return (
    <>
      {dialog && <DialogSwitch d={dialog} />}
      {prompt && <PromptSwitch p={prompt} />}
    </>
  );
}

function DialogSwitch({ d }: { d: Dialog }) {
  switch (d.kind) {
    case "host":
      return <HostForm host={d.host} />;
    case "generateKey":
      return <GenerateKey onCreated={d.onCreated} />;
    case "importKey":
      return <ImportKey />;
    case "deployKey":
      return <DeployKey keyId={d.keyId} hostId={d.hostId} />;
    case "importConfig":
      return <ImportConfig />;
    case "confirm":
      return <Confirm d={d} />;
    case "power":
      return <PowerDialog hostId={d.hostId} action={d.action} />;
    case "hardwareSudo":
      return <HardwareSudoDialog hostId={d.hostId} reason={d.reason} />;
    case "paste":
      return <PasteDialog d={d} />;
  }
}

// ---------------------------------------------------------------- power

function PowerDialog({ hostId, action }: { hostId: string; action: "reboot" | "poweroff" }) {
  const t = useT();
  const host = useApp((s) => s.hosts.find((h) => h.id === hostId));
  const [typed, setTyped] = useState("");
  const [sudo, setSudo] = useState("");
  const [needSudo, setNeedSudo] = useState<"" | "required" | "wrong">("");
  const [busy, setBusy] = useState(false);
  if (!host) return null;
  const reboot = action === "reboot";
  // Shutting down cannot be undone remotely, so it needs the name typed.
  const confirmed = reboot || typed.trim() === host.name;

  const submit = async () => {
    setBusy(true);
    try {
      if (!(await ensureConnected(host.id))) return;
      await api.power(host.id, action, sudo || undefined);
      closeDialog();
      toast("success", t(reboot ? "power.rebooting" : "power.shuttingDown", { host: host.name }));
    } catch (e) {
      if (e instanceof ApiError && (e.code === "sudo_required" || e.code === "sudo_wrong")) {
        setNeedSudo(e.code === "sudo_required" ? "required" : "wrong");
      } else {
        toast("error", host.name, errorText(e));
      }
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal
      title={t(reboot ? "power.rebootTitle" : "power.poweroffTitle", { host: host.name })}
      icon={
        <div className="key-icon" style={{ background: "var(--crit-soft)", color: "var(--crit)" }}>
          <Power size={17} />
        </div>
      }
      onClose={closeDialog}
      footer={
        <>
          <button className="btn" onClick={closeDialog}>
            {t("common.cancel")}
          </button>
          <button className="btn danger solid" disabled={busy || !confirmed} onClick={submit}>
            {reboot ? <RotateCw size={14} /> : <Power size={14} />}
            {t(reboot ? "power.doReboot" : "power.doPoweroff")}
          </button>
        </>
      }
    >
      <p className="muted" style={{ margin: 0 }}>
        {t(reboot ? "power.rebootBody" : "power.poweroffBody")}
      </p>
      {!reboot && (
        <Field label={t("power.typeName", { name: host.name })}>
          <input className="input" value={typed} onChange={(e) => setTyped(e.target.value)} placeholder={host.name} />
        </Field>
      )}
      {(host.user !== "root" || needSudo) && (
        <Field
          label={t("power.sudo")}
          hint={needSudo ? undefined : t("power.sudoHint")}
          error={
            needSudo === "wrong"
              ? t("power.sudoWrong")
              : needSudo === "required"
                ? t("power.sudoRequired", { user: host.user })
                : undefined
          }
        >
          <input
            className="input"
            type="password"
            value={sudo}
            autoComplete="off"
            onChange={(e) => setSudo(e.target.value)}
            onKeyDown={(e) => e.key === "Enter" && confirmed && void submit()}
          />
        </Field>
      )}
    </Modal>
  );
}

// ---------------------------------------------------------------- hardware

/** Asked when a hardware check needs root: sudo password, or a limited run. */
function HardwareSudoDialog({ hostId, reason }: { hostId: string; reason: "required" | "wrong" }) {
  const t = useT();
  const host = useApp((s) => s.hosts.find((h) => h.id === hostId));
  const [sudo, setSudo] = useState("");
  if (!host) return null;

  // The check takes minutes: close now, the dialog comes back if sudo fails.
  const run = (opts: { sudoPassword?: string; allowNoRoot?: boolean }) => {
    closeDialog();
    void startHardwareCheck(hostId, opts);
  };

  return (
    <Modal
      title={t("hw.sudoTitle", { host: host.name })}
      icon={
        <div className="key-icon">
          <HardDrive size={17} />
        </div>
      }
      onClose={closeDialog}
      footer={
        <>
          <button className="btn" onClick={closeDialog}>
            {t("common.cancel")}
          </button>
          <button className="btn primary" disabled={!sudo} onClick={() => run({ sudoPassword: sudo })}>
            <HardDrive size={14} />
            {t("hw.sudoRun")}
          </button>
        </>
      }
    >
      <p className="muted" style={{ margin: 0 }}>
        {t("hw.sudoBody", { user: host.user })}
      </p>
      <Field label={t("power.sudo")} error={reason === "wrong" ? t("power.sudoWrong") : undefined}>
        <input
          className="input"
          type="password"
          value={sudo}
          autoComplete="off"
          onChange={(e) => setSudo(e.target.value)}
          onKeyDown={(e) => e.key === "Enter" && sudo && run({ sudoPassword: sudo })}
        />
      </Field>
      <button className="btn ghost sm hw-noroot" onClick={() => run({ allowNoRoot: true })}>
        {t("hw.noRootRun")}
      </button>
    </Modal>
  );
}

function PromptSwitch({ p }: { p: Prompt }) {
  return p.kind === "trust" ? <TrustPrompt p={p} /> : <CredentialPrompt p={p} />;
}

// ---------------------------------------------------------------- keys

function GenerateKey({ onCreated }: { onCreated?: (k: Key) => void }) {
  const t = useT();
  const [type, setType] = useState<"ed25519" | "rsa" | "ecdsa">("ed25519");
  const [name, setName] = useState("");
  const [comment, setComment] = useState("");
  const [pass, setPass] = useState("");
  const [pass2, setPass2] = useState("");
  const [remember, setRemember] = useState(true);
  const [busy, setBusy] = useState(false);
  const mismatch = pass !== pass2;

  const submit = async () => {
    setBusy(true);
    try {
      const k = await api.generateKey({
        name,
        type,
        bits: type === "rsa" ? 4096 : type === "ecdsa" ? 256 : undefined,
        comment,
        passphrase: pass || undefined,
        rememberPassphrase: remember,
      });
      await refreshKeys();
      closeDialog();
      toast("success", t("gen.done", { name: k.name }));
      onCreated?.(k);
    } catch (e) {
      toast("error", errorText(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal
      title={t("gen.title")}
      subtitle={t("gen.subtitle")}
      icon={
        <div className="key-icon">
          <KeyRound size={17} />
        </div>
      }
      onClose={closeDialog}
      footer={
        <>
          <button className="btn" onClick={closeDialog}>
            {t("common.cancel")}
          </button>
          <button className="btn primary" disabled={busy || mismatch} onClick={submit}>
            {t("gen.create")}
          </button>
        </>
      }
    >
      <Field label={t("gen.type")}>
        <Segmented
          value={type}
          onChange={setType}
          options={[
            {
              value: "ed25519",
              label: (
                <>
                  Ed25519{" "}
                  <span className="badge accent" style={{ height: 17 }}>
                    {t("gen.recommended")}
                  </span>
                </>
              ),
            },
            { value: "rsa", label: "RSA 4096" },
            { value: "ecdsa", label: "ECDSA P-256" },
          ]}
        />
      </Field>
      <div className="grid-2">
        <Field label={t("gen.name")}>
          <input className="input" value={name} placeholder="laptop-2026" onChange={(e) => setName(e.target.value)} />
        </Field>
        <Field
          label={
            <>
              {t("gen.comment")} <span className="faint">({t("common.optional")})</span>
            </>
          }
        >
          <input
            className="input"
            value={comment}
            placeholder="me@laptop"
            onChange={(e) => setComment(e.target.value)}
          />
        </Field>
      </div>
      <div className="grid-2">
        <Field
          label={
            <>
              {t("gen.passphrase")} <span className="faint">({t("common.optional")})</span>
            </>
          }
          hint={t("gen.passphraseHint")}
        >
          <input
            className="input"
            type="password"
            value={pass}
            onChange={(e) => setPass(e.target.value)}
            autoComplete="new-password"
          />
        </Field>
        <Field label={t("gen.confirm")} error={pass2 && mismatch ? t("gen.mismatch") : undefined}>
          <input
            className="input"
            type="password"
            value={pass2}
            onChange={(e) => setPass2(e.target.value)}
            autoComplete="new-password"
            disabled={!pass}
          />
        </Field>
      </div>
      {pass && (
        <label className="check">
          <input type="checkbox" checked={remember} onChange={(e) => setRemember(e.target.checked)} />
          {t("cred.remember")}
        </label>
      )}
    </Modal>
  );
}

function ImportKey() {
  const t = useT();
  // Mobile has no ~/.ssh; its "file" tab reads a key picked from device storage.
  const [tab, setTab] = useState<"ssh" | "file" | "paste">(caps.sshDir ? "ssh" : "file");
  const [found, setFound] = useState<KeyCandidate[] | null>(null);
  const [path, setPath] = useState("");
  const [pem, setPem] = useState("");
  const [fileName, setFileName] = useState("");
  const [name, setName] = useState("");
  const [pass, setPass] = useState("");
  const [busy, setBusy] = useState(false);
  const byPath = tab === "file" && caps.fileImport;

  useEffect(() => {
    if (caps.sshDir) api.scanKeys().then(setFound, () => setFound([]));
  }, []);

  const doImport = async (body: { path?: string; pem?: string }) => {
    setBusy(true);
    try {
      const k = await api.importKey({
        ...body,
        name: name || undefined,
        passphrase: pass || undefined,
        rememberPassphrase: true,
      });
      await refreshKeys();
      toast("success", t("imp.done", { name: k.name }));
      if (tab === "ssh") setFound(await api.scanKeys());
      else closeDialog();
    } catch (e) {
      if (e instanceof ApiError && e.code === "already_imported") toast("info", t("imp.already"));
      else toast("error", errorText(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal
      title={t("imp.title")}
      onClose={closeDialog}
      wide
      icon={
        <div className="key-icon">
          <FileInput size={17} />
        </div>
      }
      footer={
        tab === "ssh" ? (
          <button className="btn" onClick={closeDialog}>
            {t("common.close")}
          </button>
        ) : (
          <>
            <button className="btn" onClick={closeDialog}>
              {t("common.cancel")}
            </button>
            <button
              className="btn primary"
              disabled={busy || (byPath ? !path : !pem.trim())}
              onClick={() => void doImport(byPath ? { path } : { pem })}
            >
              {t("imp.importBtn")}
            </button>
          </>
        )
      }
    >
      <Segmented
        value={tab}
        onChange={setTab}
        options={[
          ...(caps.sshDir ? [{ value: "ssh" as const, label: t("imp.fromSsh") }] : []),
          { value: "file", label: t("imp.fromFile") },
          { value: "paste", label: t("imp.paste") },
        ]}
      />
      {tab === "ssh" &&
        (found === null ? (
          <div className="muted">…</div>
        ) : found.length === 0 ? (
          <div className="callout">{t("imp.none")}</div>
        ) : (
          <div className="card">
            {found.map((c) => (
              <div className="key-card" key={c.path} style={{ padding: "10px 14px" }}>
                <div className="key-icon" style={{ width: 30, height: 30 }}>
                  <KeyRound size={14} />
                </div>
                <div style={{ minWidth: 0 }}>
                  <div className="row">
                    <strong>{c.name}</strong>
                    <span className="badge">{keyTypeLabel(c.type, 0).replace(/ 0$/, "")}</span>
                    {c.encrypted && <Lock size={12} className="muted" />}
                  </div>
                  <div className="mono muted truncate" style={{ fontSize: 11.5 }}>
                    {c.fingerprint}
                  </div>
                </div>
                {c.imported ? (
                  <span className="badge ok">{t("imp.imported")}</span>
                ) : (
                  <button className="btn sm" disabled={busy} onClick={() => void doImport({ path: c.path })}>
                    {t("imp.importBtn")}
                  </button>
                )}
              </div>
            ))}
          </div>
        ))}
      {tab === "file" && !byPath && (
        <Field label={t("imp.path")}>
          <label className="btn" style={{ alignSelf: "flex-start" }}>
            <FileUp size={14} />
            {fileName || t("keys.fromDevice")}
            <input
              type="file"
              hidden
              onChange={async (e) => {
                const f = e.target.files?.[0];
                if (!f) return;
                if (f.size > 64 * 1024) return toast("error", t("err.generic"), "file too large for a key");
                setFileName(f.name);
                setName((n) => n || f.name);
                setPem(await f.text());
              }}
            />
          </label>
        </Field>
      )}
      {byPath && (
        <Field label={t("imp.path")}>
          <div className="row">
            <input
              className="input mono"
              value={path}
              onChange={(e) => setPath(e.target.value)}
              placeholder="~/.ssh/id_ed25519"
            />
            {window.termward && (
              <button
                className="btn"
                onClick={async () => {
                  const p = await window.termward!.pickKeyFile();
                  if (p) setPath(p);
                }}
              >
                <FolderOpen size={14} />
                {t("imp.browse")}
              </button>
            )}
          </div>
        </Field>
      )}
      {tab === "paste" && (
        <Field label={t("imp.pem")}>
          <textarea
            className="textarea mono"
            rows={7}
            value={pem}
            onChange={(e) => setPem(e.target.value)}
            placeholder="-----BEGIN OPENSSH PRIVATE KEY-----"
            spellCheck={false}
          />
        </Field>
      )}
      {tab !== "ssh" && (
        <div className="grid-2">
          <Field
            label={
              <>
                {t("gen.name")} <span className="faint">({t("common.optional")})</span>
              </>
            }
          >
            <input className="input" value={name} onChange={(e) => setName(e.target.value)} />
          </Field>
          <Field
            label={
              <>
                {t("gen.passphrase")} <span className="faint">({t("common.optional")})</span>
              </>
            }
          >
            <input className="input" type="password" value={pass} onChange={(e) => setPass(e.target.value)} />
          </Field>
        </div>
      )}
    </Modal>
  );
}

function DeployKey({ keyId, hostId }: { keyId: string; hostId?: string }) {
  const t = useT();
  const { keys, hosts } = useApp(useShallow((s) => ({ keys: s.keys, hosts: s.hosts })));
  const key = keys.find((k) => k.id === keyId);
  const candidates = hosts.filter((h) => !(h.auth === "key" && h.keyId === keyId));
  const [target, setTarget] = useState(hostId ?? candidates[0]?.id ?? "");
  const [switchAuth, setSwitchAuth] = useState(true);
  const [busy, setBusy] = useState(false);
  if (!key) return null;

  const submit = async () => {
    const h = hosts.find((x) => x.id === target);
    if (!h) return;
    setBusy(true);
    try {
      // The deploy uses the host's current sign-in, which may need a prompt first.
      if (!(await ensureConnected(h.id))) return;
      const res = await api.deployKey(keyId, h.id, switchAuth);
      await refreshHosts();
      closeDialog();
      toast("success", res.alreadyPresent ? t("dep.already", { host: h.name }) : t("dep.done", { host: h.name }));
    } catch (e) {
      toast("error", errorText(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal
      title={t("dep.title", { name: key.name })}
      subtitle={t("dep.subtitle")}
      icon={
        <div className="key-icon">
          <Send size={16} />
        </div>
      }
      onClose={closeDialog}
      footer={
        <>
          <button className="btn" onClick={closeDialog}>
            {t("common.cancel")}
          </button>
          <button className="btn primary" disabled={busy || !target} onClick={submit}>
            {t("dep.submit")}
          </button>
        </>
      }
    >
      <Field label={t("dep.host")}>
        <select className="select" value={target} onChange={(e) => setTarget(e.target.value)}>
          {candidates.map((h) => (
            <option key={h.id} value={h.id}>
              {h.name} ({h.user}@{h.address})
            </option>
          ))}
        </select>
      </Field>
      <label className="check">
        <input type="checkbox" checked={switchAuth} onChange={(e) => setSwitchAuth(e.target.checked)} />
        <span>
          {t("dep.switch")}
          <small>{t("dep.switchHint")}</small>
        </span>
      </label>
    </Modal>
  );
}

// ---------------------------------------------------------------- import servers

type ImportFrom = "config" | "known";

const knownKey = (c: KnownCandidate) => `${c.address} ${c.port}`;

/** Loads one import source; `data` is null while loading. */
function useImportSource<T extends ImportSource>(load: (path?: string) => Promise<T>) {
  const [path, setPath] = useState<string | undefined>();
  const [data, setData] = useState<T | null>(null);
  const [failed, setFailed] = useState("");
  useEffect(() => {
    let stale = false;
    setData(null);
    setFailed("");
    load(path).then(
      (d) => !stale && setData(d),
      (e) => !stale && setFailed(errorText(e)),
    );
    return () => {
      stale = true;
    };
  }, [path]);
  return { path, setPath, data, failed };
}

function ImportConfig() {
  const t = useT();
  const [from, setFrom] = useState<ImportFrom>("config");
  const cfg = useImportSource<SshConfigImport>(api.sshConfig);
  const known = useImportSource<KnownHostsImport>(api.knownHosts);
  const [pickedCfg, setPickedCfg] = useState<Set<string>>(new Set());
  const [pickedKnown, setPickedKnown] = useState<Set<string>>(new Set());
  const [edits, setEdits] = useState<Record<string, { user?: string; group?: string }>>({});
  const [group, setGroup] = useState("");
  const [busy, setBusy] = useState(false);

  // A config lists hosts the user set up on purpose: preselect the new ones.
  // known_hosts is a history (every server ever visited): nothing preselected.
  useEffect(() => {
    setPickedCfg(new Set((cfg.data?.entries ?? []).filter((c) => !c.exists).map((c) => c.alias)));
  }, [cfg.data]);
  useEffect(() => {
    setPickedKnown(new Set());
    setEdits({});
  }, [known.data]);

  const src = from === "config" ? cfg : known;
  const knownRows = known.data?.entries ?? [];
  const userOf = (c: KnownCandidate) => edits[knownKey(c)]?.user ?? known.data?.defaultUser ?? "";
  const chosenKnown = knownRows.filter((c) => !c.exists && pickedKnown.has(knownKey(c)));
  const noUser = from === "known" && chosenKnown.some((c) => !userOf(c).trim());
  const count = from === "config" ? pickedCfg.size : chosenKnown.length;
  const newKnown = known.data && known.data.exists && !known.data.error ? knownRows.filter((c) => !c.exists).length : 0;

  const finish = (res: ImportResult, note?: string) => {
    const problems = [
      ...res.skipped.map((n) => `${n}: ${t("cfg.exists")}`),
      ...Object.entries(res.failed).map(([n, m]) => `${n}: ${m}`),
    ];
    closeDialog();
    toast(
      res.imported.length ? "success" : "info",
      t("cfg.done", { n: res.imported.length }),
      [res.imported.length ? note : undefined, ...problems].filter(Boolean).join("\n") || undefined,
    );
  };

  const submit = async () => {
    if (count === 0 || noUser) return;
    setBusy(true);
    try {
      if (from === "config") {
        const res = await api.importSshConfig([...pickedCfg], group, cfg.path);
        await Promise.all([refreshHosts(), refreshKeys()]);
        finish(res);
      } else {
        const res = await api.importKnownHosts(
          chosenKnown.map((c) => ({
            name: c.name,
            address: c.address,
            port: c.port,
            user: userOf(c).trim(),
            group: (edits[knownKey(c)]?.group ?? "").trim() || group.trim(),
          })),
        );
        await refreshHosts();
        finish(res, t("cfg.knownDoneNote"));
      }
    } catch (e) {
      toast("error", errorText(e));
    } finally {
      setBusy(false);
    }
  };

  const choose = async () => {
    const p = await window.termward?.pickSshFile?.(from === "config" ? "config" : "known_hosts");
    if (p) src.setPath(p);
  };
  // Ways forward offered wherever a source has nothing to list.
  const actions = (
    <>
      {window.termward?.pickSshFile && (
        <button className="btn sm" onClick={() => void choose()}>
          <FolderOpen size={13} />
          {t("cfg.choose")}
        </button>
      )}
      {from === "config" && newKnown > 0 && (
        <button className="btn sm" onClick={() => setFrom("known")}>
          {t("cfg.tryKnown", { n: newKnown })}
        </button>
      )}
      {src.path && (
        <button className="btn sm ghost" onClick={() => src.setPath(undefined)}>
          {t("cfg.useDefault")}
        </button>
      )}
    </>
  );

  const d = src.data;
  const selectableCfg = cfg.data?.entries.filter((c) => !c.exists) ?? [];
  const selectableKnown = knownRows.filter((c) => !c.exists);

  return (
    <Modal
      title={t("cfg.title")}
      subtitle={t(from === "config" ? "cfg.subtitle" : "cfg.knownSubtitle")}
      wide
      icon={
        <div className="key-icon">
          <FileInput size={17} />
        </div>
      }
      onClose={closeDialog}
      footer={
        <>
          {noUser && <span className="import-foot-note">{t("cfg.needUser")}</span>}
          <button className="btn" onClick={closeDialog}>
            {t("common.cancel")}
          </button>
          <button className="btn primary" disabled={busy || count === 0 || noUser} onClick={() => void submit()}>
            {count === 0 ? t("cfg.importNone") : t("cfg.import", { n: count })}
          </button>
        </>
      }
    >
      <Segmented<ImportFrom>
        value={from}
        onChange={setFrom}
        options={[
          { value: "config", label: t("cfg.fromConfig") },
          { value: "known", label: t("cfg.fromKnown") },
        ]}
      />

      {src.failed ? (
        // Keep the ways out: a failed read must not leave only Cancel.
        <ImportNote tone="crit" icon={<ShieldAlert size={18} />} title={src.failed} body={t("cfg.errBody")}>
          {actions}
        </ImportNote>
      ) : d === null ? (
        <div className="muted">{t("cfg.reading")}</div>
      ) : !d.exists ? (
        <ImportNote
          icon={<FileQuestion size={18} />}
          title={t("cfg.missing", { path: d.path })}
          body={t(from === "config" ? "cfg.missingConfig" : "cfg.missingKnown")}
        >
          {actions}
        </ImportNote>
      ) : d.error ? (
        <ImportNote
          tone="crit"
          icon={<ShieldAlert size={18} />}
          title={
            d.error === "parse" && d.errorLine && from === "config"
              ? t("cfg.errParseLine", { path: d.path, line: d.errorLine })
              : t(importErrorKey(from, d.error), { path: d.path })
          }
          body={t("cfg.errBody")}
        >
          {actions}
        </ImportNote>
      ) : d.entries.length === 0 ? (
        <ImportNote
          icon={<Info size={18} />}
          title={
            from === "config"
              ? t("cfg.emptyConfig", { path: d.path })
              : known.data!.hashed > 0
                ? t("cfg.allHashed", { path: d.path, n: known.data!.hashed })
                : t("cfg.emptyKnown", { path: d.path })
          }
          body={t(from === "config" ? "cfg.emptyConfigBody" : "cfg.emptyKnownBody")}
        >
          {actions}
        </ImportNote>
      ) : (
        <>
          <div className="import-source">
            <span className="mono truncate selectable" title={d.path}>
              {d.path}
            </span>
            {actions}
          </div>
          {from === "config" ? (
            <div className="card" style={{ maxHeight: 340, overflowY: "auto" }}>
              <table className="table">
                <thead>
                  <tr>
                    <th style={{ width: 30 }}>
                      <input
                        type="checkbox"
                        checked={selectableCfg.length > 0 && selectableCfg.every((c) => pickedCfg.has(c.alias))}
                        disabled={selectableCfg.length === 0}
                        onChange={(e) =>
                          setPickedCfg(new Set(e.target.checked ? selectableCfg.map((c) => c.alias) : []))
                        }
                        title={t("cfg.selectAll")}
                        aria-label={t("cfg.selectAll")}
                      />
                    </th>
                    <th>Host</th>
                    <th>{t("form.address")}</th>
                    <th>{t("host.auth")}</th>
                  </tr>
                </thead>
                <tbody>
                  {cfg.data!.entries.map((c) => (
                    <tr key={c.alias} style={{ opacity: c.exists ? 0.55 : 1 }}>
                      <td>
                        <input
                          type="checkbox"
                          disabled={c.exists}
                          checked={pickedCfg.has(c.alias)}
                          aria-label={c.alias}
                          onChange={(e) => setPickedCfg((p) => toggled(p, c.alias, e.target.checked))}
                        />
                      </td>
                      <td>
                        <strong>{c.alias}</strong>
                        {c.exists && (
                          <span className="badge" style={{ marginLeft: 6 }}>
                            {t("cfg.exists")}
                          </span>
                        )}
                      </td>
                      <td className="mono muted">
                        {c.user}@{c.hostName}
                        {c.port !== 22 && `:${c.port}`}
                        {c.proxyJump && <span className="faint"> via {c.proxyJump}</span>}
                      </td>
                      <td className="muted">{c.identityFile ? c.identityFile.split(/[\\/]/).pop() : "ssh-agent"}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          ) : (
            <>
              <div className="card" style={{ maxHeight: 300, overflowY: "auto" }}>
                <table className="table import-known">
                  <thead>
                    <tr>
                      <th style={{ width: 30 }}>
                        <input
                          type="checkbox"
                          checked={
                            selectableKnown.length > 0 && selectableKnown.every((c) => pickedKnown.has(knownKey(c)))
                          }
                          disabled={selectableKnown.length === 0}
                          onChange={(e) =>
                            setPickedKnown(new Set(e.target.checked ? selectableKnown.map(knownKey) : []))
                          }
                          title={t("cfg.selectAll")}
                          aria-label={t("cfg.selectAll")}
                        />
                      </th>
                      <th>{t("cfg.server")}</th>
                      <th style={{ width: 150 }}>{t("form.user")}</th>
                      <th style={{ width: 150 }}>{t("form.group")}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {knownRows.map((c) => {
                      const k = knownKey(c);
                      const on = pickedKnown.has(k);
                      const addr = hostTarget({ user: "", address: c.address, port: c.port }).slice(1);
                      return (
                        <tr key={k} style={{ opacity: c.exists ? 0.55 : 1 }}>
                          <td>
                            <input
                              type="checkbox"
                              disabled={c.exists}
                              checked={on}
                              aria-label={c.name}
                              onChange={(e) => setPickedKnown((p) => toggled(p, k, e.target.checked))}
                            />
                          </td>
                          <td>
                            <strong>{c.name}</strong>
                            {c.exists && (
                              <span className="badge" style={{ marginLeft: 6 }}>
                                {t("cfg.exists")}
                              </span>
                            )}
                            {c.name !== addr && !(c.name === c.address && c.port === 22) && (
                              <div className="mono muted">{addr}</div>
                            )}
                          </td>
                          <td>
                            <input
                              className={`input sm mono${on && !userOf(c).trim() ? " invalid" : ""}`}
                              value={userOf(c)}
                              disabled={c.exists}
                              spellCheck={false}
                              autoCapitalize="off"
                              aria-label={`${t("form.user")}: ${c.name}`}
                              onChange={(e) => setEdits((m) => ({ ...m, [k]: { ...m[k], user: e.target.value } }))}
                            />
                          </td>
                          <td>
                            <input
                              className="input sm"
                              value={edits[k]?.group ?? ""}
                              disabled={c.exists}
                              placeholder={group || t("form.groupPh")}
                              aria-label={`${t("form.group")}: ${c.name}`}
                              onChange={(e) => setEdits((m) => ({ ...m, [k]: { ...m[k], group: e.target.value } }))}
                            />
                          </td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              </div>
              <div className="callout info import-hint">
                <Info size={15} />
                <div>
                  {t("cfg.knownHint")}
                  {known.data!.hashed > 0 && <div>{t("cfg.hashed", { n: known.data!.hashed })}</div>}
                  {known.data!.invalid > 0 && <div>{t("cfg.invalid", { n: known.data!.invalid })}</div>}
                </div>
              </div>
            </>
          )}
          <Field
            label={
              <>
                {t("cfg.group")} <span className="faint">({t("common.optional")})</span>
              </>
            }
          >
            <input
              className="input"
              value={group}
              onChange={(e) => setGroup(e.target.value)}
              placeholder={t("form.groupPh")}
            />
          </Field>
        </>
      )}
    </Modal>
  );
}

function toggled(set: Set<string>, key: string, on: boolean): Set<string> {
  const next = new Set(set);
  if (on) next.add(key);
  else next.delete(key);
  return next;
}

function importErrorKey(from: ImportFrom, error: NonNullable<ImportSource["error"]>): TKey {
  switch (error) {
    case "parse":
      return from === "config" ? "cfg.errParse" : "cfg.errParseKnown";
    case "not_a_file":
      return "cfg.errNotFile";
    case "too_large":
      return "cfg.errTooLarge";
    default:
      return "cfg.errUnreadable";
  }
}

/** An import source with nothing to list: says why, and what to do next. */
function ImportNote({
  icon,
  title,
  body,
  tone,
  children,
}: {
  icon: ReactNode;
  title: string;
  body: string;
  tone?: "crit";
  children?: ReactNode;
}) {
  return (
    <div className={`import-note${tone ? ` ${tone}` : ""}`}>
      <div className="import-note-icon">{icon}</div>
      <div className="import-note-text">
        <strong className="selectable">{title}</strong>
        <p>{body}</p>
        <div className="import-note-actions">{children}</div>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------- paste

const PASTE_PREVIEW_LINES = 6;

/** Several lines are about to be typed into a shell: show them first. */
function PasteDialog({ d }: { d: Extract<Dialog, { kind: "paste" }> }) {
  const t = useT();
  const [dontAsk, setDontAsk] = useState(false);
  const lines = d.text.replace(/\n+$/, "").split("\n");
  const shown = lines.slice(0, PASTE_PREVIEW_LINES).map((l) => (l.length > 160 ? l.slice(0, 160) + "…" : l));
  const close = () => {
    closeDialog();
    d.onClose();
  };
  const paste = () => {
    if (dontAsk) setPrefs({ confirmPaste: false });
    closeDialog();
    d.onPaste();
    d.onClose();
  };
  return (
    <Modal
      title={t("paste.title", { n: lines.length })}
      subtitle={t("paste.body", { target: d.target })}
      icon={
        <div className="key-icon">
          <ClipboardPaste size={17} />
        </div>
      }
      onClose={close}
      footer={
        <>
          <button className="btn" onClick={close}>
            {t("common.cancel")}
          </button>
          <button className="btn primary" autoFocus onClick={paste}>
            {t("term.paste")}
          </button>
        </>
      }
    >
      <pre className="paste-preview">
        {shown.join("\n")}
        {lines.length > shown.length && (
          <span className="faint">{"\n" + t("paste.more", { n: lines.length - shown.length })}</span>
        )}
      </pre>
      <label className="check">
        <input type="checkbox" checked={dontAsk} onChange={(e) => setDontAsk(e.target.checked)} />
        <span>
          {t("paste.dontAsk")}
          <small>{t("paste.dontAskHint")}</small>
        </span>
      </label>
    </Modal>
  );
}

// ---------------------------------------------------------------- confirm & prompts

function Confirm({ d }: { d: Extract<Dialog, { kind: "confirm" }> }) {
  const t = useT();
  const [busy, setBusy] = useState(false);
  return (
    <Modal
      title={d.title}
      onClose={closeDialog}
      footer={
        <>
          <button className="btn" onClick={closeDialog}>
            {t("common.cancel")}
          </button>
          <button
            className={`btn ${d.danger ? "danger solid" : "primary"}`}
            disabled={busy}
            autoFocus
            onClick={async () => {
              setBusy(true);
              try {
                await d.run();
                closeDialog();
              } catch (e) {
                toast("error", errorText(e));
              } finally {
                setBusy(false);
              }
            }}
          >
            {d.confirm}
          </button>
        </>
      }
    >
      <p className="muted" style={{ margin: 0 }}>
        {d.body}
      </p>
    </Modal>
  );
}

function TrustPrompt({ p }: { p: Extract<Prompt, { kind: "trust" }> }) {
  const t = useT();
  const algo = p.details.keyType.replace(/^ssh-/, "").replace(/^ecdsa-sha2-nistp\d+$/, "ecdsa");
  return (
    <Modal
      title={p.changed ? t("trust.changedTitle", { host: p.hostName }) : t("trust.title", { host: p.hostName })}
      icon={
        <div
          className="key-icon"
          style={p.changed ? { background: "var(--crit-soft)", color: "var(--crit)" } : undefined}
        >
          {p.changed ? <ShieldAlert size={17} /> : <ShieldCheck size={17} />}
        </div>
      }
      onClose={() => p.resolve(false)}
      footer={
        <>
          <button className="btn" onClick={() => p.resolve(false)}>
            {t("common.cancel")}
          </button>
          <button className={`btn ${p.changed ? "danger solid" : "primary"}`} onClick={() => p.resolve(true)}>
            {p.changed ? t("trust.acceptChanged") : t("trust.accept")}
          </button>
        </>
      }
    >
      <p className="muted" style={{ margin: 0 }}>
        {p.changed ? t("trust.changedBody") : t("trust.body", { address: p.details.address })}
      </p>
      <div className="fingerprint">
        <div className="faint" style={{ fontSize: 11.5, marginBottom: 2 }}>
          {p.details.keyType}
        </div>
        {p.details.fingerprint}
      </div>
      <div className="muted" style={{ fontSize: 12.5 }}>
        {t("trust.verify")}
        <div className="fingerprint" style={{ marginTop: 6 }}>
          ssh-keygen -lf /etc/ssh/ssh_host_{algo}_key.pub
        </div>
      </div>
    </Modal>
  );
}

function CredentialPrompt({ p }: { p: Extract<Prompt, { kind: "credential" }> }) {
  const t = useT();
  const [value, setValue] = useState("");
  const [remember, setRemember] = useState(true);
  const isPass = p.details.kind === "password";
  const submit = () => value && p.resolve({ value, remember });
  return (
    <Modal
      title={
        isPass ? t("cred.passwordTitle", { host: p.hostName }) : t("cred.passphraseTitle", { key: p.keyName ?? "" })
      }
      subtitle={isPass ? t("cred.passwordBody", { target: p.target }) : t("cred.passphraseBody")}
      icon={
        <div className="key-icon">
          <Lock size={16} />
        </div>
      }
      onClose={() => p.resolve(null)}
      footer={
        <>
          <button className="btn" onClick={() => p.resolve(null)}>
            {t("common.cancel")}
          </button>
          <button className="btn primary" disabled={!value} onClick={submit}>
            {t("cred.submit")}
          </button>
        </>
      }
    >
      {p.details.wrong && (
        <div className="callout crit" style={{ padding: "8px 12px" }}>
          {isPass ? t("cred.wrongPassword") : t("cred.wrongPassphrase")}
        </div>
      )}
      <input
        className="input"
        type="password"
        value={value}
        onChange={(e) => setValue(e.target.value)}
        onKeyDown={(e) => e.key === "Enter" && submit()}
        autoComplete="off"
      />
      <label className="check">
        <input type="checkbox" checked={remember} onChange={(e) => setRemember(e.target.checked)} />
        {t("cred.remember")}
      </label>
    </Modal>
  );
}
