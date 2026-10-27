package web

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"sort"
)

// tileSize is a tile's saved expanded size; zero fields mean "built-in
// default".
type tileSize struct {
	Width, Height int
	Expanded      *bool // saved open/closed state, nil = defaultExpandedKind
}

// fetchTileLayout returns DefaultTileKinds in userID's saved order (kinds
// never moved keep their DefaultTileKinds index as their position) plus
// each kind's saved expanded size, from dashboard_tile_layout. Sections
// are fixed by kind, so one flat order is enough: the dashboard groups
// tiles by category at render time and only their relative order within a
// section is ever visible.
func fetchTileLayout(ctx context.Context, db *sql.DB, userID int64) (order []string, sizes map[string]tileSize, err error) {
	pos := map[string]int{}
	for i, k := range DefaultTileKinds {
		pos[k] = i
	}
	sizes = map[string]tileSize{}

	rows, err := db.QueryContext(ctx, `SELECT kind, position, width, height, expanded FROM dashboard_tile_layout WHERE user_id = ?`, userID)
	if err != nil {
		return nil, nil, fmt.Errorf("querying dashboard_tile_layout: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var kind string
		var p, w, h sql.NullInt64
		var e sql.NullBool
		if err := rows.Scan(&kind, &p, &w, &h, &e); err != nil {
			return nil, nil, fmt.Errorf("scanning dashboard_tile_layout row: %w", err)
		}
		if _, known := pos[kind]; !known {
			continue
		}
		if p.Valid {
			pos[kind] = int(p.Int64)
		}
		size := tileSize{Width: int(w.Int64), Height: int(h.Int64)}
		if e.Valid {
			size.Expanded = &e.Bool
		}
		sizes[kind] = size
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	order = slices.Clone(DefaultTileKinds)
	sort.SliceStable(order, func(i, j int) bool { return pos[order[i]] < pos[order[j]] })
	return order, sizes, nil
}

// moveTile moves kind into target's slot (after target when moving forward,
// before it when moving back — the usual drag-and-drop "take its place")
// and saves every kind's resulting position. The browser only ever offers
// a target in kind's own section, so this doesn't re-check that.
func moveTile(ctx context.Context, db *sql.DB, userID int64, kind, target string) error {
	order, _, err := fetchTileLayout(ctx, db, userID)
	if err != nil {
		return err
	}
	from, to := slices.Index(order, kind), slices.Index(order, target)
	if from < 0 || to < 0 {
		return fmt.Errorf("unknown tile kind %q or %q", kind, target)
	}
	order = slices.Insert(slices.Delete(order, from, from+1), to, kind)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()
	for i, k := range order {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO dashboard_tile_layout (user_id, kind, position) VALUES (?, ?, ?)
			ON CONFLICT (user_id, kind) DO UPDATE SET position = excluded.position
		`, userID, k, i); err != nil {
			return fmt.Errorf("saving position of %s: %w", k, err)
		}
	}
	return tx.Commit()
}

// saveTileSize saves kind's expanded size: width in twelfths of the row
// (2-12, 12 = full row), height pixels (120-1200, 0 = built-in default).
func saveTileSize(ctx context.Context, db *sql.DB, userID int64, kind string, size tileSize) error {
	if !slices.Contains(DefaultTileKinds, kind) || size.Width < 2 || size.Width > 12 ||
		size.Height != 0 && (size.Height < 120 || size.Height > 1200) {
		return fmt.Errorf("invalid tile %q or size %+v", kind, size)
	}
	height := sql.NullInt64{Int64: int64(size.Height), Valid: size.Height != 0}
	_, err := db.ExecContext(ctx, `
		INSERT INTO dashboard_tile_layout (user_id, kind, width, height) VALUES (?, ?, ?, ?)
		ON CONFLICT (user_id, kind) DO UPDATE SET width = excluded.width, height = excluded.height
	`, userID, kind, size.Width, height)
	if err != nil {
		return fmt.Errorf("saving size of %s: %w", kind, err)
	}
	return nil
}

// saveTileExpanded saves kind's open/closed state, so the next page load
// opens it the same way.
func saveTileExpanded(ctx context.Context, db *sql.DB, userID int64, kind string, expanded bool) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO dashboard_tile_layout (user_id, kind, expanded) VALUES (?, ?, ?)
		ON CONFLICT (user_id, kind) DO UPDATE SET expanded = excluded.expanded
	`, userID, kind, expanded)
	if err != nil {
		return fmt.Errorf("saving expanded state of %s: %w", kind, err)
	}
	return nil
}
