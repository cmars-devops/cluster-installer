<script lang="ts">
  interface Props {
    tone?: 'neutral' | 'success' | 'warn' | 'danger' | 'info';
    children?: any;
  }
  let { tone = 'neutral', children }: Props = $props();
</script>

<span class="badge {tone}">
  {@render children?.()}
</span>

<style>
  .badge { display: inline-block; padding: 0.15rem 0.5rem; border-radius: 999px;
           font-size: 0.7rem; font-weight: 500; line-height: 1.2; }
  .neutral { background: #3f3f46; color: #d4d4d8; }
  .success { background: #14532d; color: #4ade80; }
  .warn    { background: #78350f; color: #fbbf24; }
  .danger  { background: #7f1d1d; color: #fca5a5; }
  .info    { background: #1e3a8a; color: #93c5fd; }
</style>
