// Renders the Termward icon (a terminal prompt ">_" on a clay tile) into PNGs
// with signed-distance shapes, so no image tooling is needed.
import { existsSync, mkdirSync, writeFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import zlib from "node:zlib";

const outDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..", "assets");
mkdirSync(outDir, { recursive: true });

// ---- geometry in a 512×512 design space
const tile = { cx: 256, cy: 256, half: 232, r: 112 };
const strokes = [
  [150, 170, 250, 256],
  [250, 256, 150, 342],
  [282, 342, 370, 342],
];
const strokeWidth = 44;

function sdRoundRect(x, y) {
  const qx = Math.abs(x - tile.cx) - tile.half + tile.r;
  const qy = Math.abs(y - tile.cy) - tile.half + tile.r;
  return Math.hypot(Math.max(qx, 0), Math.max(qy, 0)) + Math.min(Math.max(qx, qy), 0) - tile.r;
}

function sdSegment(x, y, [ax, ay, bx, by]) {
  const px = x - ax,
    py = y - ay,
    dx = bx - ax,
    dy = by - ay;
  const h = Math.max(0, Math.min(1, (px * dx + py * dy) / (dx * dx + dy * dy)));
  return Math.hypot(px - dx * h, py - dy * h) - strokeWidth / 2;
}

const glyph = (x, y) => Math.min(...strokes.map((s) => sdSegment(x, y, s)));
const cover = (d, pxSize) => Math.max(0, Math.min(1, 0.5 - d / pxSize));

const top = [0xdb, 0x7f, 0x5f]; // clay, lighter at the top
const bottom = [0xc2, 0x5e, 0x3d];

// shape: "rounded" (desktop), "circle" (Android round icon), "square" (iOS, full bleed)
function tileDistance(x, y, shape) {
  if (shape === "circle") return Math.hypot(x - tile.cx, y - tile.cy) - tile.half;
  if (shape === "square") return -1;
  return sdRoundRect(x, y);
}

function render(size, opts = {}) {
  return encodePng(size, size, renderRaw(size, opts));
}

function renderRaw(size, { withTile = true, mono = false, shape = "rounded" } = {}) {
  const px = 512 / size;
  const data = Buffer.alloc(size * size * 4);
  for (let j = 0; j < size; j++) {
    for (let i = 0; i < size; i++) {
      const x = (i + 0.5) * px,
        y = (j + 0.5) * px;
      const g = cover(glyph(x, y), px);
      let r, gg, b, a;
      if (withTile) {
        const t = y / 512;
        const tileA = cover(tileDistance(x, y, shape), px);
        r = (top[0] + (bottom[0] - top[0]) * t) * (1 - g) + 255 * g;
        gg = (top[1] + (bottom[1] - top[1]) * t) * (1 - g) + 255 * g;
        b = (top[2] + (bottom[2] - top[2]) * t) * (1 - g) + 255 * g;
        a = tileA;
      } else {
        r = gg = b = mono ? 0 : 255;
        a = g;
      }
      const o = (j * size + i) * 4;
      data[o] = Math.round(r);
      data[o + 1] = Math.round(gg);
      data[o + 2] = Math.round(b);
      data[o + 3] = Math.round(a * 255);
    }
  }
  return data;
}

// ---- minimal PNG encoder (RGBA, no filtering)
const crcTable = new Int32Array(256).map((_, n) => {
  let c = n;
  for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
  return c;
});
function crc32(buf) {
  let c = -1;
  for (const byte of buf) c = crcTable[(c ^ byte) & 0xff] ^ (c >>> 8);
  return (c ^ -1) >>> 0;
}
function chunk(type, body) {
  const len = Buffer.alloc(4);
  len.writeUInt32BE(body.length);
  const tb = Buffer.concat([Buffer.from(type, "ascii"), body]);
  const crc = Buffer.alloc(4);
  crc.writeUInt32BE(crc32(tb));
  return Buffer.concat([len, tb, crc]);
}
function encodePng(w, h, rgba) {
  const ihdr = Buffer.alloc(13);
  ihdr.writeUInt32BE(w, 0);
  ihdr.writeUInt32BE(h, 4);
  ihdr[8] = 8; // bit depth
  ihdr[9] = 6; // RGBA
  const raw = Buffer.alloc((w * 4 + 1) * h);
  for (let y = 0; y < h; y++) rgba.copy(raw, y * (w * 4 + 1) + 1, y * w * 4, (y + 1) * w * 4);
  return Buffer.concat([
    Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
    chunk("IHDR", ihdr),
    chunk("IDAT", zlib.deflateSync(raw, { level: 9 })),
    chunk("IEND", Buffer.alloc(0)),
  ]);
}

const files = {
  "icon.png": render(1024),
  "tray.png": render(32),
  "tray@2x.png": render(64),
  "trayTemplate.png": render(18, { withTile: false, mono: true }),
  "trayTemplate@2x.png": render(36, { withTile: false, mono: true }),
  "logo-64.png": render(64),
};
for (const [name, png] of Object.entries(files)) writeFileSync(path.join(outDir, name), png);
console.log(`icons written to ${outDir}`);

// Native mobile projects, when they exist.
const appDir = path.resolve(outDir, "..");
const androidRes = path.join(appDir, "android", "app", "src", "main", "res");
if (existsSync(androidRes)) {
  // Legacy launcher icons for Android 7.x (8+ uses the adaptive XML icon).
  const densities = { mdpi: 48, hdpi: 72, xhdpi: 96, xxhdpi: 144, xxxhdpi: 192 };
  for (const [d, s] of Object.entries(densities)) {
    const dir = path.join(androidRes, `mipmap-${d}`);
    mkdirSync(dir, { recursive: true });
    writeFileSync(path.join(dir, "ic_launcher.png"), render(s));
    writeFileSync(path.join(dir, "ic_launcher_round.png"), render(s, { shape: "circle" }));
  }
  console.log(`android icons written to ${androidRes}`);
}
const iosIcons = path.join(appDir, "ios", "App", "App", "Assets.xcassets", "AppIcon.appiconset");
if (existsSync(iosIcons)) {
  // iOS masks the corners itself and rejects transparency.
  writeFileSync(path.join(iosIcons, "AppIcon-512@2x.png"), render(1024, { shape: "square" }));
  // Launch screen: warm paper background with the icon in the middle.
  const splashDir = path.join(iosIcons, "..", "Splash.imageset");
  const png = splash(2732, 360, [0xfa, 0xf9, 0xf5]);
  for (const f of ["splash-2732x2732.png", "splash-2732x2732-1.png", "splash-2732x2732-2.png"]) {
    writeFileSync(path.join(splashDir, f), png);
  }
  console.log(`ios icon and splash written to ${iosIcons}/..`);
}

function splash(size, iconSize, bg) {
  const data = Buffer.alloc(size * size * 4);
  for (let i = 0; i < size * size; i++) data.set([bg[0], bg[1], bg[2], 255], i * 4);
  const icon = renderRaw(iconSize);
  const off = Math.floor((size - iconSize) / 2);
  for (let j = 0; j < iconSize; j++) {
    for (let i = 0; i < iconSize; i++) {
      const s = (j * iconSize + i) * 4;
      const d = ((j + off) * size + (i + off)) * 4;
      const a = icon[s + 3] / 255;
      for (let c = 0; c < 3; c++) data[d + c] = Math.round(icon[s + c] * a + data[d + c] * (1 - a));
    }
  }
  return encodePng(size, size, data);
}
