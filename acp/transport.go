package acp

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"

	"aimuxterm/terminal"

	"golang.org/x/crypto/ssh"
)

// Transport is a bidirectional stdio stream to an ACP agent.
type Transport interface {
	Stdout() io.Reader
	Stdin() io.Writer
	Stderr() io.Reader
	Wait() error
	Close() error
}

type localTransport struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr io.ReadCloser
	once   sync.Once
}

func startLocalTransport(ctx context.Context, launch AgentLaunch, cwd string) (*localTransport, error) {
	if strings.TrimSpace(launch.Command) == "" {
		return nil, fmt.Errorf("agent command is empty")
	}
	cmd := exec.CommandContext(ctx, launch.Command, launch.Args...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	cmd.Env = os.Environ()
	for k, v := range launch.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, err
	}
	configureBackgroundProcess(cmd)
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		_ = stderr.Close()
		return nil, fmt.Errorf("start agent: %w", err)
	}
	return &localTransport{cmd: cmd, stdin: stdin, stdout: stdout, stderr: stderr}, nil
}

func (t *localTransport) Stdout() io.Reader { return t.stdout }
func (t *localTransport) Stdin() io.Writer  { return t.stdin }
func (t *localTransport) Stderr() io.Reader { return t.stderr }
func (t *localTransport) Wait() error {
	if t.cmd == nil {
		return nil
	}
	return t.cmd.Wait()
}
func (t *localTransport) Close() error {
	var err error
	t.once.Do(func() {
		if t.stdin != nil {
			_ = t.stdin.Close()
		}
		if t.cmd != nil && t.cmd.Process != nil {
			_ = t.cmd.Process.Kill()
		}
		if t.cmd != nil {
			err = t.cmd.Wait()
		}
	})
	return err
}

type sshTransport struct {
	client  *ssh.Client
	session *ssh.Session
	stdin   io.WriteCloser
	stdout  io.Reader
	stderr  io.Reader
	once    sync.Once
}

func startSSHTransport(launch AgentLaunch, cwd string, params SSHParams) (*sshTransport, error) {
	cmdName := launch.Command
	args := launch.Args
	if strings.TrimSpace(launch.RemoteCommand) != "" {
		cmdName = launch.RemoteCommand
		args = launch.RemoteArgs
	}
	if strings.TrimSpace(cmdName) == "" {
		return nil, fmt.Errorf("remote agent command is empty")
	}

	cfg := terminal.SSHConfig{
		Host:     params.Host,
		Port:     params.Port,
		User:     params.User,
		Password: params.Password,
		KeyPath:  params.KeyPath,
	}
	if cfg.Port == 0 {
		cfg.Port = 22
	}
	auth, err := terminal.BuildSSHAuth(cfg)
	if err != nil {
		return nil, err
	}
	client, err := ssh.Dial("tcp", fmt.Sprintf("%s:%d", cfg.Host, cfg.Port), &ssh.ClientConfig{
		User:            cfg.User,
		Auth:            auth,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	})
	if err != nil {
		return nil, fmt.Errorf("ssh dial: %w", err)
	}
	session, err := client.NewSession()
	if err != nil {
		_ = client.Close()
		return nil, err
	}
	stdin, err := session.StdinPipe()
	if err != nil {
		_ = session.Close()
		_ = client.Close()
		return nil, err
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		_ = session.Close()
		_ = client.Close()
		return nil, err
	}
	stderr, err := session.StderrPipe()
	if err != nil {
		_ = session.Close()
		_ = client.Close()
		return nil, err
	}

	remoteCmd := buildRemoteCommand(cmdName, args, cwd, launch.Env)
	if err := session.Start(remoteCmd); err != nil {
		_ = session.Close()
		_ = client.Close()
		return nil, fmt.Errorf("start remote agent: %w", err)
	}
	return &sshTransport{
		client:  client,
		session: session,
		stdin:   stdin,
		stdout:  stdout,
		stderr:  stderr,
	}, nil
}

func buildRemoteCommand(command string, args []string, cwd string, env map[string]string) string {
	parts := make([]string, 0, 8+len(args)+len(env))
	if cwd != "" {
		parts = append(parts, "cd", shellQuote(cwd), "&&")
	}
	for k, v := range env {
		parts = append(parts, fmt.Sprintf("export %s=%s", shellQuote(k), shellQuote(v)))
		parts = append(parts, "&&")
	}
	parts = append(parts, shellQuote(command))
	for _, a := range args {
		parts = append(parts, shellQuote(a))
	}
	return strings.Join(parts, " ")
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

func (t *sshTransport) Stdout() io.Reader { return t.stdout }
func (t *sshTransport) Stdin() io.Writer  { return t.stdin }
func (t *sshTransport) Stderr() io.Reader { return t.stderr }
func (t *sshTransport) Wait() error {
	if t.session == nil {
		return nil
	}
	return t.session.Wait()
}
func (t *sshTransport) Close() error {
	var err error
	t.once.Do(func() {
		if t.stdin != nil {
			_ = t.stdin.Close()
		}
		if t.session != nil {
			_ = t.session.Signal(ssh.SIGKILL)
			err = t.session.Close()
		}
		if t.client != nil {
			_ = t.client.Close()
		}
	})
	return err
}
