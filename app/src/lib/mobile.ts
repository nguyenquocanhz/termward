import { Capacitor, registerPlugin, type PluginListenerHandle } from "@capacitor/core";
import { App } from "@capacitor/app";
import { StatusBar, Style } from "@capacitor/status-bar";

/** Native side: android/.../TermwardCorePlugin.java and ios/App/App/TermwardCorePlugin.swift. */
interface TermwardCorePlugin {
  start(opts: { language: string }): Promise<{ port: number; token: string }>;
  setLanguage(opts: { language: string }): Promise<void>;
  getBackground(): Promise<{ enabled: boolean }>;
  setBackground(opts: { enabled: boolean }): Promise<void>;
  requestPermissions(): Promise<{ notifications: string }>;
  addListener(event: "notificationTap", cb: (e: { hostId: string }) => void): Promise<PluginListenerHandle>;
}

const Core = registerPlugin<TermwardCorePlugin>("TermwardCore");

export async function installMobileBridge(): Promise<void> {
  const language = navigator.language.toLowerCase().startsWith("vi") ? "vi" : "en";
  const { port, token } = await Core.start({ language });
  const url = `http://127.0.0.1:${port}`;

  const taps = new Set<(hostId: string) => void>();
  void Core.addListener("notificationTap", (e) => taps.forEach((cb) => cb(e.hostId)));
  // Alerts are posted natively by the core, so ask for permission up front.
  void Core.requestPermissions().catch(() => {});

  window.termward = {
    platform: Capacitor.getPlatform(),
    coreInfo: async () => ({ url, wsUrl: url.replace(/^http/, "ws"), token }),
    // The core notifies natively (works while the WebView is paused), so the
    // UI must not post a second copy.
    notify: () => {},
    onNotificationClick: (cb) => {
      taps.add(cb);
      return () => taps.delete(cb);
    },
    pickKeyFile: async () => null,
    copy: (text) => navigator.clipboard.writeText(text),
    // Capacitor hands navigation to foreign origins to the system browser.
    openExternal: (u) => {
      if (/^https?:\/\//i.test(u)) window.location.href = u;
    },
    setTheme: (mode) => {
      void StatusBar.setStyle({ style: mode === "dark" ? Style.Dark : Style.Light }).catch(() => {});
      if (Capacitor.getPlatform() === "android") {
        void StatusBar.setBackgroundColor({ color: mode === "dark" ? "#262624" : "#faf9f5" }).catch(() => {});
      }
    },
    getBackground: async () => (await Core.getBackground()).enabled,
    setBackground: (on) => Core.setBackground({ enabled: on }),
    setLanguage: (lang) => void Core.setLanguage({ language: lang }).catch(() => {}),
  };
}

/** Android back button: close what's on top, then go back to Overview, then leave. */
export function handleBackButton(handler: () => boolean): void {
  void App.addListener("backButton", () => {
    if (!handler()) void App.minimizeApp();
  });
}
