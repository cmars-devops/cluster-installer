terraform {
  required_version = ">= 1.6"
  required_providers {
    libvirt = {
      source  = "dmacvicar/libvirt"
      version = ">= 0.7.6"
    }
  }
}

variable "name"            { type = string }
variable "memory_mb"       { type = number }
variable "vcpu"            { type = number }
variable "disk_gb"         { type = number }
variable "extra_disks_gb"  { type = list(number); default = [] }
variable "base_volume_id"  { type = string }
variable "seed_iso_path"   { type = string; description = "Path on the libvirt host to the per-node seed ISO" }
variable "network_id"      { type = string }
variable "mac"             { type = string; default = null }
variable "pool"            { type = string; default = "default" }

resource "libvirt_volume" "root" {
  name             = "${var.name}-root.qcow2"
  pool             = var.pool
  base_volume_id   = var.base_volume_id
  size             = var.disk_gb * 1024 * 1024 * 1024
}

resource "libvirt_volume" "extra" {
  for_each = { for i, gb in var.extra_disks_gb : tostring(i) => gb }
  name     = "${var.name}-data-${each.key}.qcow2"
  pool     = var.pool
  size     = each.value * 1024 * 1024 * 1024
  format   = "qcow2"
}

resource "libvirt_domain" "vm" {
  name      = var.name
  memory    = var.memory_mb
  vcpu      = var.vcpu
  autostart = true

  cpu { mode = "host-passthrough" }

  network_interface {
    network_id     = var.network_id
    mac            = var.mac
    wait_for_lease = true
  }

  disk { volume_id = libvirt_volume.root.id }

  dynamic "disk" {
    for_each = libvirt_volume.extra
    content { volume_id = disk.value.id }
  }

  # Seed ISO carrying Agama or Ignition+Combustion config drive.
  disk { file = var.seed_iso_path }

  console {
    type        = "pty"
    target_port = "0"
    target_type = "serial"
  }

  graphics {
    type        = "spice"
    listen_type = "address"
  }
}

output "id"          { value = libvirt_domain.vm.id }
output "primary_ip"  { value = libvirt_domain.vm.network_interface[0].addresses[0] }
