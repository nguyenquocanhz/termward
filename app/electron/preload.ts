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
  copy: (text: string) => ipcRenderer.invoke("clipboard:write", text),
  openExternal: (url: string) => ipcRenderer.send("open-external", url),
  setTheme: (mode: "light" | "dark") => ipcRenderer.send("theme:changed", mode),
  quit: () => ipcRenderer.send("app:quit"),
  getCloseToTray: () => ipcRenderer.invoke("prefs:closeToTray"),
  setCloseToTray: (on: boolean) => ipcRenderer.send("prefs:setCloseToTray", on),
});
