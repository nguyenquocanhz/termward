// Runs the Vite dev server and Electron pointed at it (hot reload for the UI).
import { spawn } from "node:child_process";
import { createRequire } from "node:module";
import { createServer } from "vite";

const require = createRequire(import.meta.url);
const electronBin = require("electron");

const server = await createServer();
await server.listen();
const url = server.resolvedUrls.local[0];
console.log(`renderer: ${url}`);

const child = spawn(electronBin, ["."], {
  stdio: "inherit",
  env: { ...process.env, VITE_DEV_SERVER_URL: url },
});
child.on("exit", async (code) => {
  await server.close();
  process.exit(code ?? 0);
});
