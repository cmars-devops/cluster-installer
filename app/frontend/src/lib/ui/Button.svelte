<script lang="ts">
  interface Props {
    variant?: 'primary' | 'secondary' | 'ghost' | 'danger';
    type?: 'button' | 'submit';
    disabled?: boolean;
    onclick?: (e: MouseEvent) => void;
    children?: any;
  }
  let { variant = 'secondary', type = 'button', disabled = false, onclick, children }: Props = $props();
</script>

<button {type} {disabled} {onclick} class={variant}>
  {@render children?.()}
</button>

<style>
  button {
    padding: 0.5rem 1rem; border-radius: 5px; font-size: 0.85rem;
    font-weight: 500; cursor: pointer; transition: background 0.1s, opacity 0.1s;
    border: 1px solid transparent;
  }
  button:disabled { opacity: 0.5; cursor: not-allowed; }
  button:focus-visible { outline: 2px solid #60a5fa; outline-offset: 1px; }

  .primary    { background: #2563eb; color: white; }
  .primary:hover:not(:disabled)    { background: #1d4ed8; }
  .secondary  { background: #27272a; color: #e4e4e7; border-color: #3f3f46; }
  .secondary:hover:not(:disabled)  { background: #3f3f46; }
  .ghost      { background: transparent; color: #d4d4d8; }
  .ghost:hover:not(:disabled)      { background: #27272a; }
  .danger     { background: #dc2626; color: white; }
  .danger:hover:not(:disabled)     { background: #b91c1c; }
</style>
