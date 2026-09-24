// Shape of window.termward: provided by electron/preload.ts on desktop and by
// src/lib/mobile.ts on Android/iOS. Absent in a plain browser (npm run
// dev:web), where src/lib/api.ts falls back to the ?core=&token= parameters.
interface TermwardBridge {
  platform: string; // "win32" | "darwin" | "linux" | "android" | "ios"
  coreInfo(): Promise<{ url: string; wsUrl: string; token: string } | null>;
  notify(n: { title: string; body: string; hostId?: string }): void;
  onNotificationClick(cb: (hostId: string) => void): () => void;
  pickKeyFile(): Promise<string | null>;
  copy(text: string): Promise<void>;
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
}

interface Window {
  termward?: TermwardBridge;
}
