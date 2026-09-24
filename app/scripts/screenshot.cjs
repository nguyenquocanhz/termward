// Captures README screenshots from a running UI (npm run dev:web + core --dev).
//
//   npx electron scripts/screenshot.cjs <out.png> <light|dark> [step...]
//
// Each step is a CSS selector to click, or "js:<code>" to run in the page.
// TW_SIZE=390x844 renders a phone-sized window. A window is shown briefly:
// hidden/offscreen windows are not painted reliably.
const { app, BrowserWindow } = require("electron");
const fs = require("node:fs");

const [out, theme = "light", ...steps] = process.argv.slice(2).filter((a) => !a.startsWith("--"));
const url = process.env.TW_URL || "http://localhost:5173/?core=http://127.0.0.1:7717&token=dev";
const [width, height] = (process.env.TW_SIZE || "1320x820").split("x").map(Number);
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// Never hang a script run.
setTimeout(() => {
  console.error("screenshot timed out");
  app.exit(1);
}, 60_000).unref();

app.whenReady().then(async () => {
  const win = new BrowserWindow({
    width,
    height,
    useContentSize: true,
    frame: false,
    webPreferences: { backgroundThrottling: false },
  });
  await win.loadURL(url);
  await win.webContents.executeJavaScript(
    `localStorage.setItem("termward.prefs", JSON.stringify({ theme: ${JSON.stringify(theme)}, lang: "en" }))`,
  );
  await win.loadURL(url);
  await sleep(2500);
  for (const step of steps) {
    const code = step.startsWith("js:") ? step.slice(3) : `document.querySelector(${JSON.stringify(step)})?.click()`;
    await Promise.race([win.webContents.executeJavaScript(code), sleep(5000)]);
    await sleep(2500);
  }
  const img = await win.webContents.capturePage();
  fs.writeFileSync(out, img.toPNG());
  console.log(`saved ${out} (${img.getSize().width}x${img.getSize().height})`);
  app.quit();
});
