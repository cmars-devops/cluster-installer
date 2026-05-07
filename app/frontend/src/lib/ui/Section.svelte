<script lang="ts">
  interface Props {
    title: string;
    subtitle?: string;
    children?: any;
  }
  let { title, subtitle, children }: Props = $props();
</script>

<section class="card">
  <header>
    <h3>{title}</h3>
    {#if subtitle}<p class="sub">{subtitle}</p>{/if}
  </header>
  <div class="body">
    {@render children?.()}
  </div>
</section>

<style>
  .card { background: #1b1b1f; border: 1px solid #2a2a30; border-radius: 8px;
          padding: 1.25rem 1.5rem; margin-bottom: 1.25rem; }
  header { margin-bottom: 1rem; }
  h3 { margin: 0; font-size: 0.95rem; font-weight: 600; color: #e4e4e7; }
  .sub { margin: 0.25rem 0 0; font-size: 0.8rem; color: #a1a1aa; }
  .body { display: flex; flex-direction: column; gap: 0.75rem; }
</style>
