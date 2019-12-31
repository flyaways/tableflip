package tableflip

import (
	"encoding/gob"
	"io"
	"io/ioutil"
	"os"
	"syscall"

	"github.com/pkg/errors"
)

const (
	sentinelEnvVar = "TABLEFLIP_HAS_PARENT_7DIU3"
	notifyReady    = 42
)

type parent struct {
	readyPipeWrite *os.File
	exited         <-chan struct{}
}

func FindParent() (*parent, map[string]*os.File, error) {
	if os.Getenv(sentinelEnvVar) == "" {
		return nil, make(map[string]*os.File), nil
	}

	readyPipeWrite := os.NewFile(3, "write")
	fdNamesRead := os.NewFile(4, "read")

	var names []string
	dec := gob.NewDecoder(fdNamesRead)
	if err := dec.Decode(&names); err != nil {
		return nil, nil, errors.Wrap(err, "can't decode names from parent process")
	}

	files := make(map[string]*os.File)
	for i, key := range names {
		// Start at 5 to account for stdin, etc. and writev and read pipes.
		fd := 5 + i
		syscall.CloseOnExec(fd)
		files[key] = os.NewFile(uintptr(fd), key)
	}

	exited := make(chan struct{})
	go func() {
		defer fdNamesRead.Close()
		_, _ = io.Copy(ioutil.Discard, fdNamesRead)
		close(exited)
	}()

	return &parent{
		readyPipeWrite: readyPipeWrite,
		exited:         exited,
	}, files, nil
}

func (ps *parent) sendReady() error {
	defer ps.readyPipeWrite.Close()
	if _, err := ps.readyPipeWrite.Write([]byte{notifyReady}); err != nil {
		return errors.Wrap(err, "can't notify parent process")
	}
	return nil
}
