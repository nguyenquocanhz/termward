import type { MouseEvent } from "react";
import { ClipboardCopy, HardDrive, Pencil, SquareTerminal, Trash2 } from "lucide-react";
import type { Host } from "../lib/api";
import { t } from "../lib/i18n";
import { hostTarget, sshCommand } from "../lib/target";
import { copyText } from "../lib/util";
import { confirmDeleteHost, navigate, openDialog, openTerminal, startHardwareCheck } from "../store";
import { openContextMenu, type ContextItem } from "./ContextMenu";

/** What a right-click on a server offers, wherever the server is shown. */
export function hostMenuItems(host: Host): ContextItem[] {
  const cmd = sshCommand(host);
  return [
    { label: t("card.openTerminal"), icon: <SquareTerminal size={14} />, run: () => void openTerminal(host.id) },
    {
      label: t("ctx.checkHardware"),
      icon: <HardDrive size={14} />,
      run: () => {
        navigate({ name: "hardware", hostId: host.id });
        void startHardwareCheck(host.id);
      },
    },
    { separator: true },
    {
      label: t("ctx.copyAddress"),
      icon: <ClipboardCopy size={14} />,
      run: () => void copyText(host.address, t("common.copied")),
    },
    { label: t("ctx.copySsh", { cmd }), run: () => void copyText(cmd, t("common.copied")) },
    { separator: true },
    { label: t("host.edit"), icon: <Pencil size={14} />, run: () => openDialog({ kind: "host", host }) },
    { label: t("common.delete"), icon: <Trash2 size={14} />, danger: true, run: () => confirmDeleteHost(host) },
  ];
}

export function openHostMenu(e: MouseEvent, host: Host) {
  e.stopPropagation();
  openContextMenu(e, hostMenuItems(host), { label: `${host.name} · ${hostTarget(host)}` });
}
