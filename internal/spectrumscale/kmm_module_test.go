package spectrumscale

import (
	"testing"
)

func TestReadModuleStatusModuleLoader(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		obj      map[string]interface{}
		field    string
		want     int64
		reported bool
		wantErr  bool
	}{
		{
			name:     "missing status",
			obj:      map[string]interface{}{},
			field:    moduleLoaderFieldNodesMatchingSelectorNumber,
			reported: false,
		},
		{
			name: "empty moduleLoader map",
			obj: map[string]interface{}{
				"status": map[string]interface{}{
					"moduleLoader": map[string]interface{}{},
				},
			},
			field:    moduleLoaderFieldDesiredNumber,
			want:     0,
			reported: true,
		},
		{
			name: "reads nodesMatchingSelectorNumber",
			obj: map[string]interface{}{
				"status": map[string]interface{}{
					"moduleLoader": map[string]interface{}{
						"nodesMatchingSelectorNumber": int64(3),
					},
				},
			},
			field:    moduleLoaderFieldNodesMatchingSelectorNumber,
			want:     3,
			reported: true,
		},
		{
			name: "reads desiredNumber and availableNumber",
			obj: map[string]interface{}{
				"status": map[string]interface{}{
					"moduleLoader": map[string]interface{}{
						"desiredNumber":   int32(2),
						"availableNumber": float64(2),
					},
				},
			},
			field:    moduleLoaderFieldAvailableNumber,
			want:     2,
			reported: true,
		},
		{
			name: "unexpected field type",
			obj: map[string]interface{}{
				"status": map[string]interface{}{
					"moduleLoader": map[string]interface{}{
						"desiredNumber": "bad",
					},
				},
			},
			field:    moduleLoaderFieldDesiredNumber,
			wantErr:  true,
			reported: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, reported, err := readModuleStatusModuleLoader(tt.obj, tt.field)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("readModuleStatusModuleLoader: %v", err)
			}
			if reported != tt.reported {
				t.Fatalf("reported = %v, want %v", reported, tt.reported)
			}
			if got != tt.want {
				t.Fatalf("value = %d, want %d", got, tt.want)
			}
		})
	}
}
