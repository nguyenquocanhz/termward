// Live-metrics view for the UI. Merges the high-resolution live stream (the
// "metric" events, kept as a ring in the store) with the 30s health poll, and
// decides when a host should show the "live" affordance. Owned by the UI; the
// stream's transport and the per-host sample ring are produced by the core.

import { useEffect, useReducer } from "react";
import { useApp, type LiveState } from "../store";
import type { Point, Status } from "./api";

/**
 * A stream is "live" while fresh ticks keep arriving. Four missed 2s ticks
 * (the loop's floor interval) drops the live affordance back to the poll, so a
 * stopped stream, a Windows host, or a disconnect falls back on its own.
 */
export const LIVE_STALE_MS = 8000;

export function liveActive(ls: LiveState | undefined, now: number = Date.now()): boolean {
  return !!ls && ls.points.length > 0 && now - ls.at * 1000 < LIVE_STALE_MS;
}

/** The fast metrics a host currently shows, from the live stream or the poll. */
export interface MetricView {
  /** Show the live indicator (a stream is delivering fresh points). */
  live: boolean;
  /** Epoch seconds of the latest live point (for the age next to the dot). */
  at?: number;
  /** Ring for the sparklines: live points when live, else the 30s history. */
  points: Point[];
  /** -1 when unknown (CPU needs two samples; a host with no poll data yet). */
  cpu: number;
  mem: number;
  memUsedKb: number;
  memTotalKb: number;
  load: [number, number, number];
  swapUsedKb: number;
  swapTotalKb: number;
  procsRunning?: number;
  cores: number;
}

/**
 * Live view of one host. Subscribes to its live ring and re-evaluates on a slow
 * timer so the live flag ages out even when ticks simply stop arriving. Falls
 * back to the host's poll `sample`/`history` whenever no live stream is active.
 */
export function useHostMetrics(hostId: string, status?: Status): MetricView {
  const ls = useApp((s) => s.live[hostId]);
  const [, retick] = useReducer((n: number) => n + 1, 0);
  useEffect(() => {
    if (!ls) return;
    const id = window.setInterval(retick, 2000);
    return () => window.clearInterval(id);
  }, [ls]);

  const sample = status?.sample;

  if (liveActive(ls) && ls) {
    const last = ls.points[ls.points.length - 1];
    const m = ls.latest;
    // Prefer the full tick; on a freshly seeded ring fall back to the last
    // point (cpu/mem/load) and the poll sample for the rest.
    return {
      live: true,
      at: ls.at,
      points: ls.points,
      cpu: m ? (m.cpu ?? -1) : (last?.cpu ?? -1),
      mem: m ? m.mem : (last?.mem ?? sample?.memPercent ?? -1),
      memUsedKb: m ? m.memUsedKb : (sample?.memUsedKb ?? 0),
      memTotalKb: m ? m.memTotalKb : (sample?.memTotalKb ?? 0),
      load: m ? [m.load1, m.load5, m.load15] : [last?.load ?? 0, sample?.load[1] ?? 0, sample?.load[2] ?? 0],
      swapUsedKb: m ? m.swapUsedKb : (sample?.swapUsedKb ?? 0),
      swapTotalKb: m ? m.swapTotalKb : (sample?.swapTotalKb ?? 0),
      procsRunning: m?.procsRunning,
      cores: m ? m.cores : (sample?.cpus ?? 0),
    };
  }

  // No live stream: everything comes from the 30s poll.
  return {
    live: false,
    points: status?.history ?? [],
    cpu: sample?.cpuPercent ?? -1,
    mem: sample?.memPercent ?? -1,
    memUsedKb: sample?.memUsedKb ?? 0,
    memTotalKb: sample?.memTotalKb ?? 0,
    load: sample?.load ?? [0, 0, 0],
    swapUsedKb: sample?.swapUsedKb ?? 0,
    swapTotalKb: sample?.swapTotalKb ?? 0,
    cores: sample?.cpus ?? 0,
  };
}
