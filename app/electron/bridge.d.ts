// Shape of window.termward: provided by electron/preload.ts on desktop and by
// src/lib/mobile.ts on Android/iOS. Absent in a plain browser (npm run
// dev:web), where src/lib/api.ts falls back to the ?core=&token= parameters.

/** An app-update event forwarded from the Electron main process. */
type UpdateEvent =
  | { type: "available"; version: string; notes?: string; canInstall: boolean; url: string }
  | { type: "none" }
  | { type: "progress"; percent: number }
  | { type: "ready"; version?: string }
  | { type: "error"; message: string };

/** Desktop auto-update (electron-updater). Absent on mobile and in the browser. */
interface UpdateBridge {
  /** Ask whether a newer release exists. `manual` surfaces "up to date" / errors. */
  check(manual: boolean): Promise<void>;
  /** Download the pending update (or open the download page where self-install is impossible). */
  download(): Promise<void>;
  /** Quit and install a downloaded update (or open the download page). */
  install(): void;
  /** Open the GitHub releases page in the browser. */
  openDownload(): void;
  /** Subscribe to update events; returns an unsubscribe function. */
  on(cb: (ev: UpdateEvent) => void): () => void;
}

interface TermwardBridge {
  platform: string; // "win32" | "darwin" | "linux" | "android" | "ios"
  coreInfo(): Promise<{ url: string; wsUrl: string; token: string } | null>;
  notify(n: { title: string; body: string; hostId?: string }): void;
  onNotificationClick(cb: (hostId: string) => void): () => void;
  pickKeyFile(): Promise<string | null>;
  /** Desktop: pick an SSH config or known_hosts file to import from. */
  pickSshFile?(kind: "config" | "known_hosts"): Promise<string | null>;
  copy(text: string): Promise<void>;
  /** Desktop: clipboard text. Elsewhere the UI asks navigator.clipboard. */
  readClipboard?(): Promise<string>;
  openExternal(url: string): void;
  setTheme(mode: "light" | "dark"): void;
  /** Desktop: quit for real (the window's × may only hide to the tray). */
  quit?(): void;
  /** Desktop: whether × hides to the tray (true) or quits (false). */
  getCloseToTray?(): Promise<boolean>;
  setCloseToTray?(on: boolean): void;
  /** Mobile: keep monitoring while the app is in the background. */
  getBackground?(): Promise<boolean>;
  setBackground?(on: boolean): Promise<void>;
  /** Mobile: language for the notifications the core posts natively. */
  setLanguage?(lang: string): void;
  /** Desktop: app auto-update over GitHub Releases. */
  update?: UpdateBridge;
}

interface Window {
  termward?: TermwardBridge;
}
