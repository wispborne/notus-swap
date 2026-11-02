<script lang="ts">
  import { api } from '../servers.svelte'

  /** The server this form acts on. '' is this server. onsaved runs after a save. */
  let { server = '', onsaved }: { server?: string; onsaved?: () => void } = $props()

  type Kind = '' | 'github' | 'gitea' | 'off'
  interface Saved {
    kind: Kind
    gitea_url: string
    gitea_repo: string
    gitea_user: string
    gitea_token_set: boolean
    github_repo: string
    /** The source the environment gives, or '' when it turns updates off. */
    env_source: string
    source: string
  }

  let saved = $state<Saved | null>(null)
  let error = $state('')
  let busy = $state(false)
  let done = $state(false)
  let kind = $state<Kind>('')
  let githubRepo = $state('')
  let giteaURL = $state('')
  let giteaRepo = $state('')
  let giteaUser = $state('')
  let giteaToken = $state('')

  function fill(s: Saved) {
    saved = s
    kind = s.kind
    githubRepo = s.github_repo || 'wispborne/notus-swap'
    giteaURL = s.gitea_url
    giteaRepo = s.gitea_repo
    giteaUser = s.gitea_user
    giteaToken = ''
  }

  async function load() {
    try {
      const res = await fetch(api(server, '/notus/api/update-source'))
      if (!res.ok) throw new Error((await res.text()).trim() || res.statusText)
      fill(await res.json())
    } catch (e) {
      error = String(e)
    }
  }
  load()

  // The token is kept when it was saved before and the box is left blank.
  const tokenKept = $derived(saved?.kind === 'gitea' && saved.gitea_token_set)

  async function save() {
    busy = true
    error = ''
    try {
      const body =
        kind === 'gitea'
          ? { kind, gitea_url: giteaURL, gitea_repo: giteaRepo, gitea_user: giteaUser, gitea_token: giteaToken }
          : kind === 'github'
            ? { kind, github_repo: githubRepo }
            : { kind }
      const res = await fetch(api(server, '/notus/api/update-source'), { method: 'PUT', body: JSON.stringify(body) })
      if (!res.ok) throw new Error((await res.text()).trim() || res.statusText)
      fill(await res.json())
      done = true
      setTimeout(() => (done = false), 2000)
      onsaved?.()
    } catch (e) {
      error = e instanceof Error ? e.message : String(e)
    } finally {
      busy = false
    }
  }

  const input = 'rounded-md border border-line bg-panel2 px-2 py-1 outline-none focus:border-primary'
  const btn = 'rounded-md border border-line bg-panel2 px-2.5 py-1 hover:border-primary disabled:opacity-50 disabled:hover:border-line'
  const kinds: { value: Kind; label: string }[] = [
    { value: '', label: 'Environment settings' },
    { value: 'github', label: 'GitHub' },
    { value: 'gitea', label: 'Gitea' },
    { value: 'off', label: 'Off' },
  ]
</script>

<div class="mt-3 rounded-md border border-line bg-panel2/40 p-3 text-[12.5px]">
  <h3 class="font-semibold">Where updates come from</h3>
  {#if saved}
    <div class="mt-2 flex flex-wrap gap-x-4 gap-y-1">
      {#each kinds as k}
        <label class="flex cursor-pointer items-center gap-1.5">
          <input type="radio" name="update-source" value={k.value} bind:group={kind} class="accent-primary" />
          {k.label}
        </label>
      {/each}
    </div>

    {#if kind === ''}
      <p class="mt-2 text-muted" title="The GITEA_ and NOTUS_GITHUB_REPO settings notus-swap was started with">
        {#if saved.env_source}Uses {saved.env_source}.{:else}Updates are off.{/if}
      </p>
    {:else if kind === 'github'}
      <label class="mt-2 flex flex-wrap items-center gap-2" title="A GITHUB_TOKEN from the environment is still used, if set.">Repository
        <input class="{input} w-64 font-mono" bind:value={githubRepo} placeholder="owner/name" spellcheck="false" />
      </label>
    {:else if kind === 'gitea'}
      <div class="mt-2 grid max-w-xl grid-cols-[auto_1fr] items-center gap-x-2 gap-y-1.5">
        <span>Address</span><input class="{input} font-mono" bind:value={giteaURL} placeholder="https://git.example.com" spellcheck="false" />
        <span>Repository</span><input class="{input} font-mono" bind:value={giteaRepo} placeholder="owner/notus-swap" spellcheck="false" />
        <span>User</span><input class="{input} font-mono" bind:value={giteaUser} placeholder="owner" spellcheck="false" autocomplete="off" />
        <span title="Stored in notus-swap's database. This page never shows it again.">Token</span>
        <input
          class="{input} font-mono"
          type="password"
          bind:value={giteaToken}
          placeholder={tokenKept ? 'Saved. Leave blank to keep it.' : 'A read-only Gitea token'}
          autocomplete="new-password"
        />
      </div>
    {:else}
      <p class="mt-2 text-muted">No update checks or installs.</p>
    {/if}

    <div class="mt-2 flex items-center gap-2">
      <button class={btn} disabled={busy} onclick={save}>{busy ? 'Saving…' : 'Save'}</button>
      {#if done}<span class="text-primary">{saved.source ? `Saved. Updates now come from ${saved.source}.` : 'Saved. Updates are off.'}</span>{/if}
    </div>
  {:else if !error}
    <p class="mt-1 text-muted">Loading…</p>
  {/if}
  {#if error}<p class="mt-1 text-err">{error}</p>{/if}
</div>
