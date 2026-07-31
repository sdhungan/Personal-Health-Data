package db

import (
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Dump writes a plaintext SQL rendering of conn's schema and data to w: every
// CREATE statement in creation order, followed by INSERT statements for each
// table's rows. This is the deliberate, explicit escape hatch behind
// "healthd db decrypt" (see ARCHITECTURE.md §6) — it is not used on any
// normal read/write path.
func Dump(conn *sql.DB, w io.Writer) error {
	type object struct{ typ, name, sql string }

	rows, err := conn.Query(`SELECT type, name, sql FROM sqlite_master WHERE sql IS NOT NULL AND name NOT LIKE 'sqlite_%' ORDER BY rowid`)
	if err != nil {
		return fmt.Errorf("listing schema objects: %w", err)
	}
	var objects []object
	for rows.Next() {
		var o object
		if err := rows.Scan(&o.typ, &o.name, &o.sql); err != nil {
			rows.Close()
			return fmt.Errorf("reading schema objects: %w", err)
		}
		objects = append(objects, o)
	}
	rowsErr := rows.Err()
	rows.Close()
	if rowsErr != nil {
		return fmt.Errorf("reading schema objects: %w", rowsErr)
	}

	if _, err := fmt.Fprintln(w, "PRAGMA foreign_keys = OFF;"); err != nil {
		return err
	}
	for _, o := range objects {
		if _, err := fmt.Fprintf(w, "%s;\n", o.sql); err != nil {
			return err
		}
	}
	for _, o := range objects {
		if o.typ != "table" {
			continue
		}
		if err := dumpTableRows(conn, w, o.name); err != nil {
			return fmt.Errorf("dumping table %s: %w", o.name, err)
		}
	}
	if _, err := fmt.Fprintln(w, "PRAGMA foreign_keys = ON;"); err != nil {
		return err
	}
	return nil
}

func dumpTableRows(conn *sql.DB, w io.Writer, table string) error {
	rows, err := conn.Query(fmt.Sprintf(`SELECT * FROM "%s"`, table))
	if err != nil {
		return err
	}
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		return err
	}
	quotedCols := make([]string, len(columns))
	for i, c := range columns {
		quotedCols[i] = `"` + c + `"`
	}

	values := make([]any, len(columns))
	ptrs := make([]any, len(columns))
	for i := range values {
		ptrs[i] = &values[i]
	}

	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return err
		}
		literals := make([]string, len(values))
		for i, v := range values {
			literals[i] = sqlLiteral(v)
		}
		_, err := fmt.Fprintf(w, "INSERT INTO \"%s\" (%s) VALUES (%s);\n",
			table, strings.Join(quotedCols, ", "), strings.Join(literals, ", "))
		if err != nil {
			return err
		}
	}
	return rows.Err()
}

func sqlLiteral(v any) string {
	switch t := v.(type) {
	case nil:
		return "NULL"
	case int64:
		return strconv.FormatInt(t, 10)
	case float64:
		return strconv.FormatFloat(t, 'g', -1, 64)
	case bool:
		if t {
			return "1"
		}
		return "0"
	case string:
		return "'" + strings.ReplaceAll(t, "'", "''") + "'"
	case []byte:
		return "X'" + hex.EncodeToString(t) + "'"
	default:
		return "'" + strings.ReplaceAll(fmt.Sprintf("%v", t), "'", "''") + "'"
	}
}
