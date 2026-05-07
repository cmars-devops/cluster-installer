package seed

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/diskfs/go-diskfs"
	"github.com/diskfs/go-diskfs/disk"
	"github.com/diskfs/go-diskfs/filesystem"
	"github.com/diskfs/go-diskfs/filesystem/iso9660"
)

// SeedFormat is the OS-specific autoinst flavor.
type SeedFormat string

const (
	SeedAutoYaST  SeedFormat = "autoyast"   // openSUSE Leap / Tumbleweed
	SeedIgnition  SeedFormat = "ignition"   // openSUSE MicroOS / SLE Micro
)

// File is one entry to write into the seed ISO.
type File struct {
	Path     string // path inside the ISO, e.g. "ignition/config.ign"
	Contents []byte
}

// Build packs the given files into a small ISO9660 image at outPath. The
// volume label is chosen per-format so the OS installer auto-discovers it
// (OEMDRV for AutoYaST, ignition for Combustion+Ignition).
func Build(outPath string, format SeedFormat, files []File) error {
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return err
	}
	var label string
	switch format {
	case SeedAutoYaST:
		label = "OEMDRV"
	case SeedIgnition:
		label = "ignition"
	default:
		return fmt.Errorf("unknown seed format %q", format)
	}

	d, err := diskfs.Create(outPath, 16*1024*1024, diskfs.SectorSizeDefault) //nolint:staticcheck
	if err != nil {
		return err
	}
	d.LogicalBlocksize = 2048
	fs, err := d.CreateFilesystem(disk.FilesystemSpec{
		Partition:   0,
		FSType:      filesystem.TypeISO9660,
		VolumeLabel: label,
	})
	if err != nil {
		return err
	}
	for _, f := range files {
		if err := writeFile(fs, f); err != nil {
			return err
		}
	}
	if iso, ok := fs.(*iso9660.FileSystem); ok {
		return iso.Finalize(iso9660.FinalizeOptions{
			RockRidge:        true,
			VolumeIdentifier: label,
		})
	}
	return nil
}

func writeFile(fs filesystem.FileSystem, f File) error {
	if dir := filepath.Dir(f.Path); dir != "." && dir != "/" {
		if err := fs.Mkdir(dir); err != nil {
			return err
		}
	}
	dst, err := fs.OpenFile(f.Path, os.O_CREATE|os.O_RDWR)
	if err != nil {
		return err
	}
	if _, err := dst.Write(f.Contents); err != nil {
		return err
	}
	return nil
}
