package adsb

import (
	"encoding/json"
	"testing"
)

func TestJSONAircraft_Unmarshal(t *testing.T) { //nolint:cognitive-complexity
	tests := []struct {
		name    string
		json    string
		wantErr bool
	}{
		{
			name:    "alt_baro as string ground",
			json:    `{"hex":"406a38", "alt_baro":"ground"}`,
			wantErr: false,
		},
		{
			name:    "alt_baro as number",
			json:    `{"hex":"406a38", "alt_baro":34000}`,
			wantErr: false,
		},
		{
			name:    "alt_baro as float",
			json:    `{"hex":"406a38", "alt_baro":34000.5}`,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var aircraft JSONAircraft

			err := json.Unmarshal([]byte(tt.json), &aircraft)
			if (err != nil) != tt.wantErr {
				t.Errorf("json.Unmarshal() error = %v, wantErr %v", err, tt.wantErr)
			}

			if !tt.wantErr {
				if tt.name == "alt_baro as string ground" && aircraft.BarometricAltitude != 0 {
					t.Errorf("expected 0 for ground, got %v", aircraft.BarometricAltitude)
				}

				if tt.name == "alt_baro as number" && aircraft.BarometricAltitude != 34000 {
					t.Errorf("expected 34000, got %v", aircraft.BarometricAltitude)
				}

				if tt.name == "alt_baro as float" && aircraft.BarometricAltitude != 34000.5 {
					t.Errorf("expected 34000.5, got %v", aircraft.BarometricAltitude)
				}
			}
		})
	}
}
