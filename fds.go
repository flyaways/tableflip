package tableflip

import (
	"context"
	"net"
	"os"
	"sync"
	"syscall"

	"github.com/pkg/errors"
)

const (
	ListenTCPKind = "listenertcp"
	ListenUDPKind = "listenerudp"
	ConnecTCPKind = "connectitcp"
)

// Fds holds all file descriptors inherited from the parent process.
type Fds struct {
	mu sync.Mutex
	// NB: Files in these maps may be in blocking mode.
	inherited map[string]*os.File
	used      map[string]*os.File
	Reuseport bool
}

// Listen returns a listener inherited from the parent process, or creates a new one.
func (f *Fds) ListenTCP(network, addr string) (net.Listener, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var ln net.Listener
	var err error
	key := ListenTCPKind + ":" + network + ":" + addr
	file := f.inherited[key]
	if file != nil {
		ln, err = net.FileListener(file)
		if err != nil {
			return nil, errors.Wrapf(err, "can't inherit listener %s %s", network, addr)
		}

		delete(f.inherited, key)
		f.used[key] = file

		if ln != nil {
			return ln, nil
		}
	}

	var lfd uintptr
	lc := net.ListenConfig{Control: func(network, address string, conn syscall.RawConn) (err error) {
		fn := func(fd uintptr) {
			lfd = fd
			if f.Reuseport {
				err = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1)
				if err != nil {
					return
				}

				err = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEPORT, 1)
				if err != nil {
					return
				}

				err = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.FD_CLOEXEC, 0)
				if err != nil {
					return
				}
			}
		}

		if err = conn.Control(fn); err != nil {
			return
		}
		return
	}}

	ln, err = lc.Listen(context.Background(), network, addr)
	if err != nil {
		return nil, errors.Wrap(err, "can't create new listener")
	}

	if err := f.addConn(key, lfd); err != nil {
		ln.Close()
		return nil, err
	}

	return ln, nil
}

// ListenUDP returns a listener inherited from the parent process, or creates a new one.
func (f *Fds) ListenUDP(network, addr string) (net.PacketConn, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var ln net.PacketConn
	var err error
	key := ListenUDPKind + ":" + network + ":" + addr
	file := f.inherited[key]
	if file != nil {
		ln, err = net.FilePacketConn(file)
		if err != nil {
			return nil, errors.Wrapf(err, "can't inherit listener %s %s", network, addr)
		}

		delete(f.inherited, key)
		f.used[key] = file

		if ln != nil {
			return ln, nil
		}
	}

	var lfd uintptr
	lc := net.ListenConfig{
		Control: func(network, address string, conn syscall.RawConn) (err error) {
			fn := func(fd uintptr) {
				lfd = fd
				if f.Reuseport {
					err = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1)
					if err != nil {
						return
					}

					err = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEPORT, 1)
					if err != nil {
						return
					}

					err = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.FD_CLOEXEC, 0)
					if err != nil {
						return
					}
				}
			}

			if err = conn.Control(fn); err != nil {
				return
			}
			return
		}}

	ln, err = lc.ListenPacket(context.Background(), network, addr)
	if err != nil {
		return nil, errors.Wrap(err, "ListenPacket")
	}

	//add udp PacketConn locked
	err = f.addConn(key, lfd)
	if err != nil {
		ln.Close()
		return nil, errors.Wrap(err, "addConn")
	}

	return ln, nil
}

type NewConn func(string, string) (net.Conn, uintptr, error)

// Conn returns an inherited connection or nil.It is safe to close the returned Conn.
func (f *Fds) ConnectTCP(network, addr, key string, newconn NewConn) (net.Conn, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	key = ConnecTCPKind + ":" + network + ":" + addr + ":" + key

	if file, ok := f.inherited[key]; ok && file != nil {
		if conn, err := net.FileConn(file); err == nil {
			delete(f.inherited, key)
			f.used[key] = file
			return conn, nil
		}
	}

	conn, fd, err := newconn(network, addr)
	if err != nil {
		return nil, errors.Wrapf(err, "can't newconn %s (%s %s)", ConnecTCPKind, network, addr)
	}

	if err := f.addConn(key, fd); err != nil {
		return nil, errors.Wrapf(err, "can't dup %s (%s %s)", ConnecTCPKind, network, addr)
	}

	return conn, nil
}

func (f *Fds) addConn(key string, fd uintptr) error {
	file, err := f.dupFd(fd, key)
	if err != nil {
		return errors.Wrapf(err, "can't dup %s", key)
	}

	delete(f.inherited, key)
	f.used[key] = file
	return nil
}

func (f *Fds) closeInherited() {
	f.mu.Lock()
	defer f.mu.Unlock()

	for _, file := range f.inherited {
		_ = file.Close()
	}
	f.inherited = make(map[string]*os.File)
}

func (f *Fds) closeUsed() {
	f.mu.Lock()
	defer f.mu.Unlock()

	for _, file := range f.used {
		_ = file.Close()
	}
	f.used = make(map[string]*os.File)
}

func (f *Fds) copy() map[string]*os.File {
	f.mu.Lock()
	defer f.mu.Unlock()

	files := make(map[string]*os.File, len(f.used))
	for key, file := range f.used {
		files[key] = file
	}

	return files
}

func (f *Fds) dupFd(fd uintptr, name string) (*os.File, error) {
	dupfd, _, err := syscall.Syscall(syscall.SYS_FCNTL, fd, syscall.F_DUPFD_CLOEXEC, 0)
	if err != 0 {
		return nil, errors.Wrap(err, "can't dup fd using F_DUPFD_CLOEXEC")
	}

	if f.Reuseport {
		err := syscall.SetsockoptInt(int(dupfd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1)
		if err != nil {
			return nil, errors.Wrap(err, "can't set SO_REUSEADDR")
		}

		err = syscall.SetsockoptInt(int(dupfd), syscall.SOL_SOCKET, syscall.SO_REUSEPORT, 1)
		if err != nil {
			return nil, errors.Wrap(err, "can't set SO_REUSEPORT")
		}
	}

	return os.NewFile(dupfd, name), nil
}
