package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"os"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
)

// identity is what the server says about itself: the isolation evidence of every phase.
type identity struct {
	Database string `json:"database"`
	Addr     string `json:"server_addr"`
	Port     int    `json:"server_port"`
	Version  string `json:"version"`
}

// connect opens one connection and refuses servers outside loopback (see the package comment).
func connect(ctx context.Context, raw string) (*pgx.Conn, identity, error) {
	var id identity
	u, err := url.Parse(raw)
	if err != nil {
		return nil, id, fmt.Errorf("database URL: %w", err)
	}
	if os.Getenv("C6_ALLOW_NON_LOOPBACK") != "yes" && !loopbackHost(u.Hostname()) {
		return nil, id, fmt.Errorf("refusing %q: only a loopback database is allowed (throwaway cluster)", u.Hostname())
	}
	conn, err := pgx.Connect(ctx, raw)
	if err != nil {
		return nil, id, err
	}
	err = conn.QueryRow(ctx, `SELECT current_database(), coalesce(host(inet_server_addr()), 'local socket'),
		coalesce(inet_server_port(), 0), current_setting('server_version')`).Scan(&id.Database, &id.Addr, &id.Port, &id.Version)
	if err != nil {
		conn.Close(ctx)
		return nil, id, err
	}
	// A server in a local container reports its bridge address (private); a public address means
	// the loopback URL is a tunnel to some other machine.
	if a, err := netip.ParseAddr(id.Addr); err == nil && !a.IsLoopback() && !a.IsPrivate() && os.Getenv("C6_ALLOW_NON_LOOPBACK") != "yes" {
		conn.Close(ctx)
		return nil, id, fmt.Errorf("refusing: the server reports the public address %s", id.Addr)
	}
	return conn, id, nil
}

func loopbackHost(h string) bool {
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

func cmdWhoami(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("whoami", flag.ContinueOnError)
	db := fs.String("db", "", "database URL")
	if err := fs.Parse(args); err != nil {
		return err
	}
	conn, id, err := connect(ctx, *db)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)
	fmt.Printf("database=%s server=%s:%d version=%s\n", id.Database, id.Addr, id.Port, id.Version)
	return nil
}

var dbNameRE = regexp.MustCompile(`^[a-z][a-z0-9_]{2,40}$`)

// cmdCreateDB creates a database owned by rokh_owner with the same per-database setup as
// deploy/local/postgres/roles.sql and prints OWNER_URL, APP_URL and DB_NAME lines.
func cmdCreateDB(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("createdb", flag.ContinueOnError)
	admin := fs.String("admin", "", "superuser URL of the throwaway cluster")
	owner := fs.String("owner", "", "rokh_owner URL (any database)")
	app := fs.String("app", "", "rokh_app URL (any database)")
	prefix := fs.String("prefix", "rokh_e2e", "database name prefix")
	if err := fs.Parse(args); err != nil {
		return err
	}
	var b [5]byte
	_, _ = rand.Read(b[:])
	name := *prefix + "_" + hex.EncodeToString(b[:])
	if !dbNameRE.MatchString(name) || name == "rokhtiacafe" {
		return fmt.Errorf("bad database name %q", name)
	}
	if err := refuseLiveCluster(ctx, *admin); err != nil {
		return err
	}
	conn, _, err := connect(ctx, *admin)
	if err != nil {
		return err
	}
	ident := pgx.Identifier{name}.Sanitize()
	if _, err := conn.Exec(ctx, "CREATE DATABASE "+ident+" OWNER rokh_owner"); err != nil {
		conn.Close(ctx)
		return err
	}
	conn.Close(ctx)
	conn, _, err = connect(ctx, withDB(*admin, name))
	if err != nil {
		return err
	}
	defer conn.Close(ctx)
	for _, stmt := range []string{
		"CREATE SCHEMA rokh AUTHORIZATION rokh_owner",
		"REVOKE CREATE ON SCHEMA public FROM PUBLIC",
		"REVOKE ALL ON DATABASE " + ident + " FROM PUBLIC",
		"GRANT CONNECT ON DATABASE " + ident + " TO rokh_app",
	} {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("%s: %w", stmt, err)
		}
	}
	fmt.Printf("DB_NAME=%s\nOWNER_URL=%s\nAPP_URL=%s\n", name, withDB(*owner, name), withDB(*app, name))
	return nil
}

func cmdDropDB(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("dropdb", flag.ContinueOnError)
	admin := fs.String("admin", "", "superuser URL of the throwaway cluster")
	name := fs.String("name", "", "database created by createdb")
	prefix := fs.String("prefix", "rokh_e2e", "required name prefix")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !dbNameRE.MatchString(*name) || !strings.HasPrefix(*name, *prefix+"_") {
		return errors.New("dropdb only drops databases created by createdb")
	}
	conn, _, err := connect(ctx, *admin)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)
	_, err = conn.Exec(ctx, "DROP DATABASE IF EXISTS "+pgx.Identifier{*name}.Sanitize()+" WITH (FORCE)")
	return err
}

// refuseLiveCluster is the second fence: a throwaway test cluster has an empty "rokhtiacafe"
// database (deploy/local/postgres/roles.sql); a real one holds orders.
func refuseLiveCluster(ctx context.Context, admin string) error {
	conn, _, err := connect(ctx, withDB(admin, "rokhtiacafe"))
	if err != nil {
		return nil // no such database: nothing that looks live
	}
	defer conn.Close(ctx)
	var orders int64
	if err := conn.QueryRow(ctx, "SELECT CASE WHEN to_regclass('rokh.orders') IS NULL THEN 0 ELSE 1 END").Scan(&orders); err != nil || orders == 0 {
		return err
	}
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM rokh.orders").Scan(&orders); err != nil {
		return err
	}
	if orders > 0 {
		return fmt.Errorf("refusing: database rokhtiacafe on this cluster holds %d orders, so it is not a throwaway cluster", orders)
	}
	return nil
}

func withDB(raw, name string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	u.Path = "/" + name
	return u.String()
}
