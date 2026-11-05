//go:build !windows

package player

import (
	"context"
	"net"
	"os"
	"path/filepath"
)

func ipcAddress(name string) string {
	return filepath.Join(os.TempDir(), name+".sock")
}

func dial(ctx context.Context, address string) (net.Conn, error) {
	var dialer net.Dialer
	return dialer.DialContext(ctx, "unix", address)
}
