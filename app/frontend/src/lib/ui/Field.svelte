<script lang="ts">
  interface Props {
    label: string;
    hint?: string;
    error?: string;
    required?: boolean;
    children?: any;
  }
  let { label, hint, error, required = false, children }: Props = $props();
</script>

<label class="field" class:has-error={!!error}>
  <span class="label">
    {label}{#if required}<span class="req">*</span>{/if}
  </span>
  {@render children?.()}
  {#if hint && !error}<span class="hint">{hint}</span>{/if}
  {#if error}<span class="err">{error}</span>{/if}
</label>

<style>
  .field { display: flex; flex-direction: column; gap: 0.3rem; font-size: 0.85rem; }
  .label { color: #d4d4d8; font-weight: 500; }
  .req { color: #f87171; margin-left: 0.15rem; }
  .hint { font-size: 0.75rem; color: #71717a; }
  .err { font-size: 0.75rem; color: #f87171; }
  .field :global(input),
  .field :global(select),
  .field :global(textarea) {
    background: #0f0f12; color: #e4e4e7; border: 1px solid #3f3f46;
    border-radius: 5px; padding: 0.45rem 0.6rem; font-size: 0.85rem;
    font-family: inherit; outline: none; transition: border-color 0.1s;
  }
  .field :global(input:focus),
  .field :global(select:focus),
  .field :global(textarea:focus) { border-color: #60a5fa; }
  .has-error :global(input),
  .has-error :global(select),
  .has-error :global(textarea) { border-color: #f87171; }
</style>
