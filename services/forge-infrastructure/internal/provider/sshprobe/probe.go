package sshprobe

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// Capacity is a host's probed CPU/memory.
type Capacity struct {
	CPU       int
	MemoryMiB int
}

// Prober checks SSH connectivity and reads capacity.
type Prober interface {
	// Dial authenticates and runs a trivial command; returns nil on success.
	Dial(ctx context.Context, address, user string, privateKey []byte) error
	// ProbeCapacity runs nproc and parses /proc/meminfo MemTotal.
	ProbeCapacity(ctx context.Context, address, user string, privateKey []byte) (Capacity, error)
	// Run executes a remote script/command over SSH.
	Run(ctx context.Context, address, user string, privateKey []byte, command string) (stdout string, err error)
}

// Config tunes SSH dial behaviour.
type Config struct {
	ConnectTimeout time.Duration
	Port           int
}

// SSHProber is the real golang.org/x/crypto/ssh implementation.
type SSHProber struct {
	cfg Config
}

// New returns a real SSH prober.
func New(cfg Config) *SSHProber {
	if cfg.ConnectTimeout <= 0 {
		cfg.ConnectTimeout = 10 * time.Second
	}
	if cfg.Port <= 0 {
		cfg.Port = 22
	}
	return &SSHProber{cfg: cfg}
}

func (p *SSHProber) Dial(ctx context.Context, address, user string, privateKey []byte) error {
	_, err := p.ProbeCapacity(ctx, address, user, privateKey)
	return err
}

func (p *SSHProber) ProbeCapacity(ctx context.Context, address, user string, privateKey []byte) (Capacity, error) {
	out, err := p.Run(ctx, address, user, privateKey, `nproc; awk '/MemTotal/ {print $2}' /proc/meminfo`)
	if err != nil {
		return Capacity{}, err
	}
	return parseCapacity(out)
}

func (p *SSHProber) Run(ctx context.Context, address, user string, privateKey []byte, command string) (string, error) {
	client, err := p.dial(ctx, address, user, privateKey)
	if err != nil {
		return "", err
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer session.Close()

	var stdout, stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr

	done := make(chan error, 1)
	go func() { done <- session.Run(command) }()
	select {
	case <-ctx.Done():
		_ = session.Close()
		return "", ctx.Err()
	case err := <-done:
		if err != nil {
			return stdout.String(), fmt.Errorf("ssh run: %w: %s", err, strings.TrimSpace(stderr.String()))
		}
		return stdout.String(), nil
	}
}

func (p *SSHProber) dial(ctx context.Context, address, user string, privateKey []byte) (*ssh.Client, error) {
	signer, err := ssh.ParsePrivateKey(privateKey)
	if err != nil {
		return nil, fmt.Errorf("parse private key: %w", err)
	}
	host := address
	if !strings.Contains(host, ":") {
		host = net.JoinHostPort(host, strconv.Itoa(p.cfg.Port))
	}
	cfg := &ssh.ClientConfig{
		User:            user,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // inventory hosts are operator-declared
		Timeout:         p.cfg.ConnectTimeout,
	}
	d := net.Dialer{Timeout: p.cfg.ConnectTimeout}
	conn, err := d.DialContext(ctx, "tcp", host)
	if err != nil {
		return nil, fmt.Errorf("ssh dial %s: %w", host, err)
	}
	cc, chans, reqs, err := ssh.NewClientConn(conn, host, cfg)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("ssh handshake %s: %w", host, err)
	}
	return ssh.NewClient(cc, chans, reqs), nil
}

func parseCapacity(out string) (Capacity, error) {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 2 {
		return Capacity{}, fmt.Errorf("unexpected capacity output: %q", out)
	}
	cpu, err := strconv.Atoi(strings.TrimSpace(lines[0]))
	if err != nil || cpu < 1 {
		return Capacity{}, fmt.Errorf("parse nproc: %q", lines[0])
	}
	kb, err := strconv.Atoi(strings.TrimSpace(lines[1]))
	if err != nil || kb < 1 {
		return Capacity{}, fmt.Errorf("parse MemTotal: %q", lines[1])
	}
	return Capacity{CPU: cpu, MemoryMiB: kb / 1024}, nil
}

// Fake is a test double that records calls and returns configured results.
type Fake struct {
	DialErr     error
	Capacity    Capacity
	CapacityErr error
	RunErr      error
	Runs        []string
	Unreachable map[string]bool
}

// NewFake returns a Fake with default 2 CPU / 4096 MiB capacity.
func NewFake() *Fake {
	return &Fake{
		Capacity:    Capacity{CPU: 2, MemoryMiB: 4096},
		Unreachable: map[string]bool{},
	}
}

func (f *Fake) Dial(ctx context.Context, address, user string, privateKey []byte) error {
	_ = ctx
	_ = user
	_ = privateKey
	if f.Unreachable[address] {
		return fmt.Errorf("host %s unreachable", address)
	}
	return f.DialErr
}

func (f *Fake) ProbeCapacity(ctx context.Context, address, user string, privateKey []byte) (Capacity, error) {
	if err := f.Dial(ctx, address, user, privateKey); err != nil {
		return Capacity{}, err
	}
	if f.CapacityErr != nil {
		return Capacity{}, f.CapacityErr
	}
	return f.Capacity, nil
}

func (f *Fake) Run(ctx context.Context, address, user string, privateKey []byte, command string) (string, error) {
	if err := f.Dial(ctx, address, user, privateKey); err != nil {
		return "", err
	}
	f.Runs = append(f.Runs, command)
	if f.RunErr != nil {
		return "", f.RunErr
	}
	return "", nil
}
