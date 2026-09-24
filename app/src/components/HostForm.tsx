import { useMemo, useState } from "react";
import { useShallow } from "zustand/react/shallow";
import { KeyRound, Plus, Server } from "lucide-react";
import { api, ApiError, type AuthMethod, type Host, type HostInput } from "../lib/api";
import { useT } from "../lib/i18n";
import { stripHost } from "../lib/util";
import { caps } from "../lib/platform";
import { closeDialog, ensureConnected, errorText, navigate, refreshHosts, refreshKeys, toast, useApp } from "../store";
import { Field, Modal, Segmented, Switch } from "./ui";

export function HostForm({ host }: { host?: Host }) {
  const t = useT();
  const { keys, hosts } = useApp(useShallow((s) => ({ keys: s.keys, hosts: s.hosts })));
  const editing = !!host;

  const [f, setF] = useState<HostInput>(() =>
    host
      ? { ...stripHost(host), password: "", rememberPassword: true }
      : {
          name: "",
          address: "",
          port: 22,
          user: "root",
          group: "",
          tags: [],
          auth: keys.length ? "key" : "password",
          keyId: keys[0]?.id,
          jumpHostId: "",
          monitor: true,
          notes: "",
          password: "",
          rememberPassword: true,
        },
  );
  const [tags, setTags] = useState((host?.tags ?? []).join(", "));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [creatingKey, setCreatingKey] = useState(false);

  const groups = useMemo(() => [...new Set(hosts.map((h) => h.group).filter(Boolean))].sort(), [hosts]);
  const jumpCandidates = hosts.filter((h) => h.id !== host?.id);
  const set = <K extends keyof HostInput>(k: K, v: HostInput[K]) => setF((x) => ({ ...x, [k]: v }));

  const createKey = async () => {
    setCreatingKey(true);
    try {
      const k = await api.generateKey({ name: `${f.name || f.address || "server"}-ed25519`, type: "ed25519" });
      await refreshKeys();
      setF((x) => ({ ...x, auth: "key", keyId: k.id }));
      toast("success", t("gen.done", { name: k.name }));
    } catch (e) {
      toast("error", errorText(e));
    } finally {
      setCreatingKey(false);
    }
  };

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError("");
    const body: HostInput = {
      ...f,
      name: f.name.trim() || f.address.trim(),
      tags: tags
        .split(",")
        .map((x) => x.trim())
        .filter(Boolean),
      keyId: f.auth === "key" ? f.keyId : undefined,
      password: f.auth === "password" && f.password ? f.password : undefined,
    };
    try {
      const saved = editing ? await api.updateHost(host.id, body) : await api.createHost(body);
      await refreshHosts();
      closeDialog();
      if (!editing) {
        navigate({ name: "host", hostId: saved.id });
        // Verify right away: host key prompt, credentials, first health check.
        if (await ensureConnected(saved.id)) void api.check(saved.id);
      }
    } catch (err) {
      setError(err instanceof ApiError ? err.message : errorText(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal
      title={editing ? t("form.editTitle") : t("form.addTitle")}
      subtitle={t("form.subtitle")}
      icon={
        <div className="key-icon">
          <Server size={17} />
        </div>
      }
      onClose={closeDialog}
      wide
      footer={
        <>
          {error && (
            <span className="error" style={{ color: "var(--crit)", marginRight: "auto", fontSize: 12.5 }}>
              {error}
            </span>
          )}
          <button className="btn" type="button" onClick={closeDialog}>
            {t("common.cancel")}
          </button>
          <button
            className="btn primary"
            type="submit"
            form="host-form"
            disabled={busy || !f.address.trim() || !f.user.trim()}
          >
            {editing ? t("form.save") : t("form.add")}
          </button>
        </>
      }
    >
      <form id="host-form" className="stack" onSubmit={submit} style={{ gap: 14 }}>
        <div className="grid-3">
          <Field label={t("form.address")}>
            <input
              className="input"
              value={f.address}
              placeholder={t("form.addressPh")}
              onChange={(e) => set("address", e.target.value)}
              spellCheck={false}
              autoFocus
            />
          </Field>
          <Field label={t("form.port")}>
            <input
              className="input"
              type="number"
              min={1}
              max={65535}
              value={f.port}
              onChange={(e) => set("port", Number(e.target.value))}
            />
          </Field>
        </div>
        <div className="grid-2">
          <Field label={t("form.user")}>
            <input className="input" value={f.user} onChange={(e) => set("user", e.target.value)} spellCheck={false} />
          </Field>
          <Field
            label={
              <>
                {t("form.name")} <span className="faint">({t("common.optional")})</span>
              </>
            }
          >
            <input
              className="input"
              value={f.name}
              placeholder={f.address || "web-01"}
              onChange={(e) => set("name", e.target.value)}
            />
          </Field>
        </div>

        <Field label={t("form.auth")}>
          <Segmented<AuthMethod>
            value={f.auth}
            onChange={(v) => set("auth", v)}
            options={[
              { value: "key", label: t("form.authKey") },
              { value: "password", label: t("form.authPassword") },
              ...(caps.agent ? [{ value: "agent" as const, label: t("form.authAgent") }] : []),
            ]}
          />
        </Field>

        {f.auth === "key" && (
          <Field label={t("form.key")}>
            <div className="row">
              <select
                className="select"
                value={f.keyId ?? ""}
                onChange={(e) => set("keyId", e.target.value)}
                disabled={!keys.length}
              >
                {!keys.length && <option value="">{t("form.noKeys")}</option>}
                {keys.map((k) => (
                  <option key={k.id} value={k.id}>
                    {k.name} · {k.fingerprint.slice(7, 19)}…
                  </option>
                ))}
              </select>
              <button className="btn" type="button" onClick={createKey} disabled={creatingKey}>
                {creatingKey ? <KeyRound size={14} className="spin" /> : <Plus size={14} />}
                {t("form.createKey")}
              </button>
            </div>
          </Field>
        )}
        {f.auth === "password" && (
          <Field label={t("form.password")} hint={editing ? t("form.passwordEdit") : undefined}>
            <input
              className="input"
              type="password"
              value={f.password ?? ""}
              onChange={(e) => set("password", e.target.value)}
              autoComplete="off"
            />
            <label className="check" style={{ marginTop: 4 }}>
              <input
                type="checkbox"
                checked={!!f.rememberPassword}
                onChange={(e) => set("rememberPassword", e.target.checked)}
              />
              {t("form.remember")}
            </label>
          </Field>
        )}
        {f.auth === "agent" && (
          <div className="hint muted" style={{ fontSize: 12.5, marginTop: -6 }}>
            {t("form.agentHint")}
          </div>
        )}

        <div className="grid-2">
          <Field label={t("form.group")}>
            <input
              className="input"
              list="tw-groups"
              value={f.group}
              placeholder={t("form.groupPh")}
              onChange={(e) => set("group", e.target.value)}
            />
            <datalist id="tw-groups">
              {groups.map((g) => (
                <option key={g} value={g} />
              ))}
            </datalist>
          </Field>
          <Field label={t("form.tags")}>
            <input
              className="input"
              value={tags}
              placeholder={t("form.tagsPh")}
              onChange={(e) => setTags(e.target.value)}
            />
          </Field>
        </div>

        {jumpCandidates.length > 0 && (
          <Field label={t("form.jump")}>
            <select className="select" value={f.jumpHostId ?? ""} onChange={(e) => set("jumpHostId", e.target.value)}>
              <option value="">{t("form.jumpNone")}</option>
              {jumpCandidates.map((h) => (
                <option key={h.id} value={h.id}>
                  {h.name} ({h.user}@{h.address})
                </option>
              ))}
            </select>
          </Field>
        )}

        <div className="row" style={{ alignItems: "flex-start", gap: 14, padding: "4px 0" }}>
          <div style={{ flex: 1 }}>
            <div style={{ fontWeight: 540 }}>{t("form.monitor")}</div>
            <div className="muted" style={{ fontSize: 12.5 }}>
              {t("form.monitorHint")}
            </div>
          </div>
          <Switch on={f.monitor} onChange={(v) => set("monitor", v)} label={t("form.monitor")} />
        </div>

        <Field
          label={
            <>
              {t("form.notes")} <span className="faint">({t("common.optional")})</span>
            </>
          }
        >
          <textarea
            className="textarea"
            rows={2}
            value={f.notes ?? ""}
            onChange={(e) => set("notes", e.target.value)}
          />
        </Field>
      </form>
    </Modal>
  );
}
