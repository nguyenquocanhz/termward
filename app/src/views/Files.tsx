import { useCallback, useEffect, useRef, useState, type CSSProperties } from "react";
import {
  ArrowUp,
  Download,
  File as FileIcon,
  FileText,
  Folder,
  FolderPlus,
  Home,
  Link2,
  Loader,
  MoreVertical,
  Pencil,
  RefreshCw,
  Trash2,
  Upload,
  WifiOff,
} from "lucide-react";
import { api, type FileEntry } from "../lib/api";
import { useT } from "../lib/i18n";
import { ago, bytesKb } from "../lib/format";
import { confirmAction, ensureConnected, errorText, navigate, toast, useApp } from "../store";
import { Field, Menu, Modal, type MenuItem } from "../components/ui";
import { openContextMenu } from "../components/ContextMenu";

const linkBtn: CSSProperties = {
  background: "none",
  border: 0,
  padding: 0,
  font: "inherit",
  color: "var(--accent-ink)",
  cursor: "pointer",
};

const joinPath = (dir: string, name: string) => (dir === "/" ? "/" + name : dir + "/" + name);

function iconFor(e: FileEntry) {
  if (e.isDir) return <Folder size={16} style={{ color: "var(--accent)", flex: "none" }} />;
  if (e.symlink) return <Link2 size={16} className="muted" style={{ flex: "none" }} />;
  const text = /\.(txt|md|log|conf|cfg|ini|sh|ya?ml|json|xml|ts|tsx|js|go|py|rb|rs|c|h|env|toml|csv)$/i.test(e.name);
  return text ? (
    <FileText size={16} className="muted" style={{ flex: "none" }} />
  ) : (
    <FileIcon size={16} className="muted" style={{ flex: "none" }} />
  );
}

export function FilesView({ hostId }: { hostId: string }) {
  const t = useT();
  const host = useApp((s) => s.hosts.find((h) => h.id === hostId));

  const [path, setPath] = useState("");
  const [entries, setEntries] = useState<FileEntry[] | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [notConnected, setNotConnected] = useState(false);
  const [busy, setBusy] = useState(false);
  const [dragging, setDragging] = useState(false);
  const [modal, setModal] = useState<{ kind: "mkdir" } | { kind: "rename"; entry: FileEntry } | null>(null);
  const fileInput = useRef<HTMLInputElement>(null);

  const load = useCallback(
    async (p?: string) => {
      setLoading(true);
      setError(null);
      setNotConnected(false);
      try {
        if (!(await ensureConnected(hostId))) {
          setNotConnected(true);
          setEntries(null);
          return;
        }
        const res = await api.files.list(hostId, p);
        setPath(res.path);
        setEntries(res.entries);
      } catch (e) {
        setError(errorText(e));
        setEntries(null);
      } finally {
        setLoading(false);
      }
    },
    [hostId],
  );

  useEffect(() => {
    void load();
  }, [load]);

  const segs = path.split("/").filter(Boolean);
  const atRoot = path === "" || path === "/";
  const enter = (e: FileEntry) => e.isDir && void load(e.path);
  const up = () => void load(path.replace(/\/[^/]*$/, "") || "/");

  const doDownload = async (e: FileEntry) => {
    try {
      await api.files.download(hostId, e.path);
    } catch (err) {
      toast("error", t("err.generic"), errorText(err));
    }
  };

  const doDelete = (e: FileEntry) =>
    confirmAction({
      title: t(e.isDir ? "files.confirmDeleteDir" : "files.confirmDeleteFile", { name: e.name }),
      body: t(e.isDir ? "files.confirmDeleteDirBody" : "files.confirmDeleteFileBody"),
      confirm: t("common.delete"),
      danger: true,
      run: async () => {
        try {
          await api.files.remove(hostId, e.path, e.isDir);
          toast("success", t("files.deleted"));
          await load(path);
        } catch (err) {
          toast("error", t("err.generic"), errorText(err));
        }
      },
    });

  const submitModal = async (name: string) => {
    const n = name.trim();
    if (!n || !modal) return;
    try {
      if (modal.kind === "mkdir") {
        await api.files.mkdir(hostId, joinPath(path, n));
        toast("success", t("files.created"));
      } else {
        await api.files.rename(hostId, modal.entry.path, joinPath(path, n));
        toast("success", t("files.renamed"));
      }
      setModal(null);
      await load(path);
    } catch (err) {
      toast("error", t("err.generic"), errorText(err));
    }
  };

  const doUpload = async (files: File[]) => {
    setBusy(true);
    const done: string[] = [];
    try {
      for (const f of files) {
        await api.files.upload(hostId, joinPath(path, f.name), f);
        done.push(f.name);
      }
      if (done.length === 1) toast("success", t("files.uploadedOne", { name: done[0] }));
      else if (done.length > 1) toast("success", t("files.uploadedMany", { n: done.length }));
      await load(path);
    } catch (err) {
      toast("error", t("err.generic"), errorText(err));
    } finally {
      setBusy(false);
    }
  };

  const upload = (list: FileList | File[]) => {
    const files = Array.from(list);
    if (!files.length) return;
    const existing = new Set((entries ?? []).map((e) => e.name));
    const clash = files.filter((f) => existing.has(f.name));
    if (clash.length) {
      confirmAction({
        title: t("files.overwrite", {
          name: clash.length === 1 ? clash[0].name : t("files.items", { n: clash.length }),
        }),
        body: t("files.overwriteBody"),
        confirm: t("files.upload"),
        run: () => doUpload(files),
      });
    } else {
      void doUpload(files);
    }
  };

  const rowMenu = (e: FileEntry): MenuItem[] => [
    e.isDir
      ? { label: t("files.open"), icon: <Folder size={14} />, run: () => enter(e) }
      : { label: t("files.download"), icon: <Download size={14} />, run: () => void doDownload(e) },
    { label: t("files.rename"), icon: <Pencil size={14} />, run: () => setModal({ kind: "rename", entry: e }) },
    { label: t("common.delete"), icon: <Trash2 size={14} />, danger: true, run: () => doDelete(e) },
  ];

  return (
    <div className="page">
      <div className="page-head">
        <div style={{ minWidth: 0 }}>
          <div className="row" style={{ gap: 10 }}>
            <h1 className="truncate">{t("files.title")}</h1>
            {host && (
              <button style={linkBtn} className="truncate" onClick={() => navigate({ name: "host", hostId })}>
                {host.name}
              </button>
            )}
          </div>
          <p className="truncate mono" style={{ marginTop: 2 }}>
            <button style={linkBtn} onClick={() => void load("/")} aria-label={t("files.home")}>
              /
            </button>
            {segs.map((seg, i) => (
              <span key={i}>
                {i > 0 && <span className="faint"> / </span>}
                <button style={linkBtn} onClick={() => void load("/" + segs.slice(0, i + 1).join("/"))}>
                  {seg}
                </button>
              </span>
            ))}
          </p>
        </div>
        <div className="actions">
          <button className="icon-btn" title={t("files.up")} onClick={up} disabled={atRoot || loading}>
            <ArrowUp size={16} />
          </button>
          <button className="icon-btn" title={t("files.home")} onClick={() => void load()} disabled={loading}>
            <Home size={16} />
          </button>
          <button className="icon-btn" title={t("files.refresh")} onClick={() => void load(path)} disabled={loading}>
            <RefreshCw size={16} className={loading ? "spin" : ""} />
          </button>
          <button className="btn" onClick={() => setModal({ kind: "mkdir" })} disabled={!entries}>
            <FolderPlus size={14} />
            {t("files.newFolder")}
          </button>
          <button className="btn primary" onClick={() => fileInput.current?.click()} disabled={!entries || busy}>
            <Upload size={15} />
            {t("files.upload")}
          </button>
        </div>
      </div>

      <input
        ref={fileInput}
        type="file"
        multiple
        hidden
        onChange={(e) => {
          if (e.target.files) upload(e.target.files);
          e.target.value = "";
        }}
      />

      {notConnected ? (
        <div className="callout warn" style={{ marginTop: 16 }}>
          <WifiOff size={16} />
          <div className="grow">{t("files.notConnected")}</div>
          <button className="btn sm" onClick={() => void load(path)}>
            {t("files.connect")}
          </button>
        </div>
      ) : error ? (
        <div className="callout crit" style={{ marginTop: 16 }}>
          <WifiOff size={16} />
          <div className="grow selectable">{error}</div>
          <button className="btn sm" onClick={() => void load(path)}>
            {t("common.retry")}
          </button>
        </div>
      ) : loading && !entries ? (
        <div className="card empty" style={{ marginTop: 16, padding: 36 }}>
          <Loader size={18} className="spin" />
          <div style={{ marginTop: 8 }}>{t("files.loading")}</div>
        </div>
      ) : (
        <div
          className="card"
          style={{ marginTop: 16, position: "relative", outline: dragging ? "2px dashed var(--accent)" : undefined }}
          onDragOver={(e) => {
            e.preventDefault();
            setDragging(true);
          }}
          onDragLeave={(e) => e.currentTarget === e.target && setDragging(false)}
          onDrop={(e) => {
            e.preventDefault();
            setDragging(false);
            if (e.dataTransfer?.files) upload(e.dataTransfer.files);
          }}
        >
          {entries && entries.length === 0 ? (
            <div className="empty" style={{ padding: 36 }}>
              {t("files.empty")}
            </div>
          ) : (
            <table className="table">
              <thead>
                <tr>
                  <th>{t("files.name")}</th>
                  <th style={{ textAlign: "right", width: 120 }}>{t("files.size")}</th>
                  <th style={{ width: 150 }}>{t("files.modified")}</th>
                  <th style={{ width: 44 }} aria-label="actions" />
                </tr>
              </thead>
              <tbody>
                {entries?.map((e) => (
                  <tr
                    key={e.path}
                    onContextMenu={(ev) => openContextMenu(ev, rowMenu(e))}
                    onDoubleClick={() => (e.isDir ? enter(e) : void doDownload(e))}
                  >
                    <td>
                      <button
                        style={{
                          ...linkBtn,
                          color: "inherit",
                          display: "flex",
                          alignItems: "center",
                          gap: 8,
                          maxWidth: "100%",
                        }}
                        onClick={() => (e.isDir ? enter(e) : void doDownload(e))}
                      >
                        {iconFor(e)}
                        <span className="truncate">{e.name}</span>
                        {e.symlink && <span className="faint">· {t("files.symlink")}</span>}
                      </button>
                    </td>
                    <td className="muted" style={{ textAlign: "right", fontVariantNumeric: "tabular-nums" }}>
                      {e.isDir ? "—" : bytesKb(e.size / 1024)}
                    </td>
                    <td className="muted" title={new Date(e.mtime * 1000).toLocaleString()}>
                      {ago(e.mtime * 1000)}
                    </td>
                    <td style={{ textAlign: "right" }}>
                      <Menu label={e.name} icon={<MoreVertical size={16} />} items={rowMenu(e)} />
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
          {dragging && (
            <div
              style={{
                position: "absolute",
                inset: 0,
                display: "flex",
                flexDirection: "column",
                alignItems: "center",
                justifyContent: "center",
                gap: 8,
                background: "color-mix(in srgb, var(--bg) 82%, transparent)",
                borderRadius: 12,
                pointerEvents: "none",
                fontWeight: 540,
              }}
            >
              <Upload size={22} style={{ color: "var(--accent)" }} />
              {t("files.dropHere")}
            </div>
          )}
        </div>
      )}

      {entries && entries.length > 0 && (
        <p className="muted" style={{ marginTop: 8, fontSize: 12.5 }}>
          {t("files.items", { n: entries.length })}
        </p>
      )}

      {modal && (
        <NameModal
          kind={modal.kind}
          initial={modal.kind === "rename" ? modal.entry.name : ""}
          onClose={() => setModal(null)}
          onSubmit={submitModal}
        />
      )}
    </div>
  );
}

function NameModal({
  kind,
  initial,
  onClose,
  onSubmit,
}: {
  kind: "mkdir" | "rename";
  initial: string;
  onClose: () => void;
  onSubmit: (name: string) => void;
}) {
  const t = useT();
  const [name, setName] = useState(initial);
  const submit = () => name.trim() && onSubmit(name);
  return (
    <Modal
      title={t(kind === "mkdir" ? "files.newFolder" : "files.rename")}
      onClose={onClose}
      footer={
        <>
          <button className="btn" onClick={onClose}>
            {t("common.cancel")}
          </button>
          <button className="btn primary" onClick={submit} disabled={!name.trim()}>
            {t(kind === "mkdir" ? "files.create" : "files.rename")}
          </button>
        </>
      }
    >
      <Field label={t(kind === "mkdir" ? "files.newFolderName" : "files.newName")}>
        <input
          value={name}
          onChange={(e) => setName(e.target.value)}
          onKeyDown={(e) => e.key === "Enter" && submit()}
        />
      </Field>
    </Modal>
  );
}
