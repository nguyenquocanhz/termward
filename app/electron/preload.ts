import { contextBridge, ipcRenderer, type IpcRendererEvent } from "electron";

// The only surface the renderer gets: no Node, no filesystem, no shell.
contextBridge.exposeInMainWorld("termward", {
  platform: process.platform,
  coreInfo: () => ipcRenderer.invoke("core:info"),
  notify: (n: { title: string; body: string; hostId?: string }) => ipcRenderer.send("notify", n),
  onNotificationClick: (cb: (hostId: string) => void) => {
    const listener = (_e: IpcRendererEvent, hostId: string) => cb(hostId);
    ipcRenderer.on("notification:click", listener);
    return () => {
      ipcRenderer.removeListener("notification:click", listener);
    };
  },
  pickKeyFile: () => ipcRenderer.invoke("dialog:pickKey"),
  pickSshFile: (kind: "config" | "known_hosts") => ipcRenderer.invoke("dialog:pickSshFile", kind),
  copy: (text: string) => ipcRenderer.invoke("clipboard:write", text),
  readClipboard: () => ipcRenderer.invoke("clipboard:read"),
  openExternal: (url: string) => ipcRenderer.send("open-external", url),
  setTheme: (mode: "light" | "dark") => ipcRenderer.send("theme:changed", mode),
  quit: () => ipcRenderer.send("app:quit"),
  getCloseToTray: () => ipcRenderer.invoke("prefs:closeToTray"),
  setCloseToTray: (on: boolean) => ipcRenderer.send("prefs:setCloseToTray", on),
  update: {
    check: (manual: boolean) => ipcRenderer.invoke("update:check", manual),
    download: () => ipcRenderer.invoke("update:download"),
    install: () => ipcRenderer.send("update:install"),
    openDownload: () => ipcRenderer.send("update:openDownload"),
    on: (cb: (ev: { type: string; [k: string]: unknown }) => void) => {
      const channels: Record<string, string> = {
        "update:available": "available",
        "update:none": "none",
        "update:progress": "progress",
        "update:ready": "ready",
        "update:error": "error",
      };
      const unsubs = Object.entries(channels).map(([channel, type]) => {
        const listener = (_e: IpcRendererEvent, data: Record<string, unknown> | undefined) =>
          cb({ type, ...(data ?? {}) });
        ipcRenderer.on(channel, listener);
        return () => ipcRenderer.removeListener(channel, listener);
      });
      return () => unsubs.forEach((f) => f());
    },
  },
});
