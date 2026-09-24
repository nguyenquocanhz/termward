import type { CapacitorConfig } from "@capacitor/cli";

// The mobile shell: same UI (dist/) as the desktop app, talking to the Go core
// that runs inside the app process on http://127.0.0.1 (see android/ and ios/).
const config: CapacitorConfig = {
  appId: "io.github.nguyenquocanhz.termward",
  appName: "Termward",
  webDir: "dist",
  // Serve the UI from http://localhost so calls to the loopback core are not
  // mixed content. Cleartext is limited to localhost by the network config.
  server: { androidScheme: "http" },
  backgroundColor: "#faf9f5",
  plugins: {
    Keyboard: { resize: "native", resizeOnFullScreen: true },
  },
};

export default config;
