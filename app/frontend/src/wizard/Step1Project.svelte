<script lang="ts">
  import { _ } from 'svelte-i18n';
  import Section from '../lib/ui/Section.svelte';
  import Field from '../lib/ui/Field.svelte';
  import Button from '../lib/ui/Button.svelte';
  import StepNav from '../lib/ui/StepNav.svelte';
  import Badge from '../lib/ui/Badge.svelte';
  import { wizardStore } from '../stores/wizard';
  import { api } from '../lib/api';

  let mode = $state<'new' | 'resume'>($wizardStore.mode);
  let busy = $state(false);
  let fetchedDir = $state<string | null>($wizardStore.contentDir);
  let fetchErr = $state('');
  let runs = $state<any[]>([]);

  async function loadRuns() {
    try { runs = (await api.listRuns()) as any[]; }
    catch { runs = []; }
  }
  loadRuns();

  async function fetchContent() {
    busy = true; fetchErr = '';
    try {
      const dir = await api.fetchContent($wizardStore.inventory.content.repo, $wizardStore.inventory.content.ref);
      fetchedDir = dir;
      wizardStore.update((s) => ({ ...s, contentDir: dir }));
    } catch (e) {
      fetchErr = String(e);
    } finally { busy = false; }
  }

  function setMode(m: 'new' | 'resume') {
    mode = m;
    wizardStore.update((s) => ({ ...s, mode: m }));
  }

  // Always allow Next — content fetch is optional at this stage, the rest of the
  // wizard works with the in-memory inventory until Step 5 (Plan).
  const canAdvance = true;
</script>

<header class="step-header">
  <h2>{$_('step.1.title')}</h2>
  <p>{$_('step.1.subtitle')}</p>
</header>

<div class="grid">
  <button class="mode-card" class:active={mode === 'new'} onclick={() => setMode('new')}>
    <strong>{$_('step1.modeNew')}</strong>
    <span>{$_('step1.modeNewDesc')}</span>
  </button>
  <button class="mode-card" class:active={mode === 'resume'} onclick={() => setMode('resume')}>
    <strong>{$_('step1.modeResume')}</strong>
    <span>{$_('step1.modeResumeDesc')}</span>
  </button>
</div>

{#if mode === 'new'}
  <Section title={$_('step1.contentRepo')} subtitle={$_('step1.contentRepoHint')}>
    <Field label={$_('step1.contentRepo')} hint="https://github.com/...">
      <input bind:value={$wizardStore.inventory.content.repo} />
    </Field>
    <Field label={$_('step1.contentTag')} hint={$_('step1.contentTagHint')} required>
      <input bind:value={$wizardStore.inventory.content.ref} placeholder="v0.1.0" />
    </Field>
    <div class="row">
      <Button variant="primary" disabled={busy} onclick={fetchContent}>
        {busy ? $_('common.loading') : $_('step1.fetchContent')}
      </Button>
      {#if fetchedDir}
        <Badge tone="success">{$_('step1.fetched')} {fetchedDir}</Badge>
      {/if}
      {#if fetchErr}
        <Badge tone="danger">{fetchErr}</Badge>
      {/if}
    </div>
  </Section>
{:else}
  <Section title={$_('step1.modeResume')}>
    {#if runs.length === 0}
      <p class="muted">{$_('step1.noRuns')}</p>
    {:else}
      <table>
        <thead>
          <tr><th>ID</th><th>Cluster</th><th>Stage</th><th>Updated</th><th></th></tr>
        </thead>
        <tbody>
          {#each runs as r}
            <tr>
              <td><code>{r.id?.slice(0, 8)}…</code></td>
              <td>{r.cluster}</td>
              <td><Badge tone="info">{r.stage}</Badge></td>
              <td>{r.updated_at}</td>
              <td>
                <Button onclick={() => wizardStore.update((s) => ({ ...s, runId: r.id }))}>
                  {$_('common.start')}
                </Button>
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    {/if}
  </Section>
{/if}

<StepNav canAdvance={canAdvance} />

<style>
  .step-header { margin-bottom: 1.25rem; }
  .step-header h2 { margin: 0; font-size: 1.3rem; }
  .step-header p { margin: 0.25rem 0 0; color: #a1a1aa; font-size: 0.9rem; }
  .grid { display: grid; grid-template-columns: 1fr 1fr; gap: 1rem; margin-bottom: 1.25rem; }
  .mode-card { display: flex; flex-direction: column; gap: 0.4rem; align-items: flex-start;
               padding: 1rem 1.25rem; border-radius: 8px; cursor: pointer;
               background: #1b1b1f; border: 1px solid #2a2a30; color: inherit;
               text-align: left; font-family: inherit; transition: border-color 0.1s; }
  .mode-card:hover { border-color: #52525b; }
  .mode-card.active { border-color: #3b82f6; background: #1e293b; }
  .mode-card strong { font-size: 0.95rem; }
  .mode-card span { font-size: 0.8rem; color: #a1a1aa; }
  .row { display: flex; gap: 0.75rem; align-items: center; }
  .muted { color: #71717a; font-size: 0.85rem; }
  table { width: 100%; border-collapse: collapse; font-size: 0.85rem; }
  th, td { padding: 0.5rem; text-align: left; border-bottom: 1px solid #2a2a30; }
  th { color: #a1a1aa; font-weight: 500; }
  code { background: #27272a; padding: 0.1rem 0.4rem; border-radius: 3px; }
</style>
