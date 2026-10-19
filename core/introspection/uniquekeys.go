package introspection

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"github.com/aegion-dynamic/graphjin-slim/core/v3/sdata"
)

// uniqueKeyQueryPostgres lists the unique keys of every table: the columns of
// each unique constraint and each unique index.
//
// A unique key is a property of the tuple, so the columns are read in key
// order and a composite key stays a tuple. The primary key is left out: it
// is read from the columns themselves.
//
// A unique index over an expression, or a partial one, is not a key the
// payload can supply, so it is left out too.
const uniqueKeyQueryPostgres = `
SELECT n.nspname AS schema_name,
	c.relname AS table_name,
	ARRAY(
		SELECT a.attname
		FROM unnest(co.conkey) WITH ORDINALITY AS k(attnum, ord)
		JOIN pg_attribute a ON a.attrelid = c.oid AND a.attnum = k.attnum
		ORDER BY k.ord
	) AS columns
FROM pg_constraint co
	JOIN pg_class c ON c.oid = co.conrelid
	JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE co.contype = 'u'
	AND n.nspname NOT IN ('information_schema', 'pg_catalog')
	AND n.nspname NOT LIKE 'pg_toast%'
	AND n.nspname NOT LIKE 'pg_temp_%'
UNION ALL
SELECT n.nspname AS schema_name,
	c.relname AS table_name,
	ARRAY(
		SELECT a.attname
		FROM unnest(ix.indkey) WITH ORDINALITY AS k(attnum, ord)
		JOIN pg_attribute a ON a.attrelid = c.oid AND a.attnum = k.attnum
		ORDER BY k.ord
	) AS columns
FROM pg_index ix
	JOIN pg_class c ON c.oid = ix.indrelid
	JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE ix.indisunique
	AND NOT ix.indisprimary
	AND ix.indexprs IS NULL
	AND ix.indpred IS NULL
	AND n.nspname NOT IN ('information_schema', 'pg_catalog')
	AND n.nspname NOT LIKE 'pg_toast%'
	AND n.nspname NOT LIKE 'pg_temp_%'`

// DiscoverUniqueKeys returns the unique keys of every table: the columns of
// each unique constraint and each unique index, in key order.
//
// It is a best effort enrichment, like the composite foreign key discovery:
// if the query fails the schema still loads, without the unique keys.
func DiscoverUniqueKeys(ctx context.Context, db *sql.DB, dbtype string) ([]sdata.UniqueKeyInfo, error) {
	if db == nil {
		return nil, fmt.Errorf("db is nil")
	}
	switch dbtype {
	case "postgres", "":
		return discoverUniqueKeysPostgres(ctx, db)
	case "sqlite":
		return discoverUniqueKeysSQLite(ctx, db)
	default:
		return nil, nil
	}
}

func discoverUniqueKeysPostgres(ctx context.Context, db *sql.DB) ([]sdata.UniqueKeyInfo, error) {
	qctx, cancel := context.WithTimeout(ctx, introspectionQueryTimeout)
	defer cancel()

	rows, err := db.QueryContext(qctx, uniqueKeyQueryPostgres)
	if err != nil {
		return nil, fmt.Errorf("error fetching unique keys: %w", err)
	}
	defer rows.Close()

	var result []sdata.UniqueKeyInfo
	for rows.Next() {
		var info sdata.UniqueKeyInfo
		var columns []string
		if err := rows.Scan(&info.Schema, &info.Table, (*pgStringArray)(&columns)); err != nil {
			return nil, fmt.Errorf("error scanning unique key: %w", err)
		}
		if len(columns) == 0 {
			continue
		}
		info.Columns = columns
		result = append(result, info)
	}
	return result, rows.Err()
}

// discoverUniqueKeysSQLite reads the unique indexes of every table. SQLite
// reports them through the index_list and index_info pragmas, one table at a
// time.
func discoverUniqueKeysSQLite(ctx context.Context, db *sql.DB) ([]sdata.UniqueKeyInfo, error) {
	qctx, cancel := context.WithTimeout(ctx, introspectionQueryTimeout)
	defer cancel()

	tables, err := db.QueryContext(qctx, `SELECT name FROM sqlite_master
		WHERE type = 'table' AND name NOT LIKE 'sqlite_%' AND name NOT LIKE '_gj_%'`)
	if err != nil {
		return nil, fmt.Errorf("error fetching sqlite tables: %w", err)
	}
	defer tables.Close()

	var names []string
	for tables.Next() {
		var name string
		if err := tables.Scan(&name); err != nil {
			return nil, fmt.Errorf("error scanning sqlite table: %w", err)
		}
		names = append(names, name)
	}
	if err := tables.Err(); err != nil {
		return nil, err
	}

	var result []sdata.UniqueKeyInfo
	for _, name := range names {
		keys, err := sqliteUniqueKeys(qctx, db, name)
		if err != nil {
			// One unreadable table must not lose the keys of the others.
			continue
		}
		for _, cols := range keys {
			result = append(result, sdata.UniqueKeyInfo{
				Schema:  "public",
				Table:   name,
				Columns: cols,
			})
		}
	}
	return result, nil
}

// sqliteUniqueKeys returns the unique indexes of one table, each one the
// columns it covers in index order.
func sqliteUniqueKeys(ctx context.Context, db *sql.DB, table string) ([][]string, error) {
	rows, err := db.QueryContext(ctx, fmt.Sprintf("PRAGMA index_list(%q)", table))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type index struct {
		name   string
		unique bool
	}
	var indexes []index
	for rows.Next() {
		var seq, partial int
		var name, origin string
		var unique int
		if err := rows.Scan(&seq, &name, &unique, &origin, &partial); err != nil {
			return nil, err
		}
		// origin "pk" is the primary key, and a partial index is not a key
		// the payload can supply.
		if origin == "pk" || partial != 0 {
			continue
		}
		indexes = append(indexes, index{name: name, unique: unique != 0})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var keys [][]string
	for _, idx := range indexes {
		if !idx.unique {
			continue
		}
		cols, err := sqliteIndexColumns(ctx, db, idx.name)
		if err != nil || len(cols) == 0 {
			continue
		}
		keys = append(keys, cols)
	}
	return keys, nil
}

func sqliteIndexColumns(ctx context.Context, db *sql.DB, index string) ([]string, error) {
	rows, err := db.QueryContext(ctx, fmt.Sprintf("PRAGMA index_info(%q)", index))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var cols []string
	for rows.Next() {
		var seqno, cid int
		var name *string
		if err := rows.Scan(&seqno, &cid, &name); err != nil {
			return nil, err
		}
		if name == nil {
			// An expression index has no column to name.
			continue
		}
		cols = append(cols, *name)
	}
	return cols, rows.Err()
}

// dedupeUniqueKeys drops the keys that are listed twice, which happens when a
// unique constraint also has a unique index behind it, and puts the rest in a
// stable order so the schema snapshot does not change from one run to the
// next.
func dedupeUniqueKeys(keys []sdata.UniqueKeyInfo) []sdata.UniqueKeyInfo {
	seen := make(map[string]struct{}, len(keys))
	out := keys[:0]
	for _, k := range keys {
		id := k.Schema + ":" + k.Table + ":" + strings.Join(k.Columns, ",")
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Schema != out[j].Schema {
			return out[i].Schema < out[j].Schema
		}
		if out[i].Table != out[j].Table {
			return out[i].Table < out[j].Table
		}
		return strings.Join(out[i].Columns, ",") < strings.Join(out[j].Columns, ",")
	})
	return out
}
