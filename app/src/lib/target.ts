// How a session is named: `user@address`, the way ssh itself spells it.

interface Target {
  user: string;
  address: string;
  port: number;
}

/** `user@address`, with `:port` only when it is not 22 and IPv6 in brackets. */
export function hostTarget(h: Target): string {
  const v6 = h.address.includes(":");
  const addr = v6 ? `[${h.address}]` : h.address;
  return `${h.user}@${addr}${h.port && h.port !== 22 ? `:${h.port}` : ""}`;
}

/** The OpenSSH command that opens the same session. */
export function sshCommand(h: Target): string {
  return `ssh ${h.user}@${h.address} -p ${h.port || 22}`;
}
