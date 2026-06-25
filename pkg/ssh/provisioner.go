package ssh

import (
	"bytes"
	"context"
	"fmt"
	"time"

	gossh "golang.org/x/crypto/ssh"
)

type Provisioner interface {
	Provision(ctx context.Context, in ProvisionInput) error
	Drain(ctx context.Context, host string, sshKey []byte, user string) error
}

type ProvisionInput struct {
	Host, User string
	Port       int
	PrivateKey []byte
	K3sVersion string
	ServerURL  string
	Token      string
	NodeName   string
}

type sshProvisioner struct{}

func New() Provisioner {
	return &sshProvisioner{}
}

func (p *sshProvisioner) Provision(ctx context.Context, in ProvisionInput) error {
	client, err := dial(ctx, in)
	if err != nil {
		return fmt.Errorf("ssh dial %s: %w", in.Host, err)
	}
	defer func() { _ = client.Close() }()

	steps := []struct {
		name string
		cmd  string
	}{
		{"check-curl", "which curl || (apt-get update -qq && apt-get install -y curl)"},
		{"install-k3s", fmt.Sprintf(
			`curl -sfL https://get.k3s.io | \
 INSTALL_K3S_VERSION=%s \
 INSTALL_K3S_EXEC="agent" \
 K3S_URL=%s \
 K3S_TOKEN=%s \
 K3S_NODE_NAME=%s \
 sh -`,
			in.K3sVersion,
			in.ServerURL,
			in.Token,
			in.NodeName,
		)},
		{"enable-k3s", "systemctl enable k3s-agent && systemctl start k3s-agent"},
	}

	for _, step := range steps {
		if err := runStep(ctx, client, step.name, step.cmd); err != nil {
			return err
		}
	}
	return nil
}

func (p *sshProvisioner) Drain(ctx context.Context, host string, key []byte, user string) error {
	client, err := dial(ctx, ProvisionInput{
		Host: host, User: user, PrivateKey: key, Port: 22,
	})
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	return runStep(ctx, client, "uninstall-k3s", "/usr/local/bin/k3s-agent-uninstall.sh || true")
}

func dial(_ context.Context, in ProvisionInput) (*gossh.Client, error) {
	signer, err := gossh.ParsePrivateKey(in.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("parse key: %w", err)
	}

	port := in.Port
	if port == 0 {
		port = 22
	}

	cfg := &gossh.ClientConfig{
		User:            in.User,
		Auth:            []gossh.AuthMethod{gossh.PublicKeys(signer)},
		HostKeyCallback: gossh.InsecureIgnoreHostKey(),
		Timeout:         30 * time.Second,
	}
	addr := fmt.Sprintf("%s:%d", in.Host, port)
	return gossh.Dial("tcp", addr, cfg)
}

func runStep(ctx context.Context, client *gossh.Client, name, cmd string) error {
	sess, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("new session for %s: %w", name, err)
	}
	defer func() { _ = sess.Close() }()

	var out, errBuf bytes.Buffer
	sess.Stdout = &out
	sess.Stderr = &errBuf

	done := make(chan error, 1)
	go func() { done <- sess.Run(cmd) }()

	select {
	case <-ctx.Done():
		_ = sess.Signal(gossh.SIGTERM)
		return fmt.Errorf("step %s cancelled: %w", name, ctx.Err())
	case err := <-done:
		if err != nil {
			return fmt.Errorf("step %s failed: %w\nstdout: %s\nstderr: %s",
				name, err, out.String(), errBuf.String())
		}
	}
	return nil
}
