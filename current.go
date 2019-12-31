package tableflip

import (
	"os"
	"sync"
	"time"

	"github.com/pkg/errors"
)

// DefaultUpgradeTimeout is the duration before the Current kills the new process if no
// readiness notification was received.
const DefaultUpgradeTimeout time.Duration = time.Minute

// Options control the behaviour of the Current.
type Options struct {
	// Time after which an upgrade is considered failed. Defaults to
	// DefaultUpgradeTimeout.
	UpgradeTimeout time.Duration
	// The PID of a ready process is written to this file.
	PIDFile   string
	Reuseport bool
}

// Current handles zero downtime upgrades and passing files between processes.
type Current struct {
	*Fds

	opts      Options
	parent    *parent
	readyOnce sync.Once
	readyC    chan struct{}
	exitC     chan struct{}
	upgradeC  chan chan<- error
	exitFd    chan *os.File
}

var (
	stdEnvMu       sync.Mutex
	stdEnvUpgrader *Current
)

// New creates a new Current. Files are passed from the parent and may be empty.
//
// Only the first call to this function will succeed.
func New(opts Options) (*Current, error) {
	stdEnvMu.Lock()
	defer stdEnvMu.Unlock()

	if stdEnvUpgrader != nil {
		return nil, errors.New("tableflip: only a single Current allowed")
	}

	if initialWD == "" {
		return nil, errors.New("couldn't determine initial working directory")
	}

	parent, inherited, err := FindParent()
	if err != nil {
		return nil, err
	}

	if opts.UpgradeTimeout <= 0 {
		opts.UpgradeTimeout = DefaultUpgradeTimeout
	}

	if inherited == nil {
		inherited = make(map[string]*os.File)
	}

	c := &Current{
		opts:     opts,
		parent:   parent,
		readyC:   make(chan struct{}),
		exitC:    make(chan struct{}),
		upgradeC: make(chan chan<- error),
		exitFd:   make(chan *os.File, 1),
		Fds: &Fds{
			inherited: inherited,
			used:      make(map[string]*os.File),
			Reuseport: opts.Reuseport,
		},
	}

	go c.run()

	// Store a reference to upg in a private global variable, to prevent
	// it from being GC'ed and exitFd being closed prematurely.
	stdEnvUpgrader = c
	return c, nil
}

// Ready signals that the current process is ready to accept connections.
// It must be called to finish the upgrade.
//
// All fds which were inherited but not used are closed after the call to Ready.
func (c *Current) Ready() error {
	c.readyOnce.Do(func() {
		c.Fds.closeInherited()
		close(c.readyC)
	})

	if c.opts.PIDFile != "" {
		if err := writePIDFile(c.opts.PIDFile); err != nil {
			return errors.Wrap(err, "tableflip: can't write PID file")
		}
	}

	if c.parent == nil {
		return nil
	}
	return c.parent.sendReady()
}

// Exit returns a channel which is closed when the process should exit.
func (c *Current) Exit() <-chan struct{} {
	return c.exitC
}

// Upgrade triggers an upgrade.
func (c *Current) Upgrade() error {
	response := make(chan error, 1)
	select {
	case <-c.exitC:
		return errors.New("terminating")
	case c.upgradeC <- response:
	}

	return <-response
}

func (c *Current) run() {
	defer close(c.exitC)

	var (
		parentExited <-chan struct{}
		currentReady = c.readyC
	)

	if c.parent != nil {
		parentExited = c.parent.exited
	}

	for {
		select {
		case <-parentExited:
			parentExited = nil

		case <-currentReady:
			currentReady = nil

		case <-c.exitC:
			c.Fds.closeUsed()
			return

		case request := <-c.upgradeC:
			if currentReady != nil {
				request <- errors.New("process is not ready yet")
				continue
			}

			if parentExited != nil {
				request <- errors.New("parent hasn't exited")
				continue
			}

			file, err := c.doUpgrade()
			request <- err

			if err == nil {
				// Save file in exitFd, so that it's only closed when the process
				// exits. This signals to the new process that the old process
				// has exited.
				c.exitFd <- file

				c.Fds.closeUsed()
				return
			}

			_ = writePIDFile(c.opts.PIDFile)
		}
	}
}

func (c *Current) doUpgrade() (*os.File, error) {
	baby, err := ForkChild(c.Fds.copy())
	if err != nil {
		return nil, errors.Wrap(err, "can't start baby")
	}

	readyTimeout := time.After(c.opts.UpgradeTimeout)

	for {
		select {
		case request := <-c.upgradeC:
			request <- errors.New("upgrade in progress")

		case err := <-baby.finished:
			if err == nil {
				return nil, errors.Errorf("baby %s exited", baby.String())
			}
			return nil, errors.Wrapf(err, "baby %s exited", baby.String())

		case <-c.exitC:
			_ = baby.Kill()
			return nil, errors.New("terminating")

		case <-readyTimeout:
			_ = baby.Kill()
			return nil, errors.Errorf("new baby %s timed out", baby.String())

		case file := <-baby.ready:
			return file, nil
		}
	}
}
