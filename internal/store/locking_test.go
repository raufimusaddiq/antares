package store

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"
)

func lockProcess(t *testing.T, mode, path string, offset int64) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSQLiteLockProcess$")
	cmd.Env = append(os.Environ(), "ANTARES_LOCK_TEST_MODE="+mode, "ANTARES_LOCK_TEST_PATH="+path,
		"ANTARES_LOCK_TEST_OFFSET="+strconv.FormatInt(offset, 10))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s subprocess: %v\n%s", mode, err, out)
	}
}

func TestSQLiteLockProcess(t *testing.T) {
	path := os.Getenv("ANTARES_LOCK_TEST_PATH")
	switch os.Getenv("ANTARES_LOCK_TEST_MODE") {
	case "probe":
		f, err := os.OpenFile(path, os.O_RDWR, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		offset, err := strconv.ParseInt(os.Getenv("ANTARES_LOCK_TEST_OFFSET"), 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		lock := syscall.Flock_t{Type: syscall.F_WRLCK, Whence: 0, Start: offset, Len: 1}
		if err := syscall.FcntlFlock(f.Fd(), syscall.F_GETLK, &lock); err != nil {
			t.Fatal(err)
		}
		if lock.Type == syscall.F_UNLCK || int(lock.Pid) != os.Getppid() {
			t.Fatalf("parent POSIX lock missing at byte %d", offset)
		}
	case "external":
		db, err := sql.Open("sqlite", "file:"+path+"?mode=rw&_pragma=busy_timeout(2000)")
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		if _, err := db.Exec("INSERT INTO probe DEFAULT VALUES"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSecureFilesPreservesPOSIXLocks(t *testing.T) {
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		t.Run("file"+suffix, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "antares.db")
			if err := os.WriteFile(path, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			f, err := os.OpenFile(path+suffix, os.O_CREATE|os.O_RDWR, 0o644)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			lock := syscall.Flock_t{Type: syscall.F_RDLCK, Whence: 0, Start: 128, Len: 1}
			if err := syscall.FcntlFlock(f.Fd(), syscall.F_SETLK, &lock); err != nil {
				t.Fatal(err)
			}
			lockProcess(t, "probe", path+suffix, 128)
			if err := secureFiles(path); err != nil {
				t.Fatal(err)
			}
			lockProcess(t, "probe", path+suffix, 128)
			if mode(t, path+suffix) != 0o600 {
				t.Fatal("permission hardening did not retain 0600")
			}
		})
	}
}

func TestOpenPreservesLocksAcrossExternalSQLite(t *testing.T) {
	db, path := openTest(t)
	if _, err := db.Exec("CREATE TABLE probe (id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	for step := 0; step < 3; step++ {
		switch step {
		case 1:
			if err := secureFiles(path); err != nil {
				t.Fatal(err)
			}
		case 2:
			other, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := other.Close(); err != nil {
				t.Fatal(err)
			}
		}
		lockProcess(t, "probe", path, 1073741826)
		lockProcess(t, "probe", path+"-shm", 128)
		wal, err := os.Stat(path + "-wal")
		if err != nil {
			t.Fatal(err)
		}
		shm, err := os.Stat(path + "-shm")
		if err != nil {
			t.Fatal(err)
		}
		lockProcess(t, "external", path, 0)
		for suffix, before := range map[string]os.FileInfo{"-wal": wal, "-shm": shm} {
			after, err := os.Stat(path + suffix)
			if err != nil || !os.SameFile(before, after) {
				t.Fatalf("external connection replaced %s: %v", suffix, err)
			}
		}
		var count int
		if err := db.QueryRow("SELECT COUNT(*) FROM probe").Scan(&count); err != nil || count != step+1 {
			t.Fatalf("external commit invisible: count=%d err=%v", count, err)
		}
	}
}

func TestSecureFilesRefusesNonRegularFiles(t *testing.T) {
	for _, kind := range []string{"directory", "fifo"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "antares.db")
			if err := os.WriteFile(path, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			var err error
			if kind == "directory" {
				err = os.Mkdir(path+"-wal", 0o700)
			} else {
				err = syscall.Mkfifo(path+"-wal", 0o600)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := secureFiles(path); err == nil {
				t.Fatal("accepted a non-regular sidecar")
			}
		})
	}
}
