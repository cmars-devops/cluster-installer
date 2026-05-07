# Contributing

This project lives in two git repositories:

| Repo | Purpose | Primary | Mirror |
|------|---------|---------|--------|
| **app** (this repo) | Wails Go + Svelte UI that compiles into the single `cluster-installer.exe`. | [GitHub](https://github.com/cmars-devops/cluster-installer) | [Azure DevOps](https://dev.azure.com/triangles-infrastructure/ClusterInstaller-App/_git/ClusterInstaller-App) |
| **content** (submodule at `content/`) | Terraform modules, Ansible playbooks, Agama/Combustion seeds, Helm values, image catalog. Pulled by the .exe at runtime via Step 1 "Fetch content". | [GitHub](https://github.com/cmars-devops/cluster-installer-content) | (none) |

The split exists so the .exe stays small and content can be patched
without rebuilding the binary. The runtime contract: the .exe asks
GitHub for `content/<tag>` at install time, never bundles it.

For development, the content repo is mounted as a git submodule at the
same `content/` path so you can edit both sides in one IDE and keep
working in one directory.

### Mirror to Azure DevOps

The app repo also lives on Azure DevOps (Triangles internal infra).
GitHub is the source of truth; Azure is a passive mirror. After a
fresh clone, set up dual-push so a single `git push origin main`
updates both:

```bash
git remote set-url --add --push origin https://github.com/cmars-devops/cluster-installer.git
git remote set-url --add --push origin https://dev.azure.com/triangles-infrastructure/ClusterInstaller-App/_git/ClusterInstaller-App
```

Verify with `git remote -v` — `origin (push)` should appear twice.
A successful push then prints "Everything up-to-date" (or the new
ref) twice — once per destination.

The content submodule has no Azure mirror; the .gitmodules URL points
at GitHub, so anyone cloning from Azure still pulls the submodule
from GitHub. Acceptable because the content repo is public.

## Cloning

```bash
git clone --recurse-submodules https://github.com/cmars-devops/cluster-installer.git
cd cluster-installer
```

If you cloned without `--recurse-submodules`:

```bash
git submodule update --init
```

One-time global config so `git pull` and branch switches automatically
pull submodule updates:

```bash
git config --global submodule.recurse true
```

## Day-to-day workflow

### Editing only app code

```bash
# in repo root
vim app/internal/run/orchestrator.go
git add app/internal/run/orchestrator.go
git commit -m "..."
git push
```

The submodule pointer is untouched, no special handling needed.

### Editing only content code

```bash
cd content
vim seeds/agama/leap.auto.json.tmpl
git add seeds/agama/leap.auto.json.tmpl
git commit -m "Agama profile: tweak Leap 16 product id"
git push origin main             # push to content repo
cd ..

# Now bump the submodule pointer in the app repo:
git add content
git commit -m "Bump content/ → <new content commit hash>"
git push origin main             # push to app repo
```

### Editing both at once (the common case)

When a change crosses the boundary — e.g. you add a new field in
`content/schema/inventory.schema.json` and the matching Go struct in
`app/internal/inventory/types.go` — do it in two commits, content
first:

```bash
# 1. content side
cd content
vim schema/inventory.schema.json
git add schema/inventory.schema.json
git commit -m "Schema: add cluster.foo"
git push origin main
cd ..

# 2. app side, including the submodule bump in the SAME commit so
#    the app commit cleanly says "this exe goes with that content"
vim app/internal/inventory/types.go
git add app/internal/inventory/types.go content
git commit -m "Inventory: support cluster.foo (bumps content)"
git push origin main
```

This way every app commit explicitly pins the content commit it was
tested against — `git log -p content` on the app repo shows the
content history that mattered for each app change.

## Pulling other people's changes

```bash
git pull
# If you set submodule.recurse=true above, content/ updates automatically.
# Otherwise:
git submodule update
```

If `git status` shows `modified content (new commits)` after a pull,
that means upstream bumped the submodule pointer; run
`git submodule update` to check out the new pinned commit.

## Common pitfalls

- **Forgot to push content first**: if you push the app repo with a
  submodule pointer that doesn't exist on the content remote, anyone
  who pulls will get an error checking out the submodule. Always push
  content before app.
- **Detached HEAD in submodule**: `git submodule update` checks out a
  specific commit (not a branch), so the submodule lands in detached
  HEAD. Before editing, `cd content && git checkout main && git pull`.
- **Accidentally committed app/content path again**: shouldn't be
  possible because `content` is now a gitlink (mode 160000), not a
  tree. If you see `D content/foo.tf` in `git status` you're inside
  the submodule; `cd ..` out.

## Releases

Both repos use semver tags (`v0.1.0`, `v0.1.1`, …). The release flow:

1. Tag the content repo: `cd content && git tag v0.2.0 && git push --tags`
2. Bump the submodule in the app repo to that exact commit
3. Tag the app repo: `git tag v0.2.0 && git push --tags`
4. Build the exe: `cd app && wails build`

The exe ships with a default `content_ref` baked at build time
(currently `v0.1.0`); users can override it in the wizard's Step 1.

## When in doubt

Run `git submodule status` from the repo root — the leading character
tells you the state:

```
 56e9c61 content (v0.1.0-9-g56e9c61)   # clean, pinned
+abc1234 content                          # local commits in submodule, not pushed
-                                          # submodule not initialised
U                                          # merge conflict in submodule
```
