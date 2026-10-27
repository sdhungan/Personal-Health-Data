package googlehealth

import (
	"slices"
	"testing"
)

// TestParseTcxRoute checks trackpoints come back in order across laps and
// a trackpoint without a Position (GPS dropout) is skipped. Shape follows
// the TCX example in developers.google.com/health/data-types/workouts.
func TestParseTcxRoute(t *testing.T) {
	tcx := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<TrainingCenterDatabase xmlns="http://www.garmin.com/xmlschemas/TrainingCenterDatabase/v2">
  <Activities>
    <Activity Sport="Running">
      <Lap StartTime="2026-10-27T10:00:00Z">
        <Track>
          <Trackpoint><Time>2026-10-27T10:00:00Z</Time><Position><LatitudeDegrees>37.7749</LatitudeDegrees><LongitudeDegrees>-122.4194</LongitudeDegrees></Position></Trackpoint>
          <Trackpoint><Time>2026-10-27T10:00:05Z</Time><DistanceMeters>3.0</DistanceMeters></Trackpoint>
        </Track>
      </Lap>
      <Lap StartTime="2026-10-27T10:10:00Z">
        <Track>
          <Trackpoint><Time>2026-10-27T10:10:00Z</Time><Position><LatitudeDegrees>37.7750</LatitudeDegrees><LongitudeDegrees>-122.4190</LongitudeDegrees></Position></Trackpoint>
        </Track>
      </Lap>
    </Activity>
  </Activities>
</TrainingCenterDatabase>`)
	route, err := parseTcxRoute(tcx)
	if err != nil {
		t.Fatal(err)
	}
	want := []LatLng{{37.7749, -122.4194}, {37.7750, -122.4190}}
	if !slices.Equal(route, want) {
		t.Errorf("route = %v, want %v", route, want)
	}
}
