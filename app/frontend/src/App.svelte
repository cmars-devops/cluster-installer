<script lang="ts">
  import { _, locale } from 'svelte-i18n';
  import { wizardStore } from './stores/wizard';
  import wordmarkSvg from './assets/triangles-logo-white.svg?raw';
  import Step1Project from './wizard/Step1Project.svelte';
  import Step2Target from './wizard/Step2Target.svelte';
  import Step3OS from './wizard/Step3OS.svelte';
  import Step4Inventory from './wizard/Step4Inventory.svelte';
  import Step5Plan from './wizard/Step5Plan.svelte';
  import Step6Run from './wizard/Step6Run.svelte';
  import Step7Result from './wizard/Step7Result.svelte';

  const stepComponents = [Step1Project, Step2Target, Step3OS, Step4Inventory, Step5Plan, Step6Run, Step7Result];
  const Current = $derived(stepComponents[$wizardStore.step] ?? Step1Project);
</script>

<main>
  <header>
    <div class="brand">
      <div class="wordmark" aria-label="Triangles — {$_('app.title')}">{@html wordmarkSvg}</div>
      <p class="subtitle">{$_('app.subtitle')}</p>
    </div>

    <nav class="steps" aria-label="wizard progress">
      {#each Array(7) as _i, idx}
        <button class="step-chip"
                class:active={idx === $wizardStore.step}
                class:done={idx < $wizardStore.step}
                onclick={() => wizardStore.update((s) => ({ ...s, step: idx }))}>
          <span class="num">{idx + 1}</span>
          <span class="label">{$_(`step.${idx + 1}.short`)}</span>
        </button>
      {/each}
    </nav>

    <div class="lang">
      <button class:active={$locale === 'ko'} onclick={() => locale.set('ko')}>한국어</button>
      <button class:active={$locale === 'en'} onclick={() => locale.set('en')}>EN</button>
    </div>
  </header>

  <section class="content">
    <Current />
  </section>

  <footer>
    <span>step {$wizardStore.step + 1} / 7</span>
    {#if $wizardStore.runId}
      <span>run: <code>{$wizardStore.runId}</code></span>
    {/if}
    {#if $wizardStore.contentDir}
      <span>content: <code>{$wizardStore.contentDir}</code></span>
    {/if}
  </footer>
</main>

<style>
  main { display: grid; grid-template-rows: auto 1fr auto; height: 100%; min-height: 100vh; }

  header { display: grid; grid-template-columns: auto 1fr auto;
           gap: 2rem; align-items: center;
           padding: 0.85rem 1.5rem; border-bottom: 1px solid #27272a;
           background: linear-gradient(to bottom, #16161a, #121216); }

  .brand { display: flex; flex-direction: column; gap: 0.15rem;
           align-items: flex-start; }
  .wordmark { display: block; line-height: 0;
              filter: drop-shadow(0 0 10px rgba(0, 117, 194, 0.4)); }
  .wordmark :global(svg) { height: 32px; width: auto; display: block; }
  .subtitle { margin: 0; font-size: 0.72rem; color: #a1a1aa; }

  .steps { display: flex; gap: 0.4rem; justify-content: center; flex-wrap: wrap; }
  .step-chip { display: flex; align-items: center; gap: 0.4rem;
               padding: 0.3rem 0.7rem; border-radius: 999px;
               background: #27272a; border: 1px solid #3f3f46; color: #a1a1aa;
               cursor: pointer; transition: all 0.1s; font-family: inherit;
               font-size: 0.78rem; }
  .step-chip:hover { border-color: #52525b; color: #d4d4d8; }
  .step-chip.active { background: #3b82f6; color: white; border-color: #3b82f6; }
  .step-chip.done { background: #14532d; color: #4ade80; border-color: #15803d; }
  .step-chip .num { font-weight: 600; }
  .step-chip.active .num { color: white; }

  .lang { display: flex; gap: 0.25rem; }
  .lang button { padding: 0.3rem 0.6rem; border-radius: 4px;
                 background: transparent; border: 1px solid #3f3f46;
                 color: #a1a1aa; cursor: pointer; font-family: inherit; font-size: 0.78rem; }
  .lang button.active { background: #27272a; color: #e4e4e7; border-color: #52525b; }
  .lang button:hover { color: #e4e4e7; }

  .content { padding: 1.5rem 2rem; max-width: 1820px; margin: 0 auto; width: 100%;
             box-sizing: border-box; overflow: auto; }

  footer { display: flex; gap: 1.5rem; padding: 0.5rem 1.5rem;
           border-top: 1px solid #27272a; background: #16161a;
           color: #71717a; font-size: 0.72rem; }
  footer code { color: #93c5fd; background: #0f0f12; padding: 0.05rem 0.3rem;
                border-radius: 3px; font-family: ui-monospace, monospace; }
</style>
