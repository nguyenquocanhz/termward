//go:build windows

package sshx

import (
	"context"
	"os"
	"strings"

	"github.com/Microsoft/go-winio"
	"golang.org/x/crypto/ssh/agent"
)

// The Windows OpenSSH agent listens on a named pipe. Tools such as 1Password
// or gpg4win expose their own pipe through SSH_AUTH_SOCK.
const defaultAgentPipe = `\\.\pipe\openssh-ssh-agent`

func dialAgent(ctx context.Context) (agent.ExtendedAgent, func(), error) {
	pipe := defaultAgentPipe
	if s := os.Getenv("SSH_AUTH_SOCK"); strings.HasPrefix(s, `\\.\pipe\`) {
		pipe = s
	}
	conn, err := winio.DialPipeContext(ctx, pipe)
	if err != nil {
		return nil, nil, err
	}
	return agent.NewClient(conn), func() { conn.Close() }, nil
}
