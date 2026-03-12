package ssh

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"
)

type ConnectConfig struct {
	Host    string
	User    string
	Port    int
	KeyFile string
}

type Client struct {
	client *ssh.Client
}

func Connect(cfg ConnectConfig) (*Client, error) {
	var authMethods []ssh.AuthMethod

	// Try SSH agent first.
	if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
		conn, err := net.Dial("unix", sock)
		if err == nil {
			authMethods = append(authMethods, ssh.PublicKeysCallback(agent.NewClient(conn).Signers))
		}
	}

	// Try key file.
	if cfg.KeyFile != "" {
		path := cfg.KeyFile
		if path[:2] == "~/" {
			home, err := os.UserHomeDir()
			if err != nil {
				return nil, fmt.Errorf("expanding key file path: %w", err)
			}
			path = filepath.Join(home, path[2:])
		}
		key, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading key file %s: %w", path, err)
		}
		signer, err := ssh.ParsePrivateKey(key)
		if err != nil {
			return nil, fmt.Errorf("parsing key file %s: %w", path, err)
		}
		authMethods = append(authMethods, ssh.PublicKeys(signer))
	}

	if len(authMethods) == 0 {
		return nil, fmt.Errorf("no SSH authentication methods available (set SSH_AUTH_SOCK or use --key-file)")
	}

	// Try to use known_hosts file for host key verification.
	var hostKeyCallback ssh.HostKeyCallback
	knownHostsPath := filepath.Join(os.Getenv("HOME"), ".ssh", "known_hosts")
	if _, err := os.Stat(knownHostsPath); err == nil {
		cb, err := knownhosts.New(knownHostsPath)
		if err == nil {
			hostKeyCallback = cb
		}
	}
	if hostKeyCallback == nil {
		hostKeyCallback = ssh.InsecureIgnoreHostKey()
	}

	sshConfig := &ssh.ClientConfig{
		User:            cfg.User,
		Auth:            authMethods,
		HostKeyCallback: hostKeyCallback,
	}

	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	client, err := ssh.Dial("tcp", addr, sshConfig)
	if err != nil {
		return nil, fmt.Errorf("connecting to %s: %w", addr, err)
	}

	return &Client{client: client}, nil
}

// Run executes a command on the remote server and returns its stdout.
// If the command fails, the error includes stderr.
func (c *Client) Run(cmd string) (string, error) {
	session, err := c.client.NewSession()
	if err != nil {
		return "", fmt.Errorf("creating SSH session: %w", err)
	}
	defer session.Close()

	var stdout, stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr

	if err := session.Run(cmd); err != nil {
		return "", fmt.Errorf("running %q: %w: %s", cmd, err, stderr.String())
	}

	return stdout.String(), nil
}

// Upload streams data from reader to a file at remotePath on the server.
func (c *Client) Upload(reader io.Reader, remotePath string) error {
	session, err := c.client.NewSession()
	if err != nil {
		return fmt.Errorf("creating SSH session: %w", err)
	}
	defer session.Close()

	session.Stdin = reader

	var stderr bytes.Buffer
	session.Stderr = &stderr

	cmd := fmt.Sprintf("cat > %q", remotePath)
	if err := session.Run(cmd); err != nil {
		return fmt.Errorf("uploading to %s: %w: %s", remotePath, err, stderr.String())
	}

	return nil
}

// Dial opens a TCP connection to the given address through the SSH tunnel.
func (c *Client) Dial(network, addr string) (net.Conn, error) {
	return c.client.Dial(network, addr)
}

// RunStreaming executes a command on the remote server, streaming stdout and stderr
// to the provided writers. It blocks until the command exits or the connection drops.
func (c *Client) RunStreaming(cmd string, stdout, stderr io.Writer) error {
	session, err := c.client.NewSession()
	if err != nil {
		return fmt.Errorf("creating SSH session: %w", err)
	}
	defer session.Close()

	session.Stdout = stdout
	session.Stderr = stderr

	return session.Run(cmd)
}

func (c *Client) Close() error {
	return c.client.Close()
}
