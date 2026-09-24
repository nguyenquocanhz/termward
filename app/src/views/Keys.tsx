import { useState } from "react";
import { useShallow } from "zustand/react/shallow";
import { Copy, Download, KeyRound, Lock, Pencil, Plus, Send, Trash2 } from "lucide-react";
import { api, type Key } from "../lib/api";
import { useT } from "../lib/i18n";
import { ago, keyTypeLabel } from "../lib/format";
import { copyText } from "../lib/util";
import { confirmAction, errorText, openDialog, refreshKeys, toast, useApp } from "../store";

export function Keys() {
  const t = useT();
  const { keys, hosts } = useApp(useShallow((s) => ({ keys: s.keys, hosts: s.hosts })));

  return (
    <div className="page">
      <div className="page-head">
        <div>
          <h1>{t("nav.keys")}</h1>
          <p>{t("keys.subtitle")}</p>
        </div>
        <div className="actions">
          <button className="btn" onClick={() => openDialog({ kind: "importKey" })}>
            <Download size={15} />
            {t("keys.import")}
          </button>
          <button className="btn primary" onClick={() => openDialog({ kind: "generateKey" })}>
            <Plus size={15} />
            {t("keys.generate")}
          </button>
        </div>
      </div>

      {keys.length === 0 ? (
        <div className="card empty">
          <div className="empty-art">
            <KeyRound size={26} />
          </div>
          <h3>{t("keys.empty")}</h3>
          <p>{t("keys.emptyBody")}</p>
          <div className="actions">
            <button className="btn primary" onClick={() => openDialog({ kind: "generateKey" })}>
              <Plus size={15} />
              {t("keys.generate")}
            </button>
            <button className="btn" onClick={() => openDialog({ kind: "importKey" })}>
              <Download size={15} />
              {t("keys.import")}
            </button>
          </div>
        </div>
      ) : (
        <div className="card">
          {keys.map((k) => (
            <KeyRow
              key={k.id}
              k={k}
              usedBy={hosts.filter((h) => h.auth === "key" && h.keyId === k.id).map((h) => h.name)}
            />
          ))}
        </div>
      )}
    </div>
  );
}

function KeyRow({ k, usedBy }: { k: Key; usedBy: string[] }) {
  const t = useT();
  const [editing, setEditing] = useState(false);
  const [name, setName] = useState(k.name);

  const rename = async () => {
    setEditing(false);
    if (name.trim() && name !== k.name) {
      try {
        await api.renameKey(k.id, name);
        await refreshKeys();
      } catch (e) {
        toast("error", errorText(e));
        setName(k.name);
      }
    }
  };

  const remove = () =>
    confirmAction({
      title: t("keys.confirmDelete", { name: k.name }),
      body: t("keys.confirmDeleteBody"),
      confirm: t("common.delete"),
      danger: true,
      run: async () => {
        await api.deleteKey(k.id);
        await refreshKeys();
      },
    });

  return (
    <div className="key-card">
      <div className="key-icon">
        <KeyRound size={17} />
      </div>
      <div style={{ minWidth: 0 }}>
        <div className="row" style={{ gap: 8 }}>
          {editing ? (
            <input
              className="input"
              style={{ height: 28, maxWidth: 260 }}
              value={name}
              autoFocus
              onChange={(e) => setName(e.target.value)}
              onBlur={rename}
              onKeyDown={(e) => {
                if (e.key === "Enter") void rename();
                if (e.key === "Escape") {
                  setName(k.name);
                  setEditing(false);
                }
              }}
            />
          ) : (
            <strong className="truncate">{k.name}</strong>
          )}
          <span className="badge">{keyTypeLabel(k.type, k.bits)}</span>
          {k.encrypted && (
            <span className="badge accent">
              <Lock size={11} />
              {t("keys.encrypted")}
            </span>
          )}
        </div>
        <div className="mono muted truncate selectable" style={{ fontSize: 12, marginTop: 3 }}>
          {k.fingerprint}
        </div>
        <div className="muted" style={{ fontSize: 12, marginTop: 2 }}>
          {usedBy.length ? t("keys.usedBy", { n: usedBy.join(", ") }) : t("keys.unused")} ·{" "}
          {t("keys.created", { ago: ago(k.createdAt) })}
        </div>
      </div>
      <div className="row" style={{ gap: 4 }}>
        <button className="btn sm" onClick={() => void copyText(k.publicKey, t("common.copied"))}>
          <Copy size={13} />
          {t("keys.copyPublic")}
        </button>
        <button className="btn sm" onClick={() => openDialog({ kind: "deployKey", keyId: k.id })}>
          <Send size={13} />
          {t("keys.deploy")}
        </button>
        <button className="icon-btn" title={t("keys.rename")} onClick={() => setEditing(true)}>
          <Pencil size={14} />
        </button>
        <button className="icon-btn" title={t("common.delete")} onClick={remove}>
          <Trash2 size={14} />
        </button>
      </div>
    </div>
  );
}
