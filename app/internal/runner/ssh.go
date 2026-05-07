package runner

import (
	"context"
	"fmt"
	"net"
	"os"
	"time"

	"golang.org/x/crypto/ssh"
)

// WaitForSSH blocks until every host accepts a TCP connection on port 22 and
// returns a working SSH session, or until ctx is cancelled.
func WaitForSSH(ctx context.Context, hosts []string, user, keyPath string, perHostTimeout time.Duration) error {
	cfg, err := authConfig(user, keyPath)
	if err != nil {
		return err
	}
	for _, h := range hosts {
		if err := waitOne(ctx, h, cfg, perHostTimeout); err != nil {
			return fmt.Errorf("ssh wait %s: %w", h, err)
		}
	}
	return nil
}

func waitOne(ctx context.Context, host string, cfg *ssh.ClientConfig, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, "22"), 5*time.Second)
		if err == nil {
			c, chans, reqs, err := ssh.NewClientConn(conn, host, cfg)
			if err == nil {
				cl := ssh.NewClient(c, chans, reqs)
				_ = cl.Close()
				return nil
			}
			_ = conn.Close()
		}
		time.Sleep(5 * time.Second)
	}
	return fmt.Errorf("timed out after %s", timeout)
}

func authConfig(user, keyPath string) (*ssh.ClientConfig, error) {
	raw, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("read key: %w", err)
	}
	signer, err := ssh.ParsePrivateKey(raw)
	if err != nil {
		return nil, fmt.Errorf("parse key: %w", err)
	}
	return &ssh.ClientConfig{
		User:            user,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), //nolint:gosec — fresh-install hosts have no known key
		Timeout:         10 * time.Second,
	}, nil
}
