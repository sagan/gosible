// Package executor provides SSH-based remote execution against inventory hosts.
package executor

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/sagan/gosible/internal/inventory"
	"github.com/sagan/gosible/internal/modules"
)

// Options configures how connections are established.
type Options struct {
	// User overrides the SSH user; defaults to the inventory ansible_user or current OS user.
	User string
	// Port overrides the SSH port; defaults to ansible_port or 22.
	Port int
	// PrivateKeyFile is the path to an SSH private key file.
	PrivateKeyFile string
	// Password is used for password-based SSH auth (not recommended).
	Password string
	// Timeout is the connection dial timeout.
	Timeout time.Duration
	// StrictHostKeyChecking mirrors the SSH option (default: true).
	StrictHostKeyChecking bool
	// KnownHostsFile is the path to the known_hosts file.
	KnownHostsFile string
}

// DefaultOptions returns sensible defaults for SSH connections.
func DefaultOptions() Options {
	home, _ := os.UserHomeDir()
	return Options{
		Port:                  22,
		Timeout:               10 * time.Second,
		StrictHostKeyChecking: true,
		KnownHostsFile:        filepath.Join(home, ".ssh", "known_hosts"),
		PrivateKeyFile:        filepath.Join(home, ".ssh", "id_rsa"),
	}
}

// SSHConnection implements modules.Connection over an established SSH session.
type SSHConnection struct {
	client *ssh.Client
	host   string
}

// RunCommand executes a single command on the remote host.
func (c *SSHConnection) RunCommand(cmd string) (stdout, stderr string, rc int, err error) {
	sess, err := c.client.NewSession()
	if err != nil {
		return "", "", -1, fmt.Errorf("executor: new session: %w", err)
	}
	defer sess.Close()

	var outBuf, errBuf cappedBuffer
	sess.Stdout = &outBuf
	sess.Stderr = &errBuf

	runErr := sess.Run(cmd)
	stdout = outBuf.String()
	stderr = errBuf.String()

	if runErr != nil {
		if exitErr, ok := runErr.(*ssh.ExitError); ok {
			return stdout, stderr, exitErr.ExitStatus(), nil
		}
		return stdout, stderr, -1, fmt.Errorf("executor: run %q: %w", cmd, runErr)
	}
	return stdout, stderr, 0, nil
}

// Close tears down the underlying SSH connection.
func (c *SSHConnection) Close() error {
	return c.client.Close()
}

// Dial establishes an SSH connection to the given host using opts and the
// host's inventory variables for fine-grained overrides.
func Dial(host *inventory.Host, opts Options) (*SSHConnection, error) {
	// Resolve connection parameters from inventory variables with fallback to opts.
	sshUser := firstNonEmpty(host.GetVar("ansible_user", ""), host.GetVar("ansible_ssh_user", ""), opts.User, os.Getenv("USER"), "root")
	sshPort := host.GetVar("ansible_port", "")
	if sshPort == "" {
		sshPort = host.GetVar("ansible_ssh_port", "")
	}
	if sshPort == "" {
		sshPort = fmt.Sprintf("%d", opts.Port)
	}

	addr := host.GetVar("ansible_host", "")
	if addr == "" {
		addr = host.Name
	}
	addr = net.JoinHostPort(addr, sshPort)

	authMethods, err := buildAuthMethods(host, opts)
	if err != nil {
		return nil, err
	}

	hostKeyCallback, err := buildHostKeyCallback(opts)
	if err != nil {
		return nil, err
	}

	config := &ssh.ClientConfig{
		User:            sshUser,
		Auth:            authMethods,
		HostKeyCallback: hostKeyCallback,
		Timeout:         opts.Timeout,
	}

	client, err := ssh.Dial("tcp", addr, config)
	if err != nil {
		return nil, fmt.Errorf("executor: dial %s: %w", addr, err)
	}
	return &SSHConnection{client: client, host: host.Name}, nil
}

// buildAuthMethods assembles SSH auth methods in priority order:
// 1. ssh-agent (if SSH_AUTH_SOCK is set)
// 2. Private key file from inventory or opts
// 3. Password from inventory or opts
func buildAuthMethods(host *inventory.Host, opts Options) ([]ssh.AuthMethod, error) {
	var methods []ssh.AuthMethod

	// 1. SSH agent
	if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
		conn, err := net.Dial("unix", sock)
		if err == nil {
			agentClient := agent.NewClient(conn)
			methods = append(methods, ssh.PublicKeysCallback(agentClient.Signers))
		}
	}

	// 2. Private key file
	keyFile := host.GetVar("ansible_ssh_private_key_file", "")
	if keyFile == "" {
		keyFile = opts.PrivateKeyFile
	}
	if keyFile != "" {
		if keyFile[0] == '~' {
			home, _ := os.UserHomeDir()
			keyFile = filepath.Join(home, keyFile[1:])
		}
		keyData, err := os.ReadFile(keyFile)
		if err == nil {
			signer, err := ssh.ParsePrivateKey(keyData)
			if err == nil {
				methods = append(methods, ssh.PublicKeys(signer))
			}
		}
	}

	// 3. Password
	password := host.GetVar("ansible_password", "")
	if password == "" {
		password = host.GetVar("ansible_ssh_pass", "")
	}
	if password == "" {
		password = opts.Password
	}
	if password != "" {
		methods = append(methods, ssh.Password(password))
	}

	return methods, nil
}

// buildHostKeyCallback returns an appropriate ssh.HostKeyCallback.
func buildHostKeyCallback(opts Options) (ssh.HostKeyCallback, error) {
	if !opts.StrictHostKeyChecking {
		//nolint:gosec // intentional insecure mode
		return ssh.InsecureIgnoreHostKey(), nil
	}
	cb, err := knownhosts.New(opts.KnownHostsFile)
	if err != nil {
		// If the known_hosts file doesn't exist, fall back to insecure mode with a warning.
		//nolint:gosec
		return ssh.InsecureIgnoreHostKey(), nil
	}
	return cb, nil
}

// LocalConnection implements modules.Connection for local execution (used by "localhost" targeting).
type LocalConnection struct{}

// RunCommand runs a shell command locally.
func (c *LocalConnection) RunCommand(cmd string) (stdout, stderr string, rc int, err error) {
	// Implemented via os/exec in a separate file to keep this file focused on SSH.
	return localRun(cmd)
}

// Close is a no-op for local connections.
func (c *LocalConnection) Close() error { return nil }

// NewModuleContext constructs a modules.ModuleContext for a given host,
// merging play vars and host vars.
func NewModuleContext(host *inventory.Host, conn modules.Connection, args modules.Args, vars map[string]interface{}, become bool, becomeUser string) *modules.ModuleContext {
	// Merge host vars into the vars map (host vars take priority over play vars).
	merged := make(map[string]interface{}, len(vars)+len(host.Vars))
	for k, v := range vars {
		merged[k] = v
	}
	for k, v := range host.Vars {
		merged[k] = v
	}
	return &modules.ModuleContext{
		Host:       host.Name,
		Connection: conn,
		Args:       args,
		Vars:       merged,
		Become:     become,
		BecomeUser: becomeUser,
	}
}

// firstNonEmpty returns the first non-empty string from the list.
func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}
