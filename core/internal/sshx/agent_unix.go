//go:build !windows

package sshx

import (
	"context"
	"errors"
	"net"
	"os"

	"golang.org/x/crypto/ssh/agent"
)

func dialAgent(ctx context.Context) (agent.ExtendedAgent, func(), error) {
	sock := os.Getenv("SSH_AUTH_SOCK")
	if sock == "" {
		return nil, nil, errors.New("SSH_AUTH_SOCK is not set")
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", sock)
	if err != nil {
		return nil, nil, err
	}
	return agent.NewClient(conn), func() { conn.Close() }, nil
}
