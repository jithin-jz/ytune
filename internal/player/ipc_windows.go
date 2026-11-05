//go:build windows

package player

import (
	"context"
	"net"

	"github.com/Microsoft/go-winio"
)

func ipcAddress(name string) string {
	return `\\.\pipe\` + name
}

func dial(ctx context.Context, address string) (net.Conn, error) {
	return winio.DialPipeContext(ctx, address)
}
