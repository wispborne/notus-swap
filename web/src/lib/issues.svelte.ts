// Formats output issues detected by internal/capture/issues.go and manages per-model muting.
import type { Issue, IssueNumbers } from './api'
import { api } from './serverPath'
import { modelView } from './servers.svelte'

/** Every kind, in the order the filter lists them. */
export const issueKinds: [string, string][] = [
  ['token_limit', 'Token limit'],
  ['context_full', 'Context full'],
  ['thinking_budget', 'Thinking used the budget'],
  ['server_limit', "Server's output limit"],
  ['cut_off', 'Cut off, cause unknown'],
  ['context_error', 'Prompt too long'],
  ['context_near_full', 'Context nearly full'],
  ['thinking_loop', 'Thinking repeated itself'],
]

export interface Described {
  title: string
  /** What happened, with the numbers. */
  what: string
  fix: string
  /** Whether the fix is on the Config page. */
  config: boolean
}

const n = (v: number | undefined) => (v ?? 0).toLocaleString('en-US')

export function describe(issue: Issue): Described {
  const d: IssueNumbers = issue.detail
  const slotsNote =
    d.slots && d.slots > 1 ? ` This model has ${d.slots} parallel slots (--parallel), which can split the context between them.` : ''
  switch (issue.kind) {
    case 'token_limit':
      return {
        title: 'Cut off by the token limit',
        what: `Stopped at ${n(d.output)} tokens. That is the limit this request set (max_tokens).`,
        fix: 'Raise max_tokens in the app that sent it.',
        config: false,
      }
    case 'server_limit':
      return {
        title: "Cut off by the server's output limit",
        what: `Stopped at ${n(d.output)} tokens. The request set no limit, so llama-server used its default of ${n(d.server_limit)}.`,
        fix: "Raise or remove -n (--n-predict) in this model's command, or set max_tokens in the request.",
        config: true,
      }
    case 'context_full':
      return {
        title: 'Cut off because the context was full',
        what: `Prompt ${n(d.prompt)} + output ${n(d.output)} = ${n((d.prompt ?? 0) + (d.output ?? 0))} tokens. The context holds ${n(d.n_ctx)}.`,
        fix: `Raise --ctx-size for this model, or start a new conversation.${slotsNote}`,
        config: true,
      }
    case 'cut_off':
      return d.n_ctx
        ? {
            title: 'Cut off before finishing',
            what: `Stopped at ${n(d.output)} tokens. That is below the context size (${n(d.n_ctx)}) and any known token limit, so the cause is unclear.`,
            fix: "Check this model's command for other limits, such as -n.",
            config: true,
          }
        : {
            title: 'Cut off before finishing',
            what: `Stopped at ${n(d.output)} tokens. This model's context size isn't known, so the cause is unclear.`,
            fix: 'Load this model once. notus-swap then reads its context size and checks this request again.',
            config: false,
          }
    case 'thinking_budget': {
      const cause: Record<string, [string, string, boolean]> = {
        token_limit: [` The limit was this request's max_tokens (${n(d.limit)}).`, 'Raise max_tokens, or lower the reasoning effort.', false],
        context_full: [
          ` The context was full (${n((d.prompt ?? 0) + (d.output ?? 0))} of ${n(d.n_ctx)} tokens).`,
          'Raise --ctx-size, or lower the reasoning effort.',
          true,
        ],
        server_limit: [
          ` The limit was llama-server's default output limit (${n(d.server_limit)}).`,
          "Raise -n in this model's command, or lower the reasoning effort.",
          true,
        ],
      }
      const [why, fix, config] = cause[d.cause ?? ''] ?? ['', 'Lower the reasoning effort, or raise the token limit.', false]
      return {
        title: 'Thinking used the whole budget',
        what: `The model was still thinking when it stopped at ${n(d.output)} tokens, so there is no answer.${why}`,
        fix,
        config,
      }
    }
    case 'context_error':
      return {
        title: 'Prompt too long for the context',
        what:
          d.prompt && d.n_ctx
            ? `The prompt is ${n(d.prompt)} tokens and the context holds ${n(d.n_ctx)}, so llama-server refused it.`
            : (d.message ?? 'llama-server refused the prompt as too long.'),
        fix: `Raise --ctx-size for this model, or shorten the conversation.${slotsNote}`,
        config: true,
      }
    case 'context_near_full':
      return {
        title: 'Context nearly full',
        what:
          `The prompt used ${n(d.prompt)} of ${n(d.n_ctx)} tokens, leaving ${n(d.room)} for the answer.` +
          (d.limit ? ` The request asked for up to ${n(d.limit)}.` : ''),
        fix: 'The next message in this conversation may be cut off or refused. Raise --ctx-size, or start a new conversation.',
        config: true,
      }
    case 'thinking_loop': {
      const share = d.repeat ? ` At Strata's last check, ${Math.round(d.repeat * 100)}% of the recent thinking was in passages repeated three times.` : ''
      return {
        title: 'Thinking repeated itself',
        what: d.restarts
          ? `Strata found the thinking going in circles and restarted it ${d.restarts === 1 ? 'once' : `${n(d.restarts)} times`}, with a higher temperature and presence penalty.${share}`
          : `Strata found the thinking going in circles.${share} It counts 25% or more as a loop.`,
        fix: "Lower the reasoning effort, or raise the temperature or presence penalty. Strata's reasoning_loop_recovery setting decides whether it restarts the thinking or ends the answer.",
        config: false,
      }
    }
    default:
      return { title: issue.kind, what: '', fix: '', config: false }
  }
}

interface Mute {
  kind: string
  model: string
}

class IssueMutes {
  /** Goes up after each change, so pages showing issues fetch them again. */
  version = $state(0)

  /**
   * Mutes or unmutes one kind of issue for one model. Mutes are saved under
   * the model's ID, and cover its aliases too. Each server keeps the mutes
   * for its own models; server is the one that runs the model ('' for this
   * one).
   */
  async set(kind: string, model: string, mute: boolean, server = '') {
    const id = modelView(server).id
    model = id(model)
    // Read the current list first, so a change made on another device stays.
    const res = await fetch(api(server, '/notus/api/settings/issue_mutes'))
    const list: Mute[] = ((await res.json()) ?? []).filter((m: Mute) => !(m.kind === kind && id(m.model) === model))
    if (mute) list.push({ kind, model })
    await fetch(api(server, '/notus/api/settings/issue_mutes'), { method: 'PUT', body: JSON.stringify(list) })
    this.version++
  }
}

export const issueMutes = new IssueMutes()
