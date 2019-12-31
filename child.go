package tableflip

import (
	"encoding/gob"
	"fmt"
	"os"
	"os/exec"
	"strconv"

	"github.com/pkg/errors"
)

type child struct {
	cmd                             *exec.Cmd
	readyPipeRead, fdNamesPipeWrite *os.File
	ready                           chan *os.File
	finished                        chan error
}

var initialWD, _ = os.Getwd()

func ForkChild(inheritedFiles map[string]*os.File) (*child, error) {
	readyPipeRead, readyPipeWrite, err := os.Pipe()
	if err != nil {
		return nil, errors.Wrap(err, "pipe failed")
	}

	fdNamesPipeRead, fdNamesPipeWrite, err := os.Pipe()
	if err != nil {
		readyPipeRead.Close()
		readyPipeWrite.Close()
		return nil, errors.Wrap(err, "pipe failed")
	}

	// Copy passed fds and append the notification pipe
	fds := []*os.File{readyPipeWrite, fdNamesPipeRead}
	var fdNames []string
	for name, file := range inheritedFiles {
		fdNames = append(fdNames, name)
		fds = append(fds, file)
	}

	// Copy environment and append the notification env vars
	environ := os.Environ()
	environ = append(environ, fmt.Sprintf("%s=yes", sentinelEnvVar))

	cmd := exec.Command(os.Args[0], os.Args[1:]...)
	cmd.Dir = initialWD
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.ExtraFiles = fds
	cmd.Env = environ

	if err := cmd.Start(); err != nil {
		readyPipeRead.Close()
		readyPipeWrite.Close()
		fdNamesPipeRead.Close()
		fdNamesPipeWrite.Close()
		return nil, errors.Wrapf(err, "can't start process %s", os.Args[0])
	}

	c := &child{
		cmd:              cmd,
		readyPipeRead:    readyPipeRead,
		fdNamesPipeWrite: fdNamesPipeWrite,
		ready:            make(chan *os.File, 1),
		finished:         make(chan error, 1),
	}

	go c.writeNames(fdNames)
	go c.waitExit()
	go c.waitReady()

	return c, nil
}

func (c *child) Kill() error {
	return c.cmd.Process.Signal(os.Kill)
}

func (c *child) waitExit() {
	c.finished <- c.cmd.Wait()
	// Unblock waitReady and writeNames
	c.readyPipeRead.Close()
	c.fdNamesPipeWrite.Close()
}

func (c *child) waitReady() {
	var b [1]byte
	if n, _ := c.readyPipeRead.Read(b[:]); n > 0 && b[0] == notifyReady {
		// We know that writeNames has exited by this point.
		// Closing the FD now signals to the child that the parent
		// has exited.
		c.ready <- c.fdNamesPipeWrite
	}
	c.readyPipeRead.Close()
}

func (c *child) writeNames(fdNames []string) {
	enc := gob.NewEncoder(c.fdNamesPipeWrite)
	if fdNames == nil {
		// Gob panics on nil
		_ = enc.Encode([]string{})
		return
	}
	_ = enc.Encode(fdNames)
}

func (c *child) String() string {
	return strconv.Itoa(c.cmd.Process.Pid)
}
