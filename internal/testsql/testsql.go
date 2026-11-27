// Package testsql is the PostgreSQL and MySQL servers the programs `sngl test`
// runs query: the script harness's (cmd/sngl/script_test.go). Each is started
// on first use, from the binaries on PATH, in a directory of its own, and
// stopped when the harness is; a program finds it at an address fixed for the
// reason testhttp.Addr is.
package testsql

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
)

const (
	PostgresAddr = "127.0.0.1:47830"
	MySQLAddr    = "127.0.0.1:47831"
)

// Source is what a program opens to reach db on engine, `postgresql` or
// `mysql`.
func Source(engine, db string) string {
	if engine == "mysql" {
		return "root@tcp(" + MySQLAddr + ")/" + db
	}
	return "postgres://sngl@" + PostgresAddr + "/" + db + "?sslmode=disable"
}

// Available reports whether engine's server can be started here.
func Available(engine string) bool {
	switch engine {
	case "postgresql":
		return onPath("initdb") && onPath("postgres")
	case "mysql":
		return (onPath("mariadb-install-db") && onPath("mariadbd")) || onPath("mysqld")
	}
	return false
}

func onPath(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

type server struct {
	once sync.Once
	err  error
	dir  string
	cmd  *exec.Cmd
	db   *sql.DB
}

var (
	mu      sync.Mutex
	servers = map[string]*server{}
)

// Fresh starts engine's server, once, and makes each of dbs a new, empty
// database, dropping one of that name first: a script holds its databases
// from one target's run to the next, and each run starts from none.
func Fresh(engine string, dbs ...string) error {
	if !Available(engine) {
		return fmt.Errorf("no %s server on PATH", engine)
	}
	mu.Lock()
	s := servers[engine]
	if s == nil {
		s = &server{}
		servers[engine] = s
	}
	mu.Unlock()
	s.once.Do(func() { s.err = s.start(engine) })
	if s.err != nil {
		return s.err
	}
	for _, db := range dbs {
		name := `"` + db + `"`
		if engine == "mysql" {
			name = "`" + db + "`"
		}
		drop := "DROP DATABASE IF EXISTS " + name
		if engine == "postgresql" {
			drop += " WITH (FORCE)"
		}
		if _, err := s.db.Exec(drop); err != nil {
			return fmt.Errorf("%s: %w", drop, err)
		}
		if _, err := s.db.Exec("CREATE DATABASE " + name); err != nil {
			return fmt.Errorf("create %s: %w", db, err)
		}
	}
	return nil
}

func (s *server) start(engine string) error {
	dir, err := os.MkdirTemp("", "sngl-"+engine+"-")
	if err != nil {
		return err
	}
	s.dir = dir
	var driver, admin string
	switch engine {
	case "postgresql":
		if out, err := exec.Command("initdb", "-D", dir+"/data", "-U", "sngl", "-A", "trust", "--no-sync").CombinedOutput(); err != nil {
			return fmt.Errorf("initdb: %w: %s", err, out)
		}
		// No Unix socket: one under a long temporary directory passes the
		// 107 bytes a socket's path may hold.
		s.cmd = exec.Command("postgres", "-D", dir+"/data", "-p", strings.Split(PostgresAddr, ":")[1],
			"-k", "", "-c", "listen_addresses=127.0.0.1", "-c", "fsync=off")
		driver, admin = "pgx", Source(engine, "postgres")
	case "mysql":
		// MariaDB takes a socket whatever it is told, so it gets one where a
		// path is short.
		sock, err := os.MkdirTemp("", "my")
		if err != nil {
			return err
		}
		server := "mariadbd"
		if onPath("mariadb-install-db") && onPath("mariadbd") {
			if out, err := exec.Command("mariadb-install-db", "--no-defaults", "--datadir="+dir+"/data",
				"--auth-root-authentication-method=normal", "--skip-test-db").CombinedOutput(); err != nil {
				return fmt.Errorf("mariadb-install-db: %w: %s", err, out)
			}
		} else {
			server = "mysqld"
			if out, err := exec.Command("mysqld", "--no-defaults", "--initialize-insecure", "--datadir="+dir+"/data").CombinedOutput(); err != nil {
				return fmt.Errorf("mysqld --initialize-insecure: %w: %s", err, out)
			}
		}
		s.cmd = exec.Command(server, "--no-defaults", "--datadir="+dir+"/data", "--port="+strings.Split(MySQLAddr, ":")[1],
			"--bind-address=127.0.0.1", "--socket="+sock+"/s", "--pid-file="+dir+"/pid", "--skip-grant-tables")
		driver, admin = "mysql", "root@tcp("+MySQLAddr+")/"
	}
	log, err := os.Create(dir + "/server.log")
	if err != nil {
		return err
	}
	s.cmd.Stdout, s.cmd.Stderr = log, log
	dieWithParent(s.cmd)
	if err := s.cmd.Start(); err != nil {
		return err
	}
	if s.db, err = sql.Open(driver, admin); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for {
		if err := s.db.PingContext(ctx); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			text, _ := os.ReadFile(dir + "/server.log")
			return fmt.Errorf("%s did not start: %s", engine, text)
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// Stop stops every server started and removes its directory.
func Stop() {
	mu.Lock()
	defer mu.Unlock()
	for _, s := range servers {
		if s.db != nil {
			s.db.Close()
		}
		if s.cmd != nil && s.cmd.Process != nil {
			s.cmd.Process.Kill()
			s.cmd.Wait()
		}
		if s.dir != "" {
			os.RemoveAll(s.dir)
		}
	}
	servers = map[string]*server{}
}
