import type { Host } from "./api";
import { errorText, toast } from "../store";
import { writeClipboard } from "./clipboard";

export async function copyText(text: string, okMessage: string) {
  try {
    await writeClipboard(text);
    toast("success", okMessage);
  } catch (e) {
    toast("error", errorText(e));
  }
}

/** Host → the shape the create/update endpoints take. */
export function stripHost(h: Host) {
  const { id: _id, createdAt: _c, updatedAt: _u, ...rest } = h;
  return rest;
}
