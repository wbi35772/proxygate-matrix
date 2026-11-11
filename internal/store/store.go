package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ncruces/go-sqlite3"
	sqlite3driver "github.com/ncruces/go-sqlite3/driver"

	"github.com/ExpTechTW/proxygate/internal/model"
)

type Store struct{ db *sql.DB }

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}
	db, err := sqlite3driver.Open(path, configureSQLite)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	store := &Store{db: db}
	if err := store.initialize(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error { return s.db.Close() }

func configureSQLite(conn *sqlite3.Conn) error {
	if err := conn.BusyTimeout(5 * time.Second); err != nil {
		return err
	}
	return conn.Exec(`PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON;`)
}

func (s *Store) initialize(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS nodes (
  ip TEXT PRIMARY KEY, host_name TEXT NOT NULL, port INTEGER NOT NULL,
  score INTEGER NOT NULL, ping_ms INTEGER NOT NULL,
  speed_bps INTEGER NOT NULL, country_long TEXT NOT NULL, country_short TEXT NOT NULL,
  sessions INTEGER NOT NULL, uptime_ms INTEGER NOT NULL, total_users INTEGER NOT NULL,
  total_traffic INTEGER NOT NULL, log_type TEXT NOT NULL, operator TEXT NOT NULL, message TEXT NOT NULL,
  openvpn_config TEXT NOT NULL, protocols TEXT NOT NULL, measured_bps INTEGER NOT NULL DEFAULT 0,
  measured_at TEXT NOT NULL DEFAULT '', speed_test_failed INTEGER NOT NULL DEFAULT 0,
  last_failure TEXT NOT NULL DEFAULT '', failure_reason TEXT NOT NULL DEFAULT '',
  refreshed_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS metadata (key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS matrix_nodes (
  ip TEXT NOT NULL, port INTEGER NOT NULL, protocol TEXT NOT NULL,
  host_name TEXT NOT NULL, score INTEGER NOT NULL DEFAULT 0, ping_ms INTEGER NOT NULL DEFAULT 0,
  speed_bps INTEGER NOT NULL DEFAULT 0, country_long TEXT NOT NULL DEFAULT '',
  country_short TEXT NOT NULL DEFAULT '', sessions INTEGER NOT NULL DEFAULT 0,
  uptime_ms INTEGER NOT NULL DEFAULT 0, total_users INTEGER NOT NULL DEFAULT 0,
  total_traffic INTEGER NOT NULL DEFAULT 0, log_type TEXT NOT NULL DEFAULT '',
  operator TEXT NOT NULL DEFAULT '', message TEXT NOT NULL DEFAULT '',
  openvpn_config TEXT NOT NULL DEFAULT '', protocols TEXT NOT NULL DEFAULT '[]',
  asn TEXT NOT NULL DEFAULT '', asn_org TEXT NOT NULL DEFAULT '',
  residential INTEGER NOT NULL DEFAULT 1, fail_count INTEGER NOT NULL DEFAULT 0,
  quality_score REAL NOT NULL DEFAULT 50, cooldown_until TEXT NOT NULL DEFAULT '',
  success_count INTEGER NOT NULL DEFAULT 0, last_success TEXT NOT NULL DEFAULT '',
  source TEXT NOT NULL DEFAULT '', first_seen TEXT NOT NULL DEFAULT '',
  last_seen TEXT NOT NULL DEFAULT '', last_failure TEXT NOT NULL DEFAULT '',
  failure_reason TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (ip, port, protocol)
);
CREATE INDEX IF NOT EXISTS idx_nodes_speed ON nodes(speed_bps DESC);
CREATE INDEX IF NOT EXISTS idx_nodes_ping ON nodes(ping_ms ASC);
CREATE INDEX IF NOT EXISTS idx_nodes_score ON nodes(score DESC);
CREATE INDEX IF NOT EXISTS idx_matrix_rank ON matrix_nodes(residential DESC, fail_count ASC, speed_bps DESC);
CREATE INDEX IF NOT EXISTS idx_matrix_country ON matrix_nodes(country_short, residential DESC, fail_count ASC);
CREATE INDEX IF NOT EXISTS idx_matrix_seen ON matrix_nodes(last_seen);
DELETE FROM nodes WHERE instr(ip, ':') > 0;
`); err != nil {
		return err
	}
	if err := s.ensurePoolQualityColumns(ctx); err != nil {
		return err
	}
	var currentColumns int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM pragma_table_info('nodes') WHERE name IN ('port','speed_test_failed')`).Scan(&currentColumns); err != nil {
		return err
	}
	if currentColumns != 2 {
		return errors.New("SQLite schema is outdated; recreate the database")
	}
	return nil
}

// ensurePoolQualityColumns upgrades an existing matrix_nodes table in place so
// quality scores and cooldowns do not require wiping a populated pool.
func (s *Store) ensurePoolQualityColumns(ctx context.Context) error {
	columns := []struct{ name, definition string }{
		{"quality_score", "REAL NOT NULL DEFAULT 50"},
		{"cooldown_until", "TEXT NOT NULL DEFAULT ''"},
		{"success_count", "INTEGER NOT NULL DEFAULT 0"},
		{"last_success", "TEXT NOT NULL DEFAULT ''"},
	}
	for _, column := range columns {
		var present int
		if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM pragma_table_info('matrix_nodes') WHERE name=?`, column.name).Scan(&present); err != nil {
			return err
		}
		if present > 0 {
			continue
		}
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE matrix_nodes ADD COLUMN `+column.name+` `+column.definition); err != nil {
			return err
		}
	}
	if _, err := s.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_matrix_quality ON matrix_nodes(country_short, quality_score DESC)`); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `UPDATE matrix_nodes SET quality_score=50 WHERE quality_score=0 AND fail_count=0`)
	return err
}

func (s *Store) ReplaceNodes(ctx context.Context, nodes []model.Node, refreshedAt time.Time, preserveIP string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `CREATE TEMP TABLE incoming_nodes AS SELECT * FROM nodes WHERE 0`); err != nil {
		return err
	}
	statement, err := tx.PrepareContext(ctx, `INSERT INTO incoming_nodes
	(ip,host_name,port,score,ping_ms,speed_bps,country_long,country_short,sessions,uptime_ms,total_users,total_traffic,log_type,operator,message,openvpn_config,protocols,measured_bps,measured_at,speed_test_failed,last_failure,failure_reason,refreshed_at)
	VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,COALESCE((SELECT measured_bps FROM nodes WHERE ip=?),0),COALESCE((SELECT measured_at FROM nodes WHERE ip=?),''),COALESCE((SELECT speed_test_failed FROM nodes WHERE ip=?),0),COALESCE((SELECT last_failure FROM nodes WHERE ip=?),''),COALESCE((SELECT failure_reason FROM nodes WHERE ip=?),''),?)`)
	if err != nil {
		return err
	}
	for _, node := range nodes {
		protocols, _ := json.Marshal(node.Protocols)
		if _, err := statement.ExecContext(ctx, node.IP, node.HostName, node.Port, node.Score, node.PingMS, node.SpeedBPS,
			node.CountryLong, node.CountryShort, node.Sessions, node.UptimeMS, node.TotalUsers, node.TotalTraffic,
			node.LogType, node.Operator, node.Message, node.OpenVPNConfig, string(protocols), node.IP, node.IP, node.IP, node.IP, node.IP, formatTime(refreshedAt)); err != nil {
			return err
		}
	}
	if err := statement.Close(); err != nil {
		return err
	}
	if preserveIP != "" {
		if _, err := tx.ExecContext(ctx, `INSERT INTO incoming_nodes SELECT * FROM nodes WHERE ip=? AND NOT EXISTS (SELECT 1 FROM incoming_nodes WHERE ip=?)`, preserveIP, preserveIP); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM nodes; INSERT INTO nodes SELECT * FROM incoming_nodes; DROP TABLE incoming_nodes`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO metadata(key,value) VALUES('last_refresh',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, formatTime(refreshedAt)); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) DeleteNodeIfStale(ctx context.Context, ip string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM nodes WHERE ip=? AND refreshed_at<>(SELECT value FROM metadata WHERE key='last_refresh')`, ip)
	return err
}

func (s *Store) Nodes(ctx context.Context, mode string, limit, offset int) ([]model.Node, error) {
	order := orderBy(mode)
	rows, err := s.db.QueryContext(ctx, `SELECT host_name,ip,port,score,ping_ms,speed_bps,country_long,country_short,sessions,uptime_ms,total_users,total_traffic,log_type,operator,message,openvpn_config,protocols,measured_bps,measured_at,speed_test_failed,last_failure,failure_reason,refreshed_at FROM nodes ORDER BY `+order+` LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]model.Node, 0)
	for rows.Next() {
		node, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, node)
	}
	return result, rows.Err()
}

func (s *Store) Node(ctx context.Context, ip string) (model.Node, error) {
	row := s.db.QueryRowContext(ctx, `SELECT host_name,ip,port,score,ping_ms,speed_bps,country_long,country_short,sessions,uptime_ms,total_users,total_traffic,log_type,operator,message,openvpn_config,protocols,measured_bps,measured_at,speed_test_failed,last_failure,failure_reason,refreshed_at FROM nodes WHERE ip=?`, ip)
	return scanNode(row)
}

type scanner interface{ Scan(...any) error }

func scanNode(row scanner) (model.Node, error) {
	var node model.Node
	var protocols, measuredAt, lastFailure, refreshedAt string
	err := row.Scan(&node.HostName, &node.IP, &node.Port, &node.Score, &node.PingMS, &node.SpeedBPS, &node.CountryLong,
		&node.CountryShort, &node.Sessions, &node.UptimeMS, &node.TotalUsers, &node.TotalTraffic, &node.LogType,
		&node.Operator, &node.Message, &node.OpenVPNConfig, &protocols, &node.MeasuredBPS, &measuredAt,
		&node.SpeedTestFailed, &lastFailure, &node.FailureReason, &refreshedAt)
	if err != nil {
		return node, err
	}
	_ = json.Unmarshal([]byte(protocols), &node.Protocols)
	node.MeasuredAt = parseTime(measuredAt)
	node.LastFailure = parseTime(lastFailure)
	node.RefreshedAt = parseTime(refreshedAt)
	return node, nil
}

func (s *Store) MarkFailure(ctx context.Context, ip, reason string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE nodes SET last_failure=?, failure_reason=? WHERE ip=?`, formatTime(time.Now()), reason, ip)
	return err
}

func (s *Store) SaveMeasurement(ctx context.Context, ip string, bitsPerSecond int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE nodes SET measured_bps=?, measured_at=?, speed_test_failed=0 WHERE ip=?`, bitsPerSecond, formatTime(time.Now()), ip)
	return err
}

func (s *Store) SaveMeasurementFailure(ctx context.Context, ip string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE nodes SET measured_bps=0, measured_at=?, speed_test_failed=1 WHERE ip=?`, formatTime(time.Now()), ip)
	return err
}

func (s *Store) LastRefresh(ctx context.Context) time.Time {
	var value string
	if err := s.db.QueryRowContext(ctx, `SELECT value FROM metadata WHERE key='last_refresh'`).Scan(&value); err != nil {
		return time.Time{}
	}
	return parseTime(value)
}

func (s *Store) Metadata(ctx context.Context, key string) (string, error) {
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM metadata WHERE key=?`, key).Scan(&value)
	return value, err
}

func (s *Store) SetMetadata(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO metadata(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}

func orderBy(mode string) string {
	switch mode {
	case "ping":
		return "CASE WHEN ping_ms <= 0 THEN 1 ELSE 0 END, ping_ms ASC, score DESC"
	case "score":
		return "score DESC, speed_bps DESC"
	default:
		return "speed_bps DESC, score DESC"
	}
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func parseTime(value string) time.Time {
	parsed, _ := time.Parse(time.RFC3339Nano, value)
	return parsed
}

// UpsertNodes merges a harvest into the upstream nodes table without dropping
// measurement or failure history. Rows absent from the current harvest are
// removed, except for an actively selected IP.
func (s *Store) UpsertNodes(ctx context.Context, nodes []model.Node, refreshedAt time.Time, preserveIP string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	statement, err := tx.PrepareContext(ctx, `INSERT INTO nodes
	(ip,host_name,port,score,ping_ms,speed_bps,country_long,country_short,sessions,uptime_ms,total_users,total_traffic,log_type,operator,message,openvpn_config,protocols,measured_bps,measured_at,speed_test_failed,last_failure,failure_reason,refreshed_at)
	VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,0,'',0,'','',?)
	ON CONFLICT(ip) DO UPDATE SET
	  host_name=excluded.host_name, port=excluded.port, score=excluded.score, ping_ms=excluded.ping_ms,
	  speed_bps=excluded.speed_bps, country_long=excluded.country_long, country_short=excluded.country_short,
	  sessions=excluded.sessions, uptime_ms=excluded.uptime_ms, total_users=excluded.total_users,
	  total_traffic=excluded.total_traffic, log_type=excluded.log_type, operator=excluded.operator,
	  message=excluded.message, openvpn_config=excluded.openvpn_config, protocols=excluded.protocols,
	  refreshed_at=excluded.refreshed_at`)
	if err != nil {
		return err
	}
	for _, node := range nodes {
		protocols, _ := json.Marshal(node.Protocols)
		if _, err := statement.ExecContext(ctx, node.IP, node.HostName, node.Port, node.Score, node.PingMS, node.SpeedBPS,
			node.CountryLong, node.CountryShort, node.Sessions, node.UptimeMS, node.TotalUsers, node.TotalTraffic,
			node.LogType, node.Operator, node.Message, node.OpenVPNConfig, string(protocols), formatTime(refreshedAt)); err != nil {
			_ = statement.Close()
			return err
		}
	}
	if err := statement.Close(); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM nodes WHERE refreshed_at<>? AND ip<>?`, formatTime(refreshedAt), preserveIP); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO metadata(key,value) VALUES('last_refresh',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, formatTime(refreshedAt)); err != nil {
		return err
	}
	return tx.Commit()
}

// UpsertPoolNodes stores one candidate row per node and protocol. The pool is
// keyed by the endpoint identity so a node keeps its failure counters and
// first-seen timestamp across harvests.
func (s *Store) UpsertPoolNodes(ctx context.Context, nodes []model.Node, source string, seenAt time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	statement, err := tx.PrepareContext(ctx, `INSERT INTO matrix_nodes
	(ip,port,protocol,host_name,score,ping_ms,speed_bps,country_long,country_short,sessions,uptime_ms,total_users,
	 total_traffic,log_type,operator,message,openvpn_config,protocols,asn,asn_org,residential,fail_count,source,
	 first_seen,last_seen,last_failure,failure_reason)
	VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,0,?,?,?,'','')
	ON CONFLICT(ip,port,protocol) DO UPDATE SET
	  host_name=excluded.host_name, score=excluded.score, ping_ms=excluded.ping_ms, speed_bps=excluded.speed_bps,
	  country_long=excluded.country_long, country_short=excluded.country_short, sessions=excluded.sessions,
	  uptime_ms=excluded.uptime_ms, total_users=excluded.total_users, total_traffic=excluded.total_traffic,
	  log_type=excluded.log_type, operator=excluded.operator, message=excluded.message,
	  openvpn_config=excluded.openvpn_config, protocols=excluded.protocols, asn=excluded.asn,
	  asn_org=excluded.asn_org, residential=excluded.residential, source=excluded.source,
	  last_seen=excluded.last_seen`)
	if err != nil {
		return err
	}
	for _, node := range nodes {
		protocols, _ := json.Marshal(node.Protocols)
		for _, protocol := range node.Protocols {
			residential := 0
			if node.Residential {
				residential = 1
			}
			if _, err := statement.ExecContext(ctx, node.IP, node.Port, protocol, node.HostName, node.Score,
				node.PingMS, node.SpeedBPS, node.CountryLong, node.CountryShort, node.Sessions, node.UptimeMS,
				node.TotalUsers, node.TotalTraffic, node.LogType, node.Operator, node.Message, node.OpenVPNConfig,
				string(protocols), node.ASN, node.ASNOrg, residential, source, formatTime(seenAt), formatTime(seenAt)); err != nil {
				_ = statement.Close()
				return err
			}
		}
	}
	if err := statement.Close(); err != nil {
		return err
	}
	return tx.Commit()
}

type PoolFilter struct {
	Country            string
	Protocol           string
	ASN                string
	ResidentialOnly    bool
	PreferredCountries []string
	PreferredASNs      []string
	Exclude            []string
	Limit              int
}

func (s *Store) PoolCandidates(ctx context.Context, filter PoolFilter) ([]model.Node, error) {
	where := []string{"1=1"}
	args := make([]any, 0, len(filter.Exclude)+4)
	if filter.Country != "" {
		where = append(where, "upper(country_short)=upper(?)")
		args = append(args, filter.Country)
	}
	if filter.Protocol != "" {
		where = append(where, "protocol=?")
		args = append(args, filter.Protocol)
	}
	if filter.ASN != "" {
		where = append(where, "upper(asn)=upper(?)")
		args = append(args, filter.ASN)
	}
	if filter.ResidentialOnly {
		where = append(where, "residential=1")
	}
	if len(filter.Exclude) > 0 {
		placeholders := make([]string, len(filter.Exclude))
		for index, ip := range filter.Exclude {
			placeholders[index] = "?"
			args = append(args, ip)
		}
		where = append(where, "ip NOT IN ("+strings.Join(placeholders, ",")+")")
	}
	order := make([]string, 0, 6)
	if len(filter.PreferredCountries) > 0 {
		placeholders := make([]string, len(filter.PreferredCountries))
		for index, country := range filter.PreferredCountries {
			placeholders[index] = "?"
			args = append(args, country)
		}
		order = append(order, "CASE WHEN upper(country_short) IN ("+strings.Join(placeholders, ",")+") THEN 0 ELSE 1 END")
	}
	order = append(order, "residential DESC")
	if len(filter.PreferredASNs) > 0 {
		clauses := make([]string, len(filter.PreferredASNs))
		for index, keyword := range filter.PreferredASNs {
			clauses[index] = "upper(asn_org) LIKE ?"
			args = append(args, "%"+strings.ToUpper(keyword)+"%")
		}
		order = append(order, "CASE WHEN ("+strings.Join(clauses, " OR ")+") THEN 0 ELSE 1 END")
	}
	order = append(order, "CASE WHEN cooldown_until <> '' AND cooldown_until > ? THEN 1 ELSE 0 END")
	args = append(args, formatTime(time.Now()))
	order = append(order, "quality_score DESC", "CASE WHEN fail_count >= 3 THEN 1 ELSE 0 END", "fail_count ASC",
		"CASE WHEN speed_bps > 0 THEN 0 ELSE 1 END", "speed_bps DESC", "ping_ms ASC", "last_seen DESC")
	limit := filter.Limit
	if limit <= 0 {
		limit = 20
	}
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, `SELECT `+poolColumns+` FROM matrix_nodes WHERE `+strings.Join(where, " AND ")+` ORDER BY `+strings.Join(order, ", ")+` LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]model.Node, 0, limit)
	for rows.Next() {
		node, err := scanPoolNode(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, node)
	}
	return result, rows.Err()
}

func (s *Store) PoolNode(ctx context.Context, ip, protocol string) (model.Node, bool, error) {
	query := `SELECT ` + poolColumns + ` FROM matrix_nodes WHERE ip=?`
	args := []any{ip}
	if protocol != "" {
		query += ` AND protocol=?`
		args = append(args, protocol)
	}
	query += ` ORDER BY residential DESC, fail_count ASC, speed_bps DESC LIMIT 1`
	node, err := scanPoolNode(s.db.QueryRowContext(ctx, query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return model.Node{}, false, nil
	}
	if err != nil {
		return model.Node{}, false, err
	}
	return node, true, nil
}

const poolColumns = `host_name,ip,port,score,ping_ms,speed_bps,country_long,country_short,sessions,uptime_ms,total_users,total_traffic,log_type,operator,message,openvpn_config,protocols,asn,asn_org,residential,fail_count,quality_score,cooldown_until,success_count,last_success,source,first_seen,last_seen,last_failure,failure_reason`

func scanPoolNode(row scanner) (model.Node, error) {
	var node model.Node
	var protocols, source, firstSeen, lastSeen, lastFailure, cooldownUntil, lastSuccess string
	err := row.Scan(&node.HostName, &node.IP, &node.Port, &node.Score, &node.PingMS, &node.SpeedBPS, &node.CountryLong,
		&node.CountryShort, &node.Sessions, &node.UptimeMS, &node.TotalUsers, &node.TotalTraffic, &node.LogType,
		&node.Operator, &node.Message, &node.OpenVPNConfig, &protocols, &node.ASN, &node.ASNOrg, &node.Residential,
		&node.FailCount, &node.QualityScore, &cooldownUntil, &node.SuccessCount, &lastSuccess, &source, &firstSeen, &lastSeen, &lastFailure, &node.FailureReason)
	if err != nil {
		return node, err
	}
	_ = json.Unmarshal([]byte(protocols), &node.Protocols)
	node.Source = source
	node.FirstSeen = parseTime(firstSeen)
	node.LastSeen = parseTime(lastSeen)
	node.LastFailure = parseTime(lastFailure)
	node.CooldownUntil = parseTime(cooldownUntil)
	node.LastSuccess = parseTime(lastSuccess)
	node.RefreshedAt = node.LastSeen
	return node, nil
}

func (s *Store) MarkPoolFailure(ctx context.Context, ip, protocol, reason string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE matrix_nodes SET fail_count=fail_count+1,
		quality_score=MAX(0, quality_score-12), last_failure=?, failure_reason=?,
		cooldown_until=strftime('%Y-%m-%dT%H:%M:%SZ','now','+' || (MIN(fail_count+1,12)*60) || ' seconds')
		WHERE ip=? AND protocol=?`, formatTime(time.Now()), reason, ip, protocol)
	return err
}

// MarkPoolSuccess clears the failure counters and cooldown after a slot
// establishes a working session, so a recovered node re-enters rotation.
func (s *Store) MarkPoolSuccess(ctx context.Context, ip, protocol string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE matrix_nodes SET fail_count=0,
		quality_score=MIN(100, quality_score+5), success_count=success_count+1,
		last_success=?, cooldown_until='' WHERE ip=? AND protocol=?`, formatTime(time.Now()), ip, protocol)
	return err
}

func (s *Store) PrunePoolNodes(ctx context.Context, before time.Time) (int64, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM matrix_nodes WHERE last_seen < ?`, formatTime(before))
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

type PoolStats struct {
	Total       int            `json:"total"`
	Residential int            `json:"residential"`
	ByCountry   map[string]int `json:"byCountry"`
	ByProtocol  map[string]int `json:"byProtocol"`
	LastSeen    time.Time      `json:"lastSeen,omitzero"`
}

func (s *Store) PoolStats(ctx context.Context) (PoolStats, error) {
	stats := PoolStats{ByCountry: make(map[string]int), ByProtocol: make(map[string]int)}
	if err := s.db.QueryRowContext(ctx, `SELECT count(*), coalesce(sum(residential),0) FROM matrix_nodes`).Scan(&stats.Total, &stats.Residential); err != nil {
		return stats, err
	}
	var lastSeen string
	if err := s.db.QueryRowContext(ctx, `SELECT coalesce(max(last_seen),'') FROM matrix_nodes`).Scan(&lastSeen); err == nil {
		stats.LastSeen = parseTime(lastSeen)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT country_short, count(*) FROM matrix_nodes GROUP BY country_short ORDER BY count(*) DESC`)
	if err != nil {
		return stats, err
	}
	for rows.Next() {
		var country string
		var count int
		if err := rows.Scan(&country, &count); err != nil {
			rows.Close()
			return stats, err
		}
		if country == "" {
			country = "??"
		}
		stats.ByCountry[country] = count
	}
	rows.Close()
	rows, err = s.db.QueryContext(ctx, `SELECT protocol, count(*) FROM matrix_nodes GROUP BY protocol ORDER BY count(*) DESC`)
	if err != nil {
		return stats, err
	}
	defer rows.Close()
	for rows.Next() {
		var protocol string
		var count int
		if err := rows.Scan(&protocol, &count); err != nil {
			return stats, err
		}
		stats.ByProtocol[protocol] = count
	}
	return stats, rows.Err()
}

func IsNotFound(err error) bool { return errors.Is(err, sql.ErrNoRows) }
