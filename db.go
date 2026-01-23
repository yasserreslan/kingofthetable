package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	mysql "github.com/go-sql-driver/mysql"
)

// Player represents a persisted player record.
type Player struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Wins     int64  `json:"wins"`
	Survives int64  `json:"survives"`
}

// DB wraps a SQL connection.
type DB struct {
	sql *sql.DB
	ops chan dbOp
}

var db *DB // global optional database handle (nil when disabled)

// initDB initializes the DB if MYSQL_DSN is set. Safe to call multiple times.
func initDB() {
	//dsn := strings.TrimSpace(os.Getenv("MYSQL_DSN"))
	dsn := "mysql://kingofthetable_putslipon:402a4db3870197a7856c904513ffd8648d10e131@d8ufga.h.filess.io:3306/kingofthetable_putslipon"
	if dsn == "" {
		log.Printf("[db] MYSQL_DSN not set; persistence disabled")
		return
	}
	// Normalize DSN and add TLS/params if requested
	ndsn, err := normalizeMySQLDSN(dsn)
	if err != nil {
		log.Printf("[db] DSN normalize failed: %v", err)
		return
	}
	ndsn, err = maybeAugmentTLS(ndsn)
	if err != nil {
		log.Printf("[db] TLS setup failed: %v", err)
		return
	}
	ndsn = ensureParams(ndsn, map[string]string{
		"parseTime": "true",
		"charset":   "utf8mb4",
	})

	sqldb, err := sql.Open("mysql", ndsn)
	if err != nil {
		log.Printf("[db] open failed: %v", err)
		return
	}
	// Reasonable defaults; caller can tune via DSN too (params like parseTime=true)
	sqldb.SetConnMaxLifetime(2 * time.Hour)
	sqldb.SetMaxOpenConns(25)
	sqldb.SetMaxIdleConns(10)
	if err := sqldb.Ping(); err != nil {
		log.Printf("[db] ping failed: %v", err)
		_ = sqldb.Close()
		return
	}
	db = &DB{sql: sqldb, ops: make(chan dbOp, 1024)}
	db.startWorker()
	log.Printf("[db] connected to MySQL")
}

// EnsurePlayers inserts players by name (insert-or-ignore) and returns a name->id map.
// Also updates last_seen.
func (d *DB) EnsurePlayers(ctx context.Context, names []string) (map[string]int64, error) {
	if d == nil || d.sql == nil {
		return map[string]int64{}, nil
	}
	// Deduplicate and filter empties
	m := make(map[string]struct{}, len(names))
	uniq := make([]string, 0, len(names))
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		if _, ok := m[n]; !ok {
			m[n] = struct{}{}
			uniq = append(uniq, n)
		}
	}
	if len(uniq) == 0 {
		return map[string]int64{}, nil
	}
	// Batch insert ignore
	// INSERT INTO players (name, last_seen) VALUES (?), (?), ...
	vals := make([]string, 0, len(uniq))
	args := make([]any, 0, len(uniq))
	now := time.Now()
	for range uniq {
		vals = append(vals, "(?, ?)")
	}
	for _, n := range uniq {
		args = append(args, n, now)
	}
	ins := fmt.Sprintf("INSERT INTO players (name, last_seen) VALUES %s ON DUPLICATE KEY UPDATE last_seen=VALUES(last_seen)", strings.Join(vals, ","))
	if _, err := d.sql.ExecContext(ctx, ins, args...); err != nil {
		return nil, err
	}
	// Lookup IDs
	placeholders := strings.TrimRight(strings.Repeat("?,", len(uniq)), ",")
	sel := fmt.Sprintf("SELECT id, name, wins, survives FROM players WHERE name IN (%s)", placeholders)
	rows, err := d.sql.QueryContext(ctx, sel, anySlice(uniq)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]int64, len(uniq))
	for rows.Next() {
		var id int64
		var name string
		var wins, survives int64
		if err := rows.Scan(&id, &name, &wins, &survives); err != nil {
			return nil, err
		}
		out[name] = id
	}
	return out, rows.Err()
}

// SearchPlayers performs a LIKE search against player names.
func (d *DB) SearchPlayers(ctx context.Context, q string, limit int) ([]Player, error) {
	if d == nil || d.sql == nil {
		return nil, errors.New("database disabled")
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	q = strings.TrimSpace(q)
	like := "%" + q + "%"
	rows, err := d.sql.QueryContext(ctx,
		"SELECT id, name, wins, survives FROM players WHERE name LIKE ? ORDER BY wins DESC, survives DESC, name ASC LIMIT ?",
		like, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var res []Player
	for rows.Next() {
		var p Player
		if err := rows.Scan(&p.ID, &p.Name, &p.Wins, &p.Survives); err != nil {
			return nil, err
		}
		res = append(res, p)
	}
	return res, rows.Err()
}

// RecordGoal stores a goal event and updates per-player counters.
// It infers pre-rotation assignments from the provided team and rotation summary.
func (d *DB) RecordGoal(ctx context.Context, gameID string, team string, gs *GameState, rot rotationSummary) error {
	if d == nil || d.sql == nil {
		return nil
	}
	team = strings.ToLower(strings.TrimSpace(team))
	if team != "red" && team != "blue" {
		return fmt.Errorf("invalid team: %s", team)
	}
	// Ensure game row exists
	if _, err := d.sql.ExecContext(ctx, "INSERT IGNORE INTO games (id) VALUES (?)", gameID); err != nil {
		return err
	}
	// Collect all involved player names and resolve to IDs
	names := []string{gs.Red.Forward, gs.Red.Goalkeeper, gs.Blue.Forward, gs.Blue.Goalkeeper, rot.Benched, rot.MovedToGoalkeeper, rot.NewForward}
	nameToID, err := d.EnsurePlayers(ctx, names)
	if err != nil {
		return err
	}
	// Map pre-rotation roles
	var redF, redG, blueF, blueG string
	redF = gs.Red.Forward
	redG = gs.Red.Goalkeeper
	blueF = gs.Blue.Forward
	blueG = gs.Blue.Goalkeeper
	// For the losing team, gs currently reflects post-rotation; use rotationSummary to determine pre-rotation
	if team == "red" {
		// Blue lost: pre blueF was MovedToGoalkeeper, pre blueG was Benched
		blueF = rot.MovedToGoalkeeper
		blueG = rot.Benched
	} else {
		// Red lost
		redF = rot.MovedToGoalkeeper
		redG = rot.Benched
	}
	// Resolve IDs (ignore empty names)
	getID := func(name string) (int64, error) {
		if strings.TrimSpace(name) == "" {
			return 0, fmt.Errorf("empty player name")
		}
		id, ok := nameToID[name]
		if !ok {
			return 0, fmt.Errorf("player not found after ensure: %s", name)
		}
		return id, nil
	}
	redFid, err := getID(redF)
	if err != nil {
		return err
	}
	redGid, err := getID(redG)
	if err != nil {
		return err
	}
	blueFid, err := getID(blueF)
	if err != nil {
		return err
	}
	blueGid, err := getID(blueG)
	if err != nil {
		return err
	}
	benchedID, err := getID(rot.Benched)
	if err != nil {
		return err
	}
	movedID, err := getID(rot.MovedToGoalkeeper)
	if err != nil {
		return err
	}
	newFID, err := getID(rot.NewForward)
	if err != nil {
		return err
	}

	tx, err := d.sql.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	// Insert goal event row
	_, err = tx.ExecContext(ctx,
		`INSERT INTO goal_events (game_id, scoring_team, red_forward_id, red_goalkeeper_id, blue_forward_id, blue_goalkeeper_id, benched_player_id, moved_to_goalkeeper_id, new_forward_id)
         VALUES (?,?,?,?,?,?,?,?,?)`,
		gameID, team, redFid, redGid, blueFid, blueGid, benchedID, movedID, newFID,
	)
	if err != nil {
		return err
	}

	// Update counters
	// Definition proposal:
	// - A "win" increments for both players on the scoring team.
	// - A "survive" increments for every player that remains on the table after the round (both winners + the losing forward who moved to GK).
	// Determine survivors and winners
	var winners []int64
	var survivors []int64
	if team == "red" {
		winners = []int64{redFid, redGid}
		survivors = []int64{redFid, redGid, blueFid /* losing forward survives */}
	} else {
		winners = []int64{blueFid, blueGid}
		survivors = []int64{blueFid, blueGid, redFid}
	}
	// Deduplicate survivors just in case
	survivors = uniqIDs(survivors)

	// Increment wins
	if len(winners) > 0 {
		placeholders := strings.TrimRight(strings.Repeat("?,", len(winners)), ",")
		_, err = tx.ExecContext(ctx, fmt.Sprintf("UPDATE players SET wins = wins + 1 WHERE id IN (%s)", placeholders), anySliceInt64(winners)...)
		if err != nil {
			return err
		}
	}
	// Increment survives
	if len(survivors) > 0 {
		placeholders := strings.TrimRight(strings.Repeat("?,", len(survivors)), ",")
		_, err = tx.ExecContext(ctx, fmt.Sprintf("UPDATE players SET survives = survives + 1 WHERE id IN (%s)", placeholders), anySliceInt64(survivors)...)
		if err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

func uniqIDs(in []int64) []int64 {
	if len(in) == 0 {
		return in
	}
	sort.Slice(in, func(i, j int) bool { return in[i] < in[j] })
	out := in[:0]
	var last int64 = -1
	for i, v := range in {
		if i == 0 || v != last {
			out = append(out, v)
			last = v
		}
	}
	return out
}

func anySlice(ss []string) []any {
	out := make([]any, len(ss))
	for i := range ss {
		out[i] = ss[i]
	}
	return out
}

func anySliceInt64(ns []int64) []any {
	out := make([]any, len(ns))
	for i := range ns {
		out[i] = ns[i]
	}
	return out
}

// --- DSN/TLS helpers ---

// normalizeMySQLDSN converts URL-style or missing-protocol DSNs into go-sql-driver format.
func normalizeMySQLDSN(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	low := strings.ToLower(s)
	if strings.HasPrefix(low, "mysql://") || strings.HasPrefix(low, "mysql+tcp://") {
		u, err := url.Parse(s)
		if err != nil {
			return "", err
		}
		user := ""
		if u.User != nil {
			user = u.User.Username()
			if pw, ok := u.User.Password(); ok {
				user = user + ":" + pw
			}
		}
		host := u.Host // may include :port
		dbname := strings.TrimPrefix(u.Path, "/")
		qs := u.RawQuery
		dsn := fmt.Sprintf("%s@tcp(%s)/%s", user, host, dbname)
		if qs != "" {
			dsn += "?" + qs
		}
		return dsn, nil
	}
	// If DSN looks like user:pass@host:port/db (missing protocol), inject tcp(...)
	if strings.Contains(s, "@") && !strings.Contains(s, ")/") {
		parts := strings.SplitN(s, "@", 2)
		right := parts[1]
		rp := strings.SplitN(right, "/", 2)
		if len(rp) == 2 {
			hostport, rest := rp[0], rp[1]
			return parts[0] + "@tcp(" + hostport + ")/" + rest, nil
		}
	}
	return s, nil
}

// maybeAugmentTLS wires optional TLS params via env:
// MYSQL_TLS=true|skip-verify|custom, with custom CA via MYSQL_TLS_CA (and optional cert/key).
func maybeAugmentTLS(dsn string) (string, error) {
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("MYSQL_TLS")))
	if mode == "" {
		return dsn, nil
	}
	if hasParam(dsn, "tls") {
		return dsn, nil
	}
	name := ""
	switch mode {
	case "true", "preferred", "required":
		name = "true"
	case "skip-verify", "insecure":
		name = "skip-verify"
	case "custom":
		caPath := strings.TrimSpace(os.Getenv("MYSQL_TLS_CA"))
		certPath := strings.TrimSpace(os.Getenv("MYSQL_TLS_CERT"))
		keyPath := strings.TrimSpace(os.Getenv("MYSQL_TLS_KEY"))
		serverName := strings.TrimSpace(os.Getenv("MYSQL_TLS_SERVER_NAME"))

		rootCAs, _ := x509.SystemCertPool()
		if rootCAs == nil {
			rootCAs = x509.NewCertPool()
		}
		if caPath != "" {
			pem, err := ioutil.ReadFile(caPath)
			if err != nil {
				return dsn, fmt.Errorf("read CA: %w", err)
			}
			if ok := rootCAs.AppendCertsFromPEM(pem); !ok {
				return dsn, fmt.Errorf("append CA failed")
			}
		}
		cfg := &tls.Config{RootCAs: rootCAs}
		if serverName != "" {
			cfg.ServerName = serverName
		}
		if certPath != "" && keyPath != "" {
			cert, err := tls.LoadX509KeyPair(certPath, keyPath)
			if err != nil {
				return dsn, fmt.Errorf("load client cert: %w", err)
			}
			cfg.Certificates = []tls.Certificate{cert}
		}
		mysql.RegisterTLSConfig("custom", cfg)
		name = "custom"
	default:
		return dsn, nil
	}
	return addParam(dsn, "tls", name), nil
}

func ensureParams(dsn string, kv map[string]string) string {
	out := dsn
	for k, v := range kv {
		if !hasParam(out, k) {
			out = addParam(out, k, v)
		}
	}
	return out
}

func hasParam(dsn, key string) bool {
	i := strings.Index(dsn, "?")
	if i < 0 {
		return false
	}
	q := dsn[i+1:]
	for _, p := range strings.Split(q, "&") {
		if strings.HasPrefix(strings.ToLower(p), strings.ToLower(key)+"=") {
			return true
		}
	}
	return false
}

func addParam(dsn, key, value string) string {
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	return dsn + sep + url.QueryEscape(key) + "=" + url.QueryEscape(value)
}

// --- Async write queue ---

type dbOpType int

const (
	opEnsurePlayers dbOpType = iota + 1
	opRecordGoal
)

type dbOp struct {
	typ dbOpType
	// ensure players
	names []string
	// record goal
	gameID string
	team   string
	gs     GameState
	rot    rotationSummary
}

// EnqueueEnsurePlayers schedules an insert-or-ignore for the given names.
func (d *DB) EnqueueEnsurePlayers(names []string) {
	if d == nil || d.sql == nil {
		return
	}
	if len(names) == 0 {
		return
	}
	d.ops <- dbOp{typ: opEnsurePlayers, names: append([]string(nil), names...)}
}

// EnqueueRecordGoal schedules a goal event write with stat updates.
func (d *DB) EnqueueRecordGoal(gameID, team string, gs GameState, rot rotationSummary) {
	if d == nil || d.sql == nil {
		return
	}
	d.ops <- dbOp{typ: opRecordGoal, gameID: gameID, team: team, gs: gs, rot: rot}
}

func (d *DB) startWorker() {
	go func() {
		for op := range d.ops {
			// retry forever with backoff (caps at 1m)
			backoff := time.Second
			for {
				var err error
				switch op.typ {
				case opEnsurePlayers:
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					_, err = d.EnsurePlayers(ctx, op.names)
					cancel()
				case opRecordGoal:
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					err = d.RecordGoal(ctx, op.gameID, op.team, &op.gs, op.rot)
					cancel()
				default:
					err = nil
				}
				if err == nil {
					break
				}
				log.Printf("[dbq] op %v failed: %v; retrying in %s", op.typ, err, backoff)
				time.Sleep(backoff)
				if backoff < time.Minute {
					backoff *= 2
					if backoff > time.Minute {
						backoff = time.Minute
					}
				}
			}
		}
	}()
}
