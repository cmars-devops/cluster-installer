// Typed wrappers around the Wails-generated bindings under
// frontend/wailsjs/go/main/App.* — those files are produced by `wails dev|build`
// and intentionally not committed.
//
// Runtime contract with the Go backend (see app/app.go):
//
//   CheckRuntime() => Status
//   FetchContent(repo: string, ref: string) => contentDir: string
//   ValidateInventory(yaml: string, contentDir: string) => { valid, errors[] }
//   CreateRun(inv: Inventory) => runId: string
//   PlanRun(runId: string) => preview: string
//   ApplyRun(runId: string) => void   // streams via Wails events 'run:line' / 'run:stage'
//   ResumeRun(runId: string) => Run
//   ListRuns() => RunSummary[]

export type Status = {
  uv_installed: boolean;
  uv_version: string;
  ansible_core_installed: boolean;
  ansible_core_version: string;
  terraform_installed: boolean;
  terraform_version: string;
  bootstrap_message: string;
};

export type ValidationResult = { valid: boolean; errors: string[] };

// Lazily import the wailsjs binding so the dev server doesn't break before
// `wails dev` has generated it.
async function bindings() {
  // @ts-expect-error  wailsjs is generated at build time
  return await import('../../wailsjs/go/main/App');
}

export const api = {
  async checkRuntime(): Promise<Status> { return (await bindings()).CheckRuntime(); },
  async fetchContent(repo: string, ref: string): Promise<string> { return (await bindings()).FetchContent(repo, ref); },
  async validateInventory(yaml: string, dir: string): Promise<ValidationResult> { return (await bindings()).ValidateInventory(yaml, dir); },
  async createRun(inv: unknown): Promise<string> { return (await bindings()).CreateRun(inv); },
  async planRun(id: string): Promise<string>     { return (await bindings()).PlanRun(id); },
  async applyRun(id: string): Promise<void>      { return (await bindings()).ApplyRun(id); },
  async resumeRun(id: string): Promise<unknown>  { return (await bindings()).ResumeRun(id); },
  async listRuns(): Promise<unknown[]>           { return (await bindings()).ListRuns(); }
};
