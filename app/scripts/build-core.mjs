// Builds the Go core into ../core/bin. Honors GOOS/GOARCH for cross builds
// (the core is pure Go, no cgo, so every target builds from any machine).
import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const coreDir = path.resolve(here, "..", "..", "core");
const { version } = JSON.parse(readFileSync(path.resolve(here, "..", "package.json"), "utf8"));

const goos = process.env.GOOS || process.platform.replace("win32", "windows");
const out = path.join(coreDir, "bin", goos === "windows" ? "termwardd.exe" : "termwardd");

execFileSync(
  "go",
  ["build", "-trimpath", "-ldflags", `-s -w -X main.version=${version}`, "-o", out, "./cmd/termwardd"],
  { cwd: coreDir, stdio: "inherit", env: { ...process.env, CGO_ENABLED: "0" } },
);
console.log(`core built: ${out}`);
