import { privacy } from './privacy.svelte'
import { setModelOrder } from './modelColors.svelte'

// Polls /notus/api/status every 2 seconds for the status bar. The server
// answers from values its own monitors keep, so polling is cheap.

export interface GPU {
  card: string
  pci?: string
  name?: string
  watts: number | null
  power_cap_watts?: number
  temp_c: number | null
  vram_used: number | null
  vram_total: number | null
  busy_percent: number | null
  integrated: boolean
  power_label?: string
}

export interface RunningModel {
  model: string
  name?: string
  description?: string
  state: 'stopped' | 'starting' | 'ready' | 'stopping' | string
  unlisted?: boolean
  context_length?: number
  aliases?: string[]
}

export interface Status {
  version: string
  llama_swap: { up: boolean; error?: string; models: RunningModel[]; known: RunningModel[]; checked_at: number }
  gpus: GPU[]
  total_watts: number | null
  in_flight: number
  cpu_watts: number | null
  cpu_problem?: string
  cpu_source?: 'rapl' | 'ppt'
  system_watts: number | null
  system_base_watts: number
  psu_efficiency: number
  ram_used: number | null
  ram_total: number | null
  privacy: { hidden_models: string[]; show_hidden: boolean }
  /** A newer release was seen on the server's last check (every 15 minutes). */
  update_available: boolean
}

/**
 * AMD built-in graphics whose power sensor is labelled PPT report the whole
 * CPU socket. That is CPU power, so it stays out of GPU totals.
 */
export const isSocketSensor = (g: GPU) => g.integrated && g.power_label === 'PPT'

class StatusPoller {
  current = $state<Status | null>(null)
  /** False when notus-swap itself did not answer the last poll. */
  reachable = $state(true)
  private timer?: ReturnType<typeof setInterval>

  start() {
    if (this.timer) return
    const poll = async () => {
      try {
        const res = await fetch('/notus/api/status')
        if (!res.ok) throw new Error(String(res.status))
        const next: Status = await res.json()
        privacy.apply(next.privacy)
        setModelOrder(next.llama_swap.known.map((m) => m.model))
        this.current = next
        this.reachable = true
      } catch {
        this.reachable = false
      }
    }
    poll()
    this.timer = setInterval(poll, 2000)
  }
}

export const status = new StatusPoller()
