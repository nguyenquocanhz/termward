import type { Host } from "./api";
import { errorText, toast } from "../store";

export async function copyText(text: string, okMessage: string) {
  try {
    if (window.termward) await window.termward.copy(text);
    else await navigator.clipboard.writeText(text);
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
