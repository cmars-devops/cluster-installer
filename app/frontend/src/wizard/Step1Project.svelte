<script lang="ts">
  import { _ } from 'svelte-i18n';
  import Section from '../lib/ui/Section.svelte';
  import Field from '../lib/ui/Field.svelte';
  import Button from '../lib/ui/Button.svelte';
  import StepNav from '../lib/ui/StepNav.svelte';
  import Badge from '../lib/ui/Badge.svelte';
  import { wizardStore, type Topology } from '../stores/wizard';
  import { api, type SavedCredential } from '../lib/api';
  import logoSvg from '../assets/triangles-logo.svg?raw';
  import { onMount } from 'svelte';

  // 'new' is the legacy alias for 'new-cluster' kept for back-compat with
  // saved sessions. The Step 1 mode picker normalises everything into the
  // three-card model: new-cluster | new-vm | resume.
  type Mode = 'new-cluster' | 'new-vm' | 'resume';
  let mode = $state<Mode>(
    $wizardStore.mode === 'new' ? 'new-cluster' : ($wizardStore.mode as Mode)
  );
  let busy = $state(false);
  let fetchedDir = $state<string | null>($wizardStore.contentDir);
  let fetchErr = $state('');
  let runs = $state<any[]>([]);

  const topology = $derived($wizardStore.inventory.cluster.topology);

  async function loadRuns() {
    try { runs = (await api.listRuns()) as any[]; }
    catch { runs = []; }
  }
  loadRuns();

  // ── Saved cluster_auth registry ─────────────────────────────────
  // Mirrors the saved-targets pattern from Step 2. List loaded once on
  // mount; refreshed after every Save/Delete. selectedSavedCredID
  // drives the picker — '' = "use the form fields below".
  let savedCreds = $state<SavedCredential[]>([]);
  let selectedSavedCredID = $state<string>('');
  let savingCred = $state(false);
  let credError = $state<string | null>(null);

  onMount(async () => {
    try { savedCreds = await api.listSavedCredentials(); }
    catch (e) { console.warn('[savedCreds] list failed:', e); }
  });

  async function refreshSavedCreds() {
    try { savedCreds = await api.listSavedCredentials(); }
    catch (e) { console.warn('[savedCreds] refresh failed:', e); }
  }

  function applySavedCredential(id: string) {
    const c = savedCreds.find((x) => x.id === id);
    if (!c) { selectedSavedCredID = ''; return; }
    selectedSavedCredID = id;
    wizardStore.update((s) => ({
      ...s,
      inventory: {
        ...s.inventory,
        cluster_auth: {
          username: c.username ?? '',
          ssh_import_github: c.ssh_import_github ?? [],
          ssh_authorized_keys: c.ssh_authorized_keys ?? [],
          node_password: c.node_password ?? ''
        }
      }
    }));
    api.touchSavedCredential(id).then(refreshSavedCreds).catch(() => {});
  }

  async function saveCurrentCredential() {
    credError = null;
    const auth = $wizardStore.inventory.cluster_auth;
    const hasContent = !!auth.username
      || (auth.ssh_import_github && auth.ssh_import_github.length > 0)
      || (auth.ssh_authorized_keys && auth.ssh_authorized_keys.length > 0)
      || !!auth.node_password;
    if (!hasContent) {
      credError = '저장할 자격증명이 없습니다 — 사용자명/키/패스워드 중 하나는 채워주세요.';
      return;
    }
    savingCred = true;
    try {
      const existing = savedCreds.find((x) => x.id === selectedSavedCredID);
      const ghPart = (auth.ssh_import_github && auth.ssh_import_github.length > 0)
        ? `GitHub: ${auth.ssh_import_github[0]}` + (auth.ssh_import_github.length > 1 ? ` (+${auth.ssh_import_github.length - 1})` : '')
        : '';
      const labelDefault = existing?.label || ghPart || (auth.username ? `sudo: ${auth.username}` : 'credentials');
      const label = window.prompt('저장할 이름 (예: "lab keys (cmars)")', labelDefault) ?? labelDefault;
      const payload: SavedCredential = {
        id: selectedSavedCredID || '',
        label: label.trim() || labelDefault,
        username: auth.username,
        ssh_import_github: auth.ssh_import_github,
        ssh_authorized_keys: auth.ssh_authorized_keys,
        node_password: auth.node_password
      };
      const saved = await api.saveCredential(payload);
      selectedSavedCredID = saved.id;
      await refreshSavedCreds();
    } catch (e) {
      credError = String(e);
    } finally {
      savingCred = false;
    }
  }

  async function deleteSelectedCredential() {
    if (!selectedSavedCredID) return;
    const c = savedCreds.find((x) => x.id === selectedSavedCredID);
    if (!c) return;
    if (!window.confirm(`'${c.label}' 자격증명을 삭제할까요?`)) return;
    try {
      await api.deleteSavedCredential(selectedSavedCredID);
      selectedSavedCredID = '';
      await refreshSavedCreds();
    } catch (e) {
      credError = String(e);
    }
  }

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

  function setMode(m: Mode) {
    mode = m;
    wizardStore.update((s) => {
      const next = { ...s, mode: m };
      // new-vm: pin topology to dev-vm and seed safe defaults so Step
      // 2/3/4 render the single-VM flow immediately. Idempotent.
      if (m === 'new-vm') {
        next.inventory = {
          ...next.inventory,
          cluster: { ...next.inventory.cluster, topology: 'dev-vm' as Topology },
          target: {
            ...next.inventory.target,
            type: 'esxi',
            username: next.inventory.target.username || 'root',
            tls_insecure: next.inventory.target.tls_insecure || true,
            network: next.inventory.target.network || 'VM Network',
          },
          nodes: next.inventory.nodes.length === 0 ? [{
            hostname: 'devvm-01',
            ip: '',
            roles: [],
            os: 'ubuntu' as const,
            os_version: '26.04',
            cpu: 2,
            memory_gb: 4,
            disk_gb: 40,
            ssh_authorized_keys: [],
          }] : next.inventory.nodes,
        };
      }
      // new-cluster from dev-vm: reset topology to a safe default so the
      // user sees the cluster topology selector instead of a stale value.
      if (m === 'new-cluster' && next.inventory.cluster.topology === 'dev-vm') {
        next.inventory = {
          ...next.inventory,
          cluster: { ...next.inventory.cluster, topology: 'k8s-only' as Topology },
        };
      }
      return next;
    });
  }

  function setTopology(t: Topology) {
    wizardStore.update((s) => ({
      ...s,
      inventory: {
        ...s.inventory,
        cluster: { ...s.inventory.cluster, topology: t }
      }
    }));
  }

  // Hint for content fetch (optional in dev mode).
  const canAdvance = true;
</script>

<header class="step-header welcome">
  <div class="welcome-logo" aria-label="Triangles">{@html logoSvg}</div>
  <div>
    <h2>{$_('step.1.title')}</h2>
    <p>{$_('step.1.subtitle')}</p>
  </div>
</header>

<div class="grid">
  <button class="mode-card" class:active={mode === 'new-cluster'} onclick={() => setMode('new-cluster')}>
    <strong>{$_('step1.modeNew')}</strong>
    <span>{$_('step1.modeNewDesc')}</span>
  </button>
  <button class="mode-card devvm" class:active={mode === 'new-vm'} onclick={() => setMode('new-vm')}>
    <strong>{$_('step1.modeNewVM')}</strong>
    <span>{$_('step1.modeNewVMDesc')}</span>
  </button>
  <button class="mode-card" class:active={mode === 'resume'} onclick={() => setMode('resume')}>
    <strong>{$_('step1.modeResume')}</strong>
    <span>{$_('step1.modeResumeDesc')}</span>
  </button>
</div>

{#if mode === 'new-cluster'}
  <Section title={$_('step1.topology')} subtitle={$_('step1.topologyHint')}>
    <div class="topo-grid">
      <button class="topo-card" class:active={topology === 'ceph-only'} onclick={() => setTopology('ceph-only')}>
        <div class="topo-head">
          <span class="topo-icon ceph">●</span>
          <strong>{$_('step1.topologyCeph')}</strong>
        </div>
        <span class="topo-desc">{$_('step1.topologyCephDesc')}</span>
        <div class="topo-roles">
          <span class="role-pill">ceph-mon</span>
          <span class="role-pill">ceph-mgr</span>
          <span class="role-pill">ceph-osd</span>
          <span class="role-pill">ceph-rgw</span>
        </div>
      </button>

      <button class="topo-card" class:active={topology === 'k8s-only'} onclick={() => setTopology('k8s-only')}>
        <div class="topo-head">
          <span class="topo-icon k8s">●</span>
          <strong>{$_('step1.topologyK8s')}</strong>
        </div>
        <span class="topo-desc">{$_('step1.topologyK8sDesc')}</span>
        <div class="topo-roles">
          <span class="role-pill">control-plane</span>
          <span class="role-pill">etcd</span>
          <span class="role-pill">worker</span>
        </div>
      </button>

      <button class="topo-card" class:active={topology === 'combined'} onclick={() => setTopology('combined')}>
        <div class="topo-head">
          <span class="topo-icon combined">●</span>
          <strong>{$_('step1.topologyCombined')}</strong>
          <Badge tone="warn">{$_('step1.topologyAdvanced')}</Badge>
        </div>
        <span class="topo-desc">{$_('step1.topologyCombinedDesc')}</span>
        <div class="topo-roles">
          <span class="role-pill">control-plane</span>
          <span class="role-pill">worker</span>
          <span class="role-pill">ceph-mon</span>
          <span class="role-pill">ceph-osd</span>
          <span class="role-pill">…</span>
        </div>
      </button>
    </div>
  </Section>

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
      {#if fetchedDir}<Badge tone="success">{$_('step1.fetched')} {fetchedDir}</Badge>{/if}
      {#if fetchErr}<Badge tone="danger">{fetchErr}</Badge>{/if}
    </div>
  </Section>
{:else if mode === 'new-vm'}
  <Section title={$_('step1.devVMTitle')} subtitle={$_('step1.devVMSubtitle')}>
    <div class="devvm-info">
      <div class="devvm-row"><span class="devvm-label">{$_('step1.devVMHypervisor')}</span><code>VMware ESXi</code></div>
      <div class="devvm-row"><span class="devvm-label">{$_('step1.devVMOS')}</span><code>Ubuntu 26.04 LTS</code> (Step 3에서 24.04로 변경 가능)</div>
      <div class="devvm-row"><span class="devvm-label">{$_('step1.devVMSeed')}</span><code>cloud-init NoCloud + subiquity autoinstall</code></div>
      <div class="devvm-row"><span class="devvm-label">{$_('step1.devVMVerify')}</span>SSH·os-release / hostname·IP·MAC / 네트워크·DNS / apt update</div>
    </div>
    <p class="muted">{$_('step1.devVMNote')}</p>
  </Section>

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
      {#if fetchedDir}<Badge tone="success">{$_('step1.fetched')} {fetchedDir}</Badge>{/if}
      {#if fetchErr}<Badge tone="danger">{fetchErr}</Badge>{/if}
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

{#if mode === 'new-cluster' || mode === 'new-vm'}
  <Section title="노드 인증 자격 증명"
           subtitle="아래 정보는 새로 설치되는 노드의 sudo 계정 한 개에 모두 적용됩니다 — SSH 키, 콘솔 패스워드, sudoers NOPASSWD 항목이 같은 계정에 들어갑니다.">
    <!-- 저장된 자격증명 picker — Step 2의 saved-server 패턴과 동일.
         이전에 저장한 sudo 사용자명 + GitHub key + raw 키 + 콘솔 PW
         묶음을 한 번에 불러올 수 있다. -->
    {#if savedCreds.length > 0}
      <Field label="저장된 자격증명에서 선택">
        <div class="saved-row">
          <select value={selectedSavedCredID}
                  onchange={(e) => applySavedCredential((e.target as HTMLSelectElement).value)}>
            <option value="">— 새로 입력 —</option>
            {#each savedCreds as c}
              <option value={c.id}>{c.label}</option>
            {/each}
          </select>
          {#if selectedSavedCredID}
            <Button variant="danger" onclick={deleteSelectedCredential}>삭제</Button>
          {/if}
        </div>
      </Field>
    {/if}
    <div class="saved-actions">
      <Button variant="secondary" disabled={savingCred} onclick={saveCurrentCredential}>
        {savingCred ? '저장 중…' : (selectedSavedCredID ? '선택 항목 업데이트' : '현재 입력 저장')}
      </Button>
      {#if credError}<Badge tone="danger">{credError}</Badge>{/if}
      {#if !selectedSavedCredID && savedCreds.length === 0}
        <span class="muted">사용자명·SSH 키·패스워드를 입력한 뒤 '현재 입력 저장'을 누르면 다음 실행에서 한 번에 불러옵니다.</span>
      {/if}
    </div>

    <Field label="사용자명 (sudo 계정)"
           hint={'기본 \'triangles\'. autoinstall이 이 이름으로 사용자를 만들고, 아래 SSH 키와 콘솔 패스워드를 이 계정에 적용합니다. SSH 접속도 ssh ' + ($wizardStore.inventory.cluster_auth.username || 'triangles') + '@<ip> 형태가 됩니다.'}
           required>
      <input value={$wizardStore.inventory.cluster_auth.username}
             oninput={(e) => {
               const v = (e.target as HTMLInputElement).value.trim();
               wizardStore.update((s) => ({
                 ...s,
                 inventory: { ...s.inventory, cluster_auth: { ...s.inventory.cluster_auth, username: v } }
               }));
             }}
             placeholder="triangles" />
    </Field>
    <Field label="GitHub 사용자명 (쉼표 구분)"
           hint="github.com/<username>.keys 에서 SSH 키를 자동 가져옵니다 (ssh-import-id-gh). 여러 명 가능. 키 회전도 GitHub에서 하면 다음 설치부터 자동 반영.">
      <input value={$wizardStore.inventory.cluster_auth.ssh_import_github.join(', ')}
             oninput={(e) => {
               const list = (e.target as HTMLInputElement).value.split(',').map((s) => s.trim()).filter(Boolean);
               wizardStore.update((s) => ({
                 ...s,
                 inventory: { ...s.inventory, cluster_auth: { ...s.inventory.cluster_auth, ssh_import_github: list } }
               }));
             }}
             placeholder="예: octocat, choimars" />
    </Field>
    <details class="auth-advanced">
      <summary>또는 SSH 공개키 직접 붙여넣기 (오프라인 환경)</summary>
      <Field label="SSH 공개키 (한 줄에 하나씩)"
             hint="ssh-ed25519 / ssh-rsa 등. GitHub에 올리지 않은 키만 여기 붙여넣기. 보통은 위의 GitHub 방식이 더 편합니다.">
        <textarea
          rows="3"
          value={$wizardStore.inventory.cluster_auth.ssh_authorized_keys.join('\n')}
          oninput={(e) => {
            const lines = (e.target as HTMLTextAreaElement).value.split('\n').map((l) => l.trim()).filter(Boolean);
            wizardStore.update((s) => ({
              ...s,
              inventory: { ...s.inventory, cluster_auth: { ...s.inventory.cluster_auth, ssh_authorized_keys: lines } }
            }));
          }}
          placeholder={"ssh-ed25519 AAAAC3Nza... user@host"}
          style="width: 100%; font-family: ui-monospace, monospace; font-size: 0.78rem; resize: vertical; box-sizing: border-box;"></textarea>
      </Field>
    </details>
    <Field label="콘솔 패스워드 (선택)"
           hint={'위 \'' + ($wizardStore.inventory.cluster_auth.username || 'triangles') + '\' 사용자와 root의 콘솔/sudo 패스워드. 비워두면 SSH 키만 사용 (권장). run.json에 평문 보관되니 외부 공유 금지.'}>
      <input type="password"
             value={$wizardStore.inventory.cluster_auth.node_password}
             oninput={(e) => {
               const v = (e.target as HTMLInputElement).value;
               wizardStore.update((s) => ({
                 ...s,
                 inventory: { ...s.inventory, cluster_auth: { ...s.inventory.cluster_auth, node_password: v } }
               }));
             }}
             placeholder="비워두면 SSH 키만 사용" />
    </Field>
  </Section>
{/if}

<StepNav canAdvance={canAdvance} />

<style>
  details.auth-advanced { margin: 0.4rem 0; padding: 0; }
  details.auth-advanced > summary {
    cursor: pointer; font-size: 0.78rem; color: #71717a;
    padding: 0.3rem 0; user-select: none;
  }
  details.auth-advanced > summary:hover { color: #d4d4d8; }
  details.auth-advanced[open] > summary { color: #d4d4d8; margin-bottom: 0.3rem; }

  .step-header { margin-bottom: 1.25rem; }
  .step-header h2 { margin: 0; font-size: 1.3rem; }
  .step-header p { margin: 0.25rem 0 0; color: #a1a1aa; font-size: 0.9rem; }
  .step-header.welcome { display: flex; align-items: center; gap: 1rem;
                         padding: 0.5rem 0 1rem; }
  .welcome-logo { display: block; line-height: 0;
                  filter: drop-shadow(0 0 16px rgba(0, 117, 194, 0.45)); }
  .welcome-logo :global(svg) { width: 72px; height: 72px; display: block; }
  .grid { display: grid; grid-template-columns: repeat(3, 1fr); gap: 1rem; margin-bottom: 1.25rem; }
  @media (max-width: 1100px) { .grid { grid-template-columns: 1fr; } }
  .mode-card.devvm.active { border-color: #10b981; background: #052e29; }
  .devvm-info { display: flex; flex-direction: column; gap: 0.4rem; margin-bottom: 0.5rem; }
  .devvm-row { display: grid; grid-template-columns: 10rem 1fr; gap: 0.5rem;
               align-items: center; font-size: 0.85rem; color: #d4d4d8; }
  .devvm-label { color: #71717a; font-size: 0.78rem; text-transform: uppercase; letter-spacing: 0.05em; }
  .devvm-info code { background: #0f0f12; padding: 0.2rem 0.5rem; border-radius: 3px;
                     font-family: ui-monospace, monospace; font-size: 0.82rem; color: #93c5fd; }
  .mode-card { display: flex; flex-direction: column; gap: 0.4rem; align-items: flex-start;
               padding: 1rem 1.25rem; border-radius: 8px; cursor: pointer;
               background: #1b1b1f; border: 1px solid #2a2a30; color: inherit;
               text-align: left; font-family: inherit; transition: border-color 0.1s; }
  .mode-card:hover { border-color: #52525b; }
  .mode-card.active { border-color: #3b82f6; background: #1e293b; }
  .mode-card strong { font-size: 0.95rem; }
  .mode-card span { font-size: 0.8rem; color: #a1a1aa; }

  .topo-grid { display: grid; grid-template-columns: repeat(3, 1fr); gap: 0.75rem; }
  @media (max-width: 1100px) { .topo-grid { grid-template-columns: 1fr; } }
  .topo-card { display: flex; flex-direction: column; gap: 0.6rem;
               padding: 1rem; border-radius: 6px; cursor: pointer;
               background: #0f0f12; border: 1px solid #2a2a30;
               color: inherit; text-align: left; font-family: inherit;
               transition: border-color 0.1s; }
  .topo-card:hover { border-color: #52525b; }
  .topo-card.active { border-color: #3b82f6; background: #1e293b; }
  .topo-head { display: flex; align-items: center; gap: 0.5rem; }
  .topo-icon { font-size: 1.1rem; line-height: 1; }
  .topo-icon.ceph     { color: #f59e0b; }
  .topo-icon.k8s      { color: #3b82f6; }
  .topo-icon.combined { color: #a855f7; }
  .topo-card strong { font-size: 0.92rem; flex: 1; }
  .topo-desc { font-size: 0.78rem; color: #a1a1aa; line-height: 1.5; }
  .topo-roles { display: flex; flex-wrap: wrap; gap: 0.25rem; margin-top: 0.25rem; }
  .role-pill { display: inline-block; padding: 0.1rem 0.45rem; border-radius: 999px;
               background: #27272a; border: 1px solid #3f3f46;
               color: #a1a1aa; font-size: 0.7rem; font-family: ui-monospace, monospace; }
  .topo-card.active .role-pill { border-color: #3b82f6; color: #93c5fd; }

  .row { display: flex; gap: 0.75rem; align-items: center; }
  .muted { color: #71717a; font-size: 0.85rem; }
  table { width: 100%; border-collapse: collapse; font-size: 0.85rem; }
  th, td { padding: 0.5rem; text-align: left; border-bottom: 1px solid #2a2a30; }
  th { color: #a1a1aa; font-weight: 500; }
  code { background: #27272a; padding: 0.1rem 0.4rem; border-radius: 3px; }

  /* Saved-credential picker — same look-and-feel as Step 2's
     saved-target picker so the two registries feel like one feature. */
  .saved-row { display: flex; gap: 0.5rem; align-items: stretch; }
  .saved-row select { flex: 1; }
  .saved-actions { display: flex; gap: 0.6rem; align-items: center;
                   margin: 0.4rem 0 1rem; flex-wrap: wrap; }
  .saved-actions .muted { font-size: 0.78rem; color: #71717a; }
</style>
