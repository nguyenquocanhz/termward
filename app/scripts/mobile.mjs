// Mobile build helper.
//
//   node scripts/mobile.mjs core android   gomobile → android/app/libs/termwardcore.aar
//   node scripts/mobile.mjs core ios       gomobile → native/ios-core/Termwardcore.xcframework (macOS)
//   node scripts/mobile.mjs web            UI build for Capacitor (no desktop CSP) + cap sync
//
// gomobile must be installed: go install golang.org/x/mobile/cmd/gomobile@latest && gomobile init
import { execFileSync } from "node:child_process";
import { mkdirSync, readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const appDir = path.resolve(here, "..");
const coreDir = path.resolve(appDir, "..", "core");
const { version } = JSON.parse(readFileSync(path.join(appDir, "package.json"), "utf8"));
const run = (cmd, args, opts = {}) => execFileSync(cmd, args, { stdio: "inherit", ...opts });
// npx is a .cmd shim on Windows, which needs a shell.
const npx = (args, opts = {}) =>
  execFileSync(process.platform === "win32" ? "npx.cmd" : "npx", args, {
    stdio: "inherit",
    shell: process.platform === "win32",
    ...opts,
  });

const [what, target] = process.argv.slice(2);

if (what === "core") {
  const ldflags = `-s -w -X github.com/nguyenquocanhz/termward/core/mobile.version=${version}`;
  if (target === "android") {
    const out = path.join(appDir, "android", "app", "libs", "termwardcore.aar");
    mkdirSync(path.dirname(out), { recursive: true });
    run(
      "gomobile",
      [
        "bind",
        "-target=android",
        "-androidapi",
        "24",
        "-javapkg=io.github.nguyenquocanhz.termward",
        "-ldflags",
        ldflags,
        "-o",
        out,
        "./mobile",
      ],
      { cwd: coreDir },
    );
    console.log(`built ${out}`);
  } else if (target === "ios") {
    // Consumed by the local Swift package native/ios-core (see its Package.swift).
    const out = path.join(appDir, "native", "ios-core", "Termwardcore.xcframework");
    run("gomobile", ["bind", "-target=ios,iossimulator", "-ldflags", ldflags, "-o", out, "./mobile"], {
      cwd: coreDir,
    });
    console.log(`built ${out}`);
  } else {
    console.error("usage: mobile.mjs core android|ios");
    process.exit(2);
  }
} else if (what === "web") {
  npx(["vite", "build"], { cwd: appDir, env: { ...process.env, TERMWARD_TARGET: "mobile" } });
  npx(["cap", "sync"], { cwd: appDir });
} else {
  console.error("usage: mobile.mjs core android|ios | web");
  process.exit(2);
}
