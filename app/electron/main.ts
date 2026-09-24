import fs from "node:fs";
import path from "node:path";
import type { ChildProcess } from "node:child_process";
import {
  app,
  BrowserWindow,
  clipboard,
  dialog,
  ipcMain,
  Menu,
  nativeImage,
  nativeTheme,
  Notification,
  shell,
  Tray,
} from "electron";
import { startCore, stopCore, type CoreInfo } from "./core";

const DEV_URL = process.env.VITE_DEV_SERVER_URL;
const APP_ID = "io.github.nguyenquocanhz.termward";

// Colors of the custom title bar; must match --bg in src/styles/tokens.css.
const TITLEBAR = {
  light: { color: "#faf9f5", symbolColor: "#3d3d3a" },
  dark: { color: "#262624", symbolColor: "#c2c0b6" },
};

let win: BrowserWindow | null = null;
let tray: Tray | null = null;
let core: ChildProcess | null = null;
let coreInfo: CoreInfo | null = null;
let quitting = false;
let toldAboutTray = false;

// Small desktop-only preferences (the core owns everything else).
interface Prefs {
  closeToTray: boolean;
}
const prefsFile = () => path.join(app.getPath("userData"), "desktop-prefs.json");
let prefs: Prefs = { closeToTray: true };

function loadPrefs(): void {
  try {
    prefs = { ...prefs, ...JSON.parse(fs.readFileSync(prefsFile(), "utf8")) };
  } catch {
    /* first run */
  }
}

function savePrefs(): void {
  try {
    fs.writeFileSync(prefsFile(), JSON.stringify(prefs, null, 2));
  } catch {
    /* read-only profile: keep the in-memory value */
  }
}

function quitApp(): void {
  quitting = true;
  app.quit();
}

function assetPath(name: string): string {
  return path.join(__dirname, "..", "assets", name);
}

function showWindow(): void {
  if (!win) {
    createWindow();
    return;
  }
  if (win.isMinimized()) win.restore();
  win.show();
  win.focus();
}

function createWindow(): void {
  const dark = nativeTheme.shouldUseDarkColors;
  const mac = process.platform === "darwin";
  win = new BrowserWindow({
    width: 1320,
    height: 840,
    minWidth: 960,
    minHeight: 600,
    show: false,
    title: "Termward",
    icon: assetPath("icon.png"),
    backgroundColor: dark ? TITLEBAR.dark.color : TITLEBAR.light.color,
    titleBarStyle: "hidden",
    ...(mac
      ? { trafficLightPosition: { x: 16, y: 16 } }
      : { titleBarOverlay: { ...(dark ? TITLEBAR.dark : TITLEBAR.light), height: 44 } }),
    webPreferences: {
      preload: path.join(__dirname, "preload.js"),
      contextIsolation: true,
      sandbox: true,
      nodeIntegration: false,
      spellcheck: false,
    },
  });

  win.once("ready-to-show", () => win?.show());

  // The UI never navigates away or opens windows; links go to the browser.
  win.webContents.setWindowOpenHandler(({ url }) => {
    openExternal(url);
    return { action: "deny" };
  });
  win.webContents.on("will-navigate", (e, url) => {
    if (!DEV_URL || !url.startsWith(DEV_URL)) e.preventDefault();
  });

  // By default × keeps Termward monitoring in the tray (Settings can change it).
  win.on("close", (e) => {
    if (quitting) return;
    if (!prefs.closeToTray) {
      quitApp();
      return;
    }
    e.preventDefault();
    win?.hide();
    if (!toldAboutTray && Notification.isSupported()) {
      toldAboutTray = true;
      new Notification({
        title: "Termward is still watching your servers",
        body: "It keeps running in the tray. Use Quit in the tray menu to stop it.",
        silent: true,
      }).show();
    }
  });
  win.on("closed", () => (win = null));

  if (DEV_URL) void win.loadURL(DEV_URL);
  else void win.loadFile(path.join(__dirname, "..", "dist", "index.html"));
}

function createTray(): void {
  const img = nativeImage.createFromPath(assetPath(process.platform === "darwin" ? "trayTemplate.png" : "tray.png"));
  tray = new Tray(img);
  tray.setToolTip("Termward");
  tray.setContextMenu(
    Menu.buildFromTemplate([
      { label: "Open Termward", click: showWindow },
      { type: "separator" },
      { label: "Quit Termward", click: quitApp },
    ]),
  );
  tray.on("click", showWindow);
}

function openExternal(url: string): void {
  if (/^https?:\/\//i.test(url)) void shell.openExternal(url);
}

function registerIpc(): void {
  ipcMain.handle("core:info", () => coreInfo);

  ipcMain.on("notify", (_e, n: { title: string; body: string; hostId?: string }) => {
    if (!Notification.isSupported()) return;
    const note = new Notification({ title: String(n.title), body: String(n.body) });
    note.on("click", () => {
      showWindow();
      if (n.hostId) win?.webContents.send("notification:click", n.hostId);
    });
    note.show();
  });

  ipcMain.handle("dialog:pickKey", async () => {
    const res = await dialog.showOpenDialog(win!, {
      title: "Import SSH private key",
      defaultPath: path.join(app.getPath("home"), ".ssh"),
      properties: ["openFile", "showHiddenFiles"],
    });
    return res.canceled ? null : res.filePaths[0];
  });

  ipcMain.on("app:quit", quitApp);
  ipcMain.handle("prefs:closeToTray", () => prefs.closeToTray);
  ipcMain.on("prefs:setCloseToTray", (_e, on: boolean) => {
    prefs.closeToTray = !!on;
    savePrefs();
  });

  ipcMain.handle("clipboard:write", (_e, text: string) => clipboard.writeText(String(text)));
  ipcMain.on("open-external", (_e, url: string) => openExternal(String(url)));

  ipcMain.on("theme:changed", (_e, mode: "light" | "dark") => {
    if (process.platform !== "darwin" && win) {
      win.setTitleBarOverlay({ ...(mode === "dark" ? TITLEBAR.dark : TITLEBAR.light), height: 44 });
    }
  });
}

if (!app.requestSingleInstanceLock()) {
  app.quit();
} else {
  app.on("second-instance", showWindow);
  app.setAppUserModelId(APP_ID); // required for notifications on Windows

  app.whenReady().then(async () => {
    loadPrefs();
    registerIpc();
    // No menu bar on Windows/Linux (the title bar is custom, and default
    // accelerators like Ctrl+W/Ctrl+R would close or reload the app). macOS
    // keeps the standard menus so Cmd+C/V/Q work.
    Menu.setApplicationMenu(
      process.platform === "darwin"
        ? Menu.buildFromTemplate([{ role: "appMenu" }, { role: "editMenu" }, { role: "windowMenu" }])
        : null,
    );
    try {
      const started = await startCore(
        path.join(app.getPath("userData"), "core"),
        path.join(app.getPath("userData"), "logs", "core.log"),
      );
      core = started.child;
      coreInfo = started.info;
    } catch (err) {
      dialog.showErrorBox("Termward could not start", String(err));
      app.exit(1);
      return;
    }
    core.once("exit", (code) => {
      if (quitting) return;
      dialog.showErrorBox(
        "Termward core stopped",
        `The background service exited (code ${code}). Termward will close.`,
      );
      quitting = true;
      app.quit();
    });

    createWindow();
    createTray();
  });

  app.on("activate", showWindow); // macOS dock click
  app.on("before-quit", () => {
    quitting = true;
    stopCore(core);
  });
  // Keep running in the tray when every window is closed.
  app.on("window-all-closed", () => {});
}
