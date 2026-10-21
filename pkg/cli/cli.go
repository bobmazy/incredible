package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"
)

// waitDelay bounds how long a command's I/O pipes are waited for after it has
// been killed or has exited. Wrappers like the az script or bw.cmd don't pass
// the kill on to the process they start, which keeps the pipes open.
var waitDelay = 5 * time.Second

type Cli struct {
	Executable     string
	Ctx            context.Context
	AdditionalEnvs []string
}

func (c Cli) WithEnv(name string, value string) Cli {
	return Cli{
		Executable:     c.Executable,
		Ctx:            c.Ctx,
		AdditionalEnvs: append(c.AdditionalEnvs, fmt.Sprintf("%s=%s", name, value)),
	}
}

// newCommand returns the command and a function that must be called with the
// result of running it. That function replaces the error with the cause if
// the context is done.
func (c Cli) newCommand(args ...string) (*exec.Cmd, func(error) error) {
	ctx := c.Ctx
	if ctx == nil {
		ctx = context.Background()
	}

	cmd := exec.CommandContext(ctx, c.Executable, args...)
	cmd.WaitDelay = waitDelay

	if c.AdditionalEnvs != nil && len(c.AdditionalEnvs) > 0 {
		cmd.Env = cmd.Environ()
		cmd.Env = append(cmd.Env, c.AdditionalEnvs...)
	}

	finish := func(err error) error {
		if err != nil && ctx.Err() != nil {
			return context.Cause(ctx)
		}
		return err
	}
	return cmd, finish
}

func (c Cli) RunInheritStreams(args ...string) error {
	cmd, finish := c.newCommand(args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return finish(cmd.Run())
}

func (c Cli) RunReadStdout(args ...string) (string, error) {
	cmd, finish := c.newCommand(args...)
	buff := bytes.Buffer{}
	cmd.Stdout = &buff
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	err := cmd.Run()
	if errors.Is(err, exec.ErrWaitDelay) {
		// The command exited successfully, but left a process behind that
		// still holds stdout. All output of the command has been read.
		err = nil
	}
	return buff.String(), finish(err)
}

func (c Cli) RunRedirectStdout(args ...string) (io.Reader, error) {
	cmd, finish := c.newCommand(args...)
	reader, writer := io.Pipe()
	cmd.Stdout = writer
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	startErr := cmd.Start()
	if startErr != nil {
		return nil, finish(startErr)
	}

	go func() {
		// The context must stay alive until the command has finished.
		err := finish(cmd.Wait())
		if err != nil {
			_ = writer.CloseWithError(err)
		} else {
			_ = writer.Close()
		}
	}()

	return reader, nil
}
