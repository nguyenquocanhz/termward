// Which shell is the UI running in? Electron (desktop), Capacitor (mobile), or
// a plain browser during development.

export function platform(): string {
  return window.termward?.platform ?? "web";
}

export function isMobile(): boolean {
  const p = platform();
  return p === "android" || p === "ios";
}

export function isDesktop(): boolean {
  return !!window.termward && !isMobile();
}

/** Features that need a desktop filesystem or ssh-agent. */
export const caps = {
  get sshDir() {
    return !isMobile(); // ~/.ssh scan and ~/.ssh/config import
  },
  get agent() {
    return !isMobile();
  },
  get fileImport() {
    return isDesktop();
  },
};

/** Installs window.termward on mobile (Electron's preload provides it on desktop). */
export async function installBridge(): Promise<void> {
  if (window.termward) return;
  const { Capacitor } = await import("@capacitor/core");
  if (!Capacitor.isNativePlatform()) return;
  const { installMobileBridge } = await import("./mobile");
  await installMobileBridge();
}
