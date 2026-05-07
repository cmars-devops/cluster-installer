# Seed templates

Per-node first-boot configuration. The Windows installer renders each
template (Go `text/template`) into a per-node ISO labeled `OEMDRV` (AutoYaST
auto-pickup) or `ignition` (Combustion+Ignition pickup), then attaches the ISO
to the VM as a second virtual CD-ROM.

| File | OS | Picked up by |
|------|----|--------------|
| `autoyast/leap.xml.tmpl` | openSUSE Leap | `autoyast=device://sr1/autoinst.xml` kernel arg |
| `autoyast/tumbleweed.xml.tmpl` | openSUSE Tumbleweed | same |
| `ignition/microos-base.ign.tmpl` | openSUSE MicroOS / SLE Micro | Ignition stage in initrd, file at `/ignition/config.ign` |
| `ignition/combustion-script.tmpl` | openSUSE MicroOS / SLE Micro | Combustion stage, file at `/combustion/script` |

## Template variables

The Go renderer feeds the following struct into every template:

```go
type SeedContext struct {
    Cluster ClusterCtx   // .Cluster.Name, .Cluster.Domain
    Network NetworkCtx   // .Network.{Gateway,DNS,PrefixLen,PodCIDR,ServiceCIDR}
    Node    NodeCtx      // .Node.{Hostname,IP,Roles,OS,NetworkInterface,SSHAuthorizedKeys,NeedsCeph}
}
```

`.Node.NeedsCeph` is `true` if the node carries any `ceph-*` role.

## Why `.tmpl`, not `.j2`?

`.j2` (Jinja2) is reserved for files Ansible itself renders. The seed files
are rendered by the Windows installer's Go runtime *before* the OS even
boots, so they use Go template syntax (`{{ .Field }}`).
