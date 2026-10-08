// One clipboard for every shell: Electron goes through IPC to the main process
// (the sandboxed renderer may not read the clipboard by itself), mobile and
// the browser ask navigator.clipboard, which the platform can refuse.

export async function writeClipboard(text: string): Promise<void> {
  if (window.termward) await window.termward.copy(text);
  else await navigator.clipboard.writeText(text);
}

/** Clipboard text. Rejects when the platform refuses: the caller says so. */
export async function readClipboard(): Promise<string> {
  if (window.termward?.readClipboard) return (await window.termward.readClipboard()) ?? "";
  if (!navigator.clipboard?.readText) throw new Error("clipboard read is not available");
  return navigator.clipboard.readText();
}
