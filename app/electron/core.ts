import { spawn, type ChildProcess } from "node:child_process";
import { randomBytes } from "node:crypto";
import fs from "node:fs";
import path from "node:path";
import readline from "node:readline";
import { app } from "electron";

export interface CoreInfo {
  url: string;
  wsUrl: string;
  token: string;
}

export function coreBinary(): string {
  const exe = process.platform === "win32" ? "termwardd.exe" : "termwardd";
  return app.isPackaged
    ? path.join(process.resourcesPath, "core", exe)
    : path.join(__dirname, "..", "..", "core", "bin", exe);
}

/**
 * Starts the Go core with a fresh random token and resolves once it reports
 * the port it listens on. The core exits by itself when our stdin pipe closes,
 * so it never outlives the app, even after a crash.
 */
export function startCore(dataDir: string, logFile: string): Promise<{ info: CoreInfo; child: ChildProcess }> {
  const token = randomBytes(32).toString("hex");
  fs.mkdirSync(path.dirname(logFile), { recursive: true });
  const log = fs.createWriteStream(logFile, { flags: "a" });
  log.write(`\n--- ${new Date().toISOString()} starting ${coreBinary()}\n`);

  const child = spawn(coreBinary(), ["--data-dir", dataDir, "--watch-stdin"], {
    env: { ...process.env, TERMWARD_TOKEN: token },
    stdio: ["pipe", "pipe", "pipe"],
    windowsHide: true,
  });
  child.stderr?.on("data", (d: Buffer) => log.write(d));
  child.once("exit", () => log.end());

  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => {
      child.kill();
      reject(new Error(`core did not start within 15 seconds (see ${logFile})`));
    }, 15_000);
    const lines = readline.createInterface({ input: child.stdout! });
    lines.on("line", (line) => {
      const m = /^TERMWARD_READY (\d+)$/.exec(line.trim());
      if (!m) return;
      clearTimeout(timer);
      const port = Number(m[1]);
      resolve({
        info: { url: `http://127.0.0.1:${port}`, wsUrl: `ws://127.0.0.1:${port}`, token },
        child,
      });
    });
    child.once("error", (err) => {
      clearTimeout(timer);
      reject(err);
    });
    child.once("exit", (code) => {
      clearTimeout(timer);
      reject(new Error(`core exited with code ${code} (see ${logFile})`));
    });
  });
}

export function stopCore(child: ChildProcess | null): void {
  if (!child || child.exitCode !== null) return;
  child.stdin?.end(); // graceful: the core watches stdin
  setTimeout(() => {
    if (child.exitCode === null) child.kill();
  }, 2000).unref();
}
