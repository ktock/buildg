package testutil

import (
	"net"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/google/go-dap"
)

type dapOptions struct {
	env        []string
	globalOpts []string
}

func WithDAPServerEnv(env ...string) DAPServerOption {
	return func(o *dapOptions) {
		o.env = env
	}
}

func WithDAPServerGlobalOptions(globalOpts ...string) DAPServerOption {
	return func(o *dapOptions) {
		o.globalOpts = append(o.globalOpts, globalOpts...)
	}
}

type DAPServerOption func(*dapOptions)

func NewDAPServer(t *testing.T, opts ...DAPServerOption) *DAPServer {
	gotOpts := dapOptions{}
	for _, o := range opts {
		o(&gotOpts)
	}

	buildgCmd := getBuildgBinary(t)
	args := []string{"--debug", "dap", "serve"}
	cmd := exec.Command(buildgCmd, append(gotOpts.globalOpts, args...)...)
	cmd.Env = append(os.Environ(), gotOpts.env...)
	c1, c2 := net.Pipe()
	cmd.Stdin = c1
	cmd.Stdout = c1
	cmd.Stderr = os.Stdout
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	return &DAPServer{cmd: cmd, conn: c2}
}

type DAPServer struct {
	cmd       *exec.Cmd
	conn      net.Conn
	closeOnce sync.Once
}

func (s *DAPServer) Conn() net.Conn {
	return s.conn
}

func (s *DAPServer) Wait() (retErr error) {
	_, err := s.cmd.Process.Wait()
	s.conn.Close()
	return err
}

func (s *DAPServer) Close() (retErr error) {
	s.closeOnce.Do(func() {
		if s.cmd.ProcessState != nil && s.cmd.ProcessState.Exited() {
			return
		}
		dap.WriteProtocolMessage(s.conn, &dap.DisconnectRequest{
			Request: dap.Request{
				ProtocolMessage: dap.ProtocolMessage{
					Seq:  99999,
					Type: "request",
				},
				Command: "disconnect",
			},
		})
		s.conn.Close()
		doneCh := make(chan struct{})
		go func() {
			s.cmd.Wait()
			doneCh <- struct{}{}
		}()
		select {
		case <-doneCh:
			return
		case <-time.After(3 * time.Second):
			s.cmd.Process.Kill()
		}
		retErr = s.cmd.Wait()
	})
	return retErr
}
