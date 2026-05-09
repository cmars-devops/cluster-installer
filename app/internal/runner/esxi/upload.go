package esxi

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"

	"github.com/vmware/govmomi"
	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/vim25/progress"
	"github.com/vmware/govmomi/vim25/soap"

	"github.com/cmars-devops/cluster-installer/internal/inventory"
)

// Client is a thin wrapper around govmomi that the orchestrator uses for
// the ESXi side of an Apply: ISO uploads, presence checks, and (later)
// VM lifecycle. It owns one logged-in session — callers must defer Close.
type Client struct {
	gc       *govmomi.Client
	finder   *find.Finder
	dc       *object.Datacenter
	insecure bool
}

// NewClient logs in once. The returned client is bound to the default
// datacenter (standalone ESXi: "ha-datacenter"; vCenter: the first DC
// returned). For multi-DC vCenter installs the wizard would need to grow
// a datacenter picker — out of scope for v1.x.
func NewClient(ctx context.Context, t inventory.TargetSpec) (*Client, error) {
	if t.Type != "esxi" {
		return nil, fmt.Errorf("target.type=%q is not esxi", t.Type)
	}
	u, err := normaliseSDKURL(t.Endpoint, t.Username, t.Password)
	if err != nil {
		return nil, err
	}
	gc, err := govmomi.NewClient(ctx, u, t.TLSInsecure)
	if err != nil {
		return nil, fmt.Errorf("connect %s: %w", t.Endpoint, err)
	}
	finder := find.NewFinder(gc.Client, true)
	dc, err := finder.DefaultDatacenter(ctx)
	if err != nil {
		_ = gc.Logout(ctx)
		return nil, fmt.Errorf("default datacenter: %w", err)
	}
	finder.SetDatacenter(dc)
	return &Client{gc: gc, finder: finder, dc: dc, insecure: t.TLSInsecure}, nil
}

// Close logs the session out. Safe to call on a nil receiver.
func (c *Client) Close(ctx context.Context) {
	if c == nil || c.gc == nil {
		return
	}
	_ = c.gc.Logout(ctx)
}

// Datacenter returns the datacenter name the client is bound to.
// Standalone ESXi: "ha-datacenter". vCenter: the first DC the finder
// returned (today, until the wizard grows a DC picker).
func (c *Client) Datacenter() string {
	if c == nil || c.dc == nil {
		return ""
	}
	return c.dc.Name()
}

// UploadFile pushes a local file to a vSphere datastore at the given
// relative path. Uses the SOAP-attached file-manager URL — that's the
// same HTTP endpoint vSphere's web UI uses for the upload button.
//
// dsRel is the path *inside* the datastore, e.g.
// "cluster-installer/<run-id>/seed-<host>.iso". Intermediate directories
// are created on demand.
func (c *Client) UploadFile(ctx context.Context, datastore, dsRel, localPath string, lineEmit func(string)) error {
	ds, err := c.finder.Datastore(ctx, datastore)
	if err != nil {
		return fmt.Errorf("find datastore %q: %w", datastore, err)
	}

	// Ensure parent dirs exist on the datastore. file-manager mkdir is
	// idempotent — we swallow "already exists".
	if dir := path.Dir(dsRel); dir != "." && dir != "/" {
		if err := c.ensureDatastoreDir(ctx, ds, dir); err != nil {
			return fmt.Errorf("mkdir %s: %w", dir, err)
		}
	}

	f, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		return err
	}

	emit(lineEmit, "uploading %s → [%s] %s (%s)", path.Base(localPath), datastore, dsRel, mibSize(stat.Size()))

	pp := soap.DefaultUpload
	pp.ContentLength = stat.Size()
	pp.Method = "PUT"
	if lineEmit != nil {
		pp.Progress = newProgressSinker(stat.Size(), path.Base(localPath), lineEmit)
	}

	if err := ds.Upload(ctx, f, dsRel, &pp); err != nil {
		return fmt.Errorf("upload %s: %w", localPath, err)
	}
	emit(lineEmit, "%s  upload done", path.Base(localPath))
	return nil
}

// UploadStream uploads from an in-memory reader. Currently unused by the
// orchestrator (which always materialises ISOs to disk first), but
// exposed for the future ISO-remaster path that would stream a
// transformed ISO directly to the datastore.
func (c *Client) UploadStream(ctx context.Context, datastore, dsRel string, body io.Reader, size int64) error {
	ds, err := c.finder.Datastore(ctx, datastore)
	if err != nil {
		return err
	}
	if dir := path.Dir(dsRel); dir != "." && dir != "/" {
		if err := c.ensureDatastoreDir(ctx, ds, dir); err != nil {
			return err
		}
	}
	pp := soap.DefaultUpload
	pp.ContentLength = size
	pp.Method = "PUT"
	return ds.Upload(ctx, body, dsRel, &pp)
}

// DSPath formats a datastore-relative path the way vSphere expects it
// in API calls and CD-ROM backings: "[datastore] subdir/file.iso".
func DSPath(datastore, dsRel string) string {
	return fmt.Sprintf("[%s] %s", datastore, strings.TrimLeft(dsRel, "/"))
}

func (c *Client) ensureDatastoreDir(ctx context.Context, ds *object.Datastore, dsRel string) error {
	fm := object.NewFileManager(c.gc.Client)
	dsPath := ds.Path(dsRel)
	// MakeDirectory(..., createParents=true) walks the chain for us.
	if err := fm.MakeDirectory(ctx, dsPath, c.dc, true); err != nil {
		// vSphere reports an existing directory in two distinct forms
		// depending on which API path raised it:
		//   - SOAP fault name:  "FileAlreadyExists"
		//   - Human message:    "...already exists"  (e.g. ServerFaultCode
		//                       wrapping it)
		// Both are benign for our mkdir-p semantics, so accept either.
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "alreadyexists") ||
			strings.Contains(msg, "already exists") {
			return nil
		}
		return err
	}
	return nil
}

// progressSinker adapts govmomi's progress.Sinker into 2-second log
// lines. The cadence matches imagecache's download progress so the run
// log reads consistently regardless of whether bytes are flowing in
// (mirror download) or out (datastore upload).
type progressSinker struct {
	total    int64
	basename string
	lineEmit func(string)
}

func newProgressSinker(total int64, basename string, lineEmit func(string)) progress.Sinker {
	return &progressSinker{total: total, basename: basename, lineEmit: lineEmit}
}

func (p *progressSinker) Sink() chan<- progress.Report {
	ch := make(chan progress.Report)
	go func() {
		var lastEmit time.Time
		for r := range ch {
			if r.Error() != nil {
				emit(p.lineEmit, "%s  ERROR: %v", p.basename, r.Error())
				continue
			}
			pct := r.Percentage()
			if pct < 100 && time.Since(lastEmit) < 2*time.Second {
				continue
			}
			lastEmit = time.Now()
			done := int64(float32(p.total) * pct / 100)
			emit(p.lineEmit, "%s  %.0f%% (%s/%s)",
				p.basename, pct, mibSize(done), mibSize(p.total))
		}
	}()
	return ch
}

func emit(p func(string), format string, args ...interface{}) {
	if p == nil {
		return
	}
	p(fmt.Sprintf(format, args...))
}

func mibSize(n int64) string {
	const m = 1024 * 1024
	if n <= 0 {
		return "0 MiB"
	}
	return fmt.Sprintf("%d MiB", n/m)
}
