<script lang="ts">
  import { _ } from 'svelte-i18n';
  import { wizardStore, gotoStep } from '../stores/wizard';
</script>

<h2>{$_('step.2.title')}</h2>

<fieldset>
  <legend>Target type</legend>
  <label><input type="radio" bind:group={$wizardStore.inventory.target!.type} value="libvirt" /> libvirt / KVM (SSH)</label>
  <label><input type="radio" bind:group={$wizardStore.inventory.target!.type} value="proxmox" /> Proxmox VE (REST)</label>
</fieldset>

<label>
  Endpoint
  <input bind:value={$wizardStore.inventory.target!.endpoint}
         placeholder={$wizardStore.inventory.target?.type === 'libvirt'
                       ? 'qemu+ssh://root@kvm1/system'
                       : 'https://pve1.example.com:8006/'} />
</label>

<div class="row">
  <button onclick={() => gotoStep(0)}>{$_('common.back')}</button>
  <button onclick={() => gotoStep(2)}>{$_('common.next')}</button>
</div>

<style>
  fieldset { margin: 1rem 0; padding: 1rem; border: 1px solid #3f3f46; border-radius: 6px; }
  label { display: block; margin: 0.5rem 0; }
  input { padding: 0.4rem; background: #1f1f23; color: inherit; border: 1px solid #3f3f46; border-radius: 4px; }
  .row { display: flex; gap: 0.5rem; margin-top: 1rem; }
</style>
