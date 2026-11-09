package store

import (
	"context"
	"time"
)

// ReplayRelationMaintenance describes estimates and settings without packet data.
// Null timestamps and zero counters may reflect reset/uncollected statistics.
type ReplayRelationMaintenance struct {
	Relation         string     `json:"relation"`
	TableBytes       int64      `json:"table_bytes"`
	IndexBytes       int64      `json:"index_bytes"`
	LiveRowsEstimate int64      `json:"live_rows_estimate"`
	DeadRowsEstimate int64      `json:"dead_rows_estimate"`
	LastVacuum       *time.Time `json:"last_vacuum"`
	LastAutovacuum   *time.Time `json:"last_autovacuum"`
	LastAnalyze      *time.Time `json:"last_analyze"`
	LastAutoanalyze  *time.Time `json:"last_autoanalyze"`
	VacuumCount      int64      `json:"vacuum_count"`
	AutovacuumCount  int64      `json:"autovacuum_count"`
	Options          []string   `json:"options"`
}

// ReplayMaintenance reports table/TOAST overrides alongside global defaults.
// Empty options mean inheritance, not disabled maintenance. Long transactions
// and their xmin can delay reclamation; activity visibility depends on the role.
type ReplayMaintenance struct {
	Relations                []ReplayRelationMaintenance `json:"relations"`
	Settings                 map[string]string           `json:"settings"`
	TransactionsWithXmin     int64                       `json:"transactions_with_xmin"`
	OldestTransactionSeconds float64                     `json:"oldest_transaction_seconds"`
	PreparedTransactions     int64                       `json:"prepared_transactions"`
}

func (s *Store) replayMaintenance(ctx context.Context) (ReplayMaintenance, error) {
	out := ReplayMaintenance{Settings: map[string]string{}}
	rows, err := s.reader().Query(ctx, `
 SELECT c.oid::regclass::text,pg_table_size(c.oid)-CASE WHEN c.reltoastrelid=0 THEN 0 ELSE pg_total_relation_size(c.reltoastrelid) END,pg_indexes_size(c.oid),
 coalesce(st.n_live_tup,0),coalesce(st.n_dead_tup,0),st.last_vacuum,st.last_autovacuum,
 st.last_analyze,st.last_autoanalyze,coalesce(st.vacuum_count,0),coalesce(st.autovacuum_count,0),coalesce(c.reloptions,'{}'::text[])
 FROM pg_class c LEFT JOIN pg_stat_all_tables st ON st.relid=c.oid
 WHERE c.oid='public.score_snapshots'::regclass OR c.oid=(SELECT reltoastrelid FROM pg_class WHERE oid='public.score_snapshots'::regclass)
 ORDER BY c.oid`)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var r ReplayRelationMaintenance
		if err := rows.Scan(&r.Relation, &r.TableBytes, &r.IndexBytes, &r.LiveRowsEstimate, &r.DeadRowsEstimate, &r.LastVacuum, &r.LastAutovacuum, &r.LastAnalyze, &r.LastAutoanalyze, &r.VacuumCount, &r.AutovacuumCount, &r.Options); err != nil {
			rows.Close()
			return out, err
		}
		out.Relations = append(out.Relations, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	rows, err = s.reader().Query(ctx, `SELECT name,current_setting(name) FROM pg_settings
 WHERE name IN ('autovacuum','track_counts','autovacuum_naptime','autovacuum_max_workers',
 'autovacuum_vacuum_threshold','autovacuum_vacuum_scale_factor','autovacuum_vacuum_insert_threshold','autovacuum_vacuum_insert_scale_factor',
 'autovacuum_analyze_threshold','autovacuum_analyze_scale_factor','autovacuum_vacuum_cost_limit','autovacuum_vacuum_cost_delay','autovacuum_work_mem','maintenance_work_mem')`)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			rows.Close()
			return out, err
		}
		out.Settings[k] = v
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	err = s.reader().QueryRow(ctx, `SELECT count(*) FILTER(WHERE backend_xmin IS NOT NULL),
 coalesce(max(extract(epoch FROM(clock_timestamp()-xact_start))),0),
 (SELECT count(*) FROM pg_prepared_xacts WHERE database=current_database())
 FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid()`).Scan(&out.TransactionsWithXmin, &out.OldestTransactionSeconds, &out.PreparedTransactions)
	return out, err
}
