<script lang="ts">
  import { _ } from 'svelte-i18n';
  import Button from './Button.svelte';
  import { gotoStep, wizardStore } from '../../stores/wizard';

  interface Props {
    /** When false, the Next button is disabled (typically because validation has not passed). */
    canAdvance?: boolean;
    /** Override label for the forward action (defaults to common.next). */
    nextLabel?: string;
    /** Hook fired before moving forward — return false to block. */
    onNext?: () => boolean | Promise<boolean> | void;
  }
  let { canAdvance = true, nextLabel, onNext }: Props = $props();

  async function next() {
    if (onNext) {
      const ok = await onNext();
      if (ok === false) return;
    }
    gotoStep($wizardStore.step + 1);
  }
</script>

<div class="row">
  <Button variant="ghost" disabled={$wizardStore.step === 0} onclick={() => gotoStep($wizardStore.step - 1)}>
    ← {$_('common.back')}
  </Button>
  <Button variant="primary" disabled={!canAdvance} onclick={next}>
    {nextLabel ?? $_('common.next')} →
  </Button>
</div>

<style>
  .row { display: flex; gap: 0.5rem; justify-content: flex-end; margin-top: 1.25rem; }
</style>
