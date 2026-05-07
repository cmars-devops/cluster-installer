<script lang="ts">
  import { _ } from 'svelte-i18n';
  import { wizardStore } from './stores/wizard';
  import Step1Project from './wizard/Step1Project.svelte';
  import Step2Target from './wizard/Step2Target.svelte';
  import Step3OS from './wizard/Step3OS.svelte';
  import Step4Inventory from './wizard/Step4Inventory.svelte';
  import Step5Plan from './wizard/Step5Plan.svelte';
  import Step6Run from './wizard/Step6Run.svelte';
  import Step7Result from './wizard/Step7Result.svelte';

  const stepComponents = [Step1Project, Step2Target, Step3OS, Step4Inventory, Step5Plan, Step6Run, Step7Result];
  $: Current = stepComponents[$wizardStore.step] ?? Step1Project;
</script>

<main>
  <header>
    <h1>{$_('app.title')}</h1>
    <ol class="steps">
      {#each Array(7) as _i, idx}
        <li class:active={idx === $wizardStore.step}
            class:done={idx < $wizardStore.step}>
          {$_(`step.${idx + 1}.short`)}
        </li>
      {/each}
    </ol>
  </header>

  <section class="content">
    <svelte:component this={Current} />
  </section>
</main>

<style>
  main { display: grid; grid-template-rows: auto 1fr; height: 100%; }
  header { padding: 1rem 1.5rem; border-bottom: 1px solid #27272a;
           display: flex; align-items: center; justify-content: space-between; }
  h1 { margin: 0; font-size: 1.1rem; font-weight: 600; }
  .steps { list-style: none; display: flex; gap: 0.5rem; padding: 0; margin: 0; }
  .steps li { padding: 0.25rem 0.6rem; border-radius: 999px; background: #27272a; font-size: 0.8rem; }
  .steps li.active { background: #3b82f6; color: white; }
  .steps li.done { background: #16a34a; color: white; }
  .content { padding: 2rem; overflow: auto; }
</style>
