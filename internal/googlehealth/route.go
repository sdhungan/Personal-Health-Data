package googlehealth

import (
	"context"
	"database/sql"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
)

// LatLng is one GPS fix on an exercise route, as [lat, lng] — the exact
// shape Leaflet's L.polyline takes, so route_json passes straight through.
type LatLng [2]float64

// ExportExerciseTcx calls GET .../dataTypes/exercise/dataPoints/{id}:exportExerciseTcx
// and returns the raw TCX (XML) route file. ?alt=media is required, or the
// API answers with a JSON envelope instead of the file (see
// developers.google.com/health/data-types/workouts). Needs the
// googlehealth.location.readonly scope; without it this is an
// authorization error.
func (c *Client) ExportExerciseTcx(ctx context.Context, dataPointID string) ([]byte, error) {
	u := fmt.Sprintf("%s/users/me/dataTypes/exercise/dataPoints/%s:exportExerciseTcx?alt=media", BaseURL, dataPointID)
	return c.do(ctx, http.MethodGet, u, nil)
}

// parseTcxRoute extracts every trackpoint's position from a TCX file, in
// order. Trackpoints without a Position (e.g. a GPS dropout) are skipped.
// Field names are the Garmin TCX v2 schema's, which the docs' example uses
// — DOCUMENTED, not yet checked against a real export from this account.
func parseTcxRoute(tcx []byte) ([]LatLng, error) {
	var doc struct {
		Trackpoints []struct {
			Position *struct {
				Lat float64 `xml:"LatitudeDegrees"`
				Lng float64 `xml:"LongitudeDegrees"`
			} `xml:"Position"`
		} `xml:"Activities>Activity>Lap>Track>Trackpoint"`
	}
	if err := xml.Unmarshal(tcx, &doc); err != nil {
		return nil, fmt.Errorf("decoding TCX: %w", err)
	}
	var route []LatLng
	for _, tp := range doc.Trackpoints {
		if tp.Position != nil {
			route = append(route, LatLng{tp.Position.Lat, tp.Position.Lng})
		}
	}
	return route, nil
}

// ExerciseRoute returns sessionID's GPS route: from route_json if it was
// already fetched, otherwise fetched from the API once and cached there.
// nil (no error) means the session has no GPS route.
func (s *DBSyncer) ExerciseRoute(ctx context.Context, sessionID int64) ([]LatLng, error) {
	var dataPointID, routeJSON sql.NullString
	var hasGPS bool
	err := s.DB.QueryRowContext(ctx, `
		SELECT data_point_id, has_gps, route_json FROM watch_exercise_session WHERE user_id = ? AND id = ?
	`, s.UserID, sessionID).Scan(&dataPointID, &hasGPS, &routeJSON)
	if err != nil {
		return nil, fmt.Errorf("querying exercise session %d: %w", sessionID, err)
	}
	var route []LatLng
	if routeJSON.Valid {
		if err := json.Unmarshal([]byte(routeJSON.String), &route); err != nil {
			return nil, fmt.Errorf("decoding cached route: %w", err)
		}
		return route, nil
	}
	if !hasGPS || !dataPointID.Valid {
		return nil, nil
	}
	if s.Client == nil {
		return nil, fmt.Errorf("Google Health isn't connected")
	}

	tcx, err := s.Client.ExportExerciseTcx(ctx, dataPointID.String)
	if err != nil {
		return nil, fmt.Errorf("fetching route: %w", err)
	}
	if route, err = parseTcxRoute(tcx); err != nil {
		return nil, err
	}
	if route == nil {
		route = []LatLng{} // cache "fetched, but empty" too, so it isn't re-fetched every click
	}
	encoded, _ := json.Marshal(route)
	if _, err := s.DB.ExecContext(ctx, `UPDATE watch_exercise_session SET route_json = ? WHERE user_id = ? AND id = ?`,
		string(encoded), s.UserID, sessionID); err != nil {
		return nil, fmt.Errorf("caching route: %w", err)
	}
	return route, nil
}
