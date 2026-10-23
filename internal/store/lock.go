package store

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"os"
	"syscall"
)

// Open holds a shared flock on the database file until the *sql.DB is closed;
// Restore takes an exclusive one. flock conflicts between open file
// descriptions, so this holds across processes and within one, and the kernel
// drops it if the process dies. It is how "stop antares first" becomes
// something restore can check instead of assume. It does not see a process
// that uses SQLite without store.Open (e.g. the sqlite3 CLI); restore probes
// the write lock for those.

// errLocked is returned when the lock is held in a conflicting mode.
var errLocked = errors.New("database is locked by another process")

func flockFile(path string, how int) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), how|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errLocked
		}
		return nil, err
	}
	return f, nil
}

func lockShared(path string) (*os.File, error) {
	f, err := flockFile(path, syscall.LOCK_SH)
	if errors.Is(err, errLocked) {
		return nil, errors.New("store: the database is being restored (exclusive lock held); retry when the restore has finished")
	}
	if err != nil {
		return nil, fmt.Errorf("store: lock database: %w", err)
	}
	return f, nil
}

// lockedConnector opens SQLite connections and owns the flock; database/sql
// closes it when the DB is closed.
type lockedConnector struct {
	dsn  string
	drv  driver.Driver
	lock *os.File
}

func (c *lockedConnector) Connect(context.Context) (driver.Conn, error) { return c.drv.Open(c.dsn) }
func (c *lockedConnector) Driver() driver.Driver                        { return c.drv }
func (c *lockedConnector) Close() error                                 { return c.lock.Close() }
