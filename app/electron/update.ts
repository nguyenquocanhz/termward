// Auto-update over GitHub Releases (electron-updater). We only ever *propose*
// an update: nothing is downloaded until the user asks, and nothing is
// installed until the user restarts. Windows (NSIS) and Linux AppImage can
// download and install in place; everywhere else (macOS without a signing
// identity, .deb, dev) we only tell the user and open the download page.
import { app, ipcMain, net, shell, type BrowserWindow } from "electron";
// electron-updater is CommonJS with named exports and no default export, so a
// default import compiles to `.default` (undefined). `autoUpdater` is a lazy
// getter — referenced through this named import it is created on first use,
// after the app is ready.
import { autoUpdater } from "electron-updater";

const OWNER = "nguyenquocanhz";
const REPO = "termward";
const RELEASES_PAGE = `https://github.com/${OWNER}/${REPO}/releases/latest`;

let getWin: () => BrowserWindow | null = () => null;

function send(channel: string, data?: unknown): void {
  const w = getWin();
  if (w && !w.isDestroyed()) w.webContents.send(channel, data);
}

/** Where electron-updater can download and install an update by itself. */
function canInstall(): boolean {
  if (process.platform === "win32") return true;
  if (process.platform === "linux") return !!process.env.APPIMAGE; // .deb and the rest: notify only
  return false; // macOS without a paid signing identity cannot self-update
}

/** True when a.b.c is strictly newer than the other (ignores any suffix). */
function isNewer(a: string, b: string): boolean {
  const pa = a.replace(/^v/, "").split(".").map((n) => parseInt(n, 10) || 0);
  const pb = b.replace(/^v/, "").split(".").map((n) => parseInt(n, 10) || 0);
  for (let i = 0; i < 3; i++) {
    if ((pa[i] ?? 0) !== (pb[i] ?? 0)) return (pa[i] ?? 0) > (pb[i] ?? 0);
  }
  return false;
}

async function checkForUpdates(manual: boolean): Promise<void> {
  if (!app.isPackaged) {
    if (manual) send("update:none"); // nothing to update from a dev run
    return;
  }
  try {
    if (canInstall()) {
      await autoUpdater.checkForUpdates(); // emits update-available / update-not-available
      return;
    }
    // macOS (unsigned) and .deb: ask GitHub directly, then only notify + link out.
    const res = await net.fetch(`https://api.github.com/repos/${OWNER}/${REPO}/releases/latest`, {
      headers: { Accept: "application/vnd.github+json" },
    });
    if (!res.ok) throw new Error(`GitHub returned ${res.status}`);
    const rel = (await res.json()) as { tag_name?: string; body?: string };
    const latest = (rel.tag_name ?? "").replace(/^v/, "");
    if (latest && isNewer(latest, app.getVersion())) {
      send("update:available", { version: latest, notes: rel.body, canInstall: false, url: RELEASES_PAGE });
    } else {
      send("update:none");
    }
  } catch (err) {
    send("update:error", { message: String((err as Error)?.message ?? err) });
  }
}

/**
 * Wires electron-updater to the renderer. The renderer decides what to show:
 * it only surfaces "none"/"error" when it asked for them (a manual check or a
 * download in flight), so the quiet startup check never nags.
 */
export function registerUpdates(win: () => BrowserWindow | null, onBeforeInstall?: () => void): void {
  getWin = win;
  autoUpdater.autoDownload = false; // download only when the user accepts
  autoUpdater.autoInstallOnAppQuit = false; // install only when the user restarts
  autoUpdater.allowDowngrade = false;

  autoUpdater.on("update-available", (info) =>
    send("update:available", {
      version: info.version,
      notes: typeof info.releaseNotes === "string" ? info.releaseNotes : undefined,
      canInstall: canInstall(),
      url: RELEASES_PAGE,
    }),
  );
  autoUpdater.on("update-not-available", () => send("update:none"));
  autoUpdater.on("download-progress", (p) => send("update:progress", { percent: Math.round(p.percent) }));
  autoUpdater.on("update-downloaded", (info) => send("update:ready", { version: info.version }));
  autoUpdater.on("error", (err) => send("update:error", { message: String(err?.message ?? err) }));

  ipcMain.handle("update:check", (_e, manual: boolean) => checkForUpdates(!!manual));
  ipcMain.handle("update:download", async () => {
    if (!canInstall()) {
      void shell.openExternal(RELEASES_PAGE);
      return;
    }
    try {
      await autoUpdater.downloadUpdate();
    } catch (err) {
      send("update:error", { message: String((err as Error)?.message ?? err) });
    }
  });
  ipcMain.on("update:install", () => {
    if (!canInstall()) {
      void shell.openExternal(RELEASES_PAGE);
      return;
    }
    onBeforeInstall?.(); // let the shell bypass "close to tray" so the installer can run
    autoUpdater.quitAndInstall();
  });
  ipcMain.on("update:openDownload", () => void shell.openExternal(RELEASES_PAGE));
}

/** A quiet check a few seconds after launch (never surfaces errors by itself). */
export function checkForUpdatesOnStartup(): void {
  setTimeout(() => void checkForUpdates(false), 8000).unref();
}
