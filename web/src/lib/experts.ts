// Where Strata's expert work happened while generating, from the numbers it
// reports per request (capture.ServerStats).
import type { ServerStats } from './api'

export interface ExpertShares {
  /** Done from the VRAM cache. */
  vram: number
  /** Read over PCIe. */
  pcie: number
  /** Missed the VRAM cache. */
  missed: number
}

/**
 * Splits the expert work into shares that add up to 1. Strata's hit rate is
 * a share of the work not read over PCIe, so it is scaled down by that share.
 * Null for other servers.
 */
export function expertShares(s: ServerStats | null | undefined): ExpertShares | null {
  if (s?.expert_hit_rate == null) return null
  const pcie = s.pcie_share ?? 0
  const vram = (1 - pcie) * s.expert_hit_rate
  return { vram, pcie, missed: Math.max(0, 1 - pcie - vram) }
}
