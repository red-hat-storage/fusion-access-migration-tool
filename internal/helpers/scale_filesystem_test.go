package helpers

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestScaleFilesystemReportsHealthy(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		obj    *unstructured.Unstructured
		wantOK bool
	}{
		{
			name: "legacy mounted true",
			obj: &unstructured.Unstructured{Object: map[string]interface{}{
				"status": map[string]interface{}{"mounted": true, "phase": "Ready"},
			}},
			wantOK: true,
		},
		{
			name: "legacy mounted false",
			obj: &unstructured.Unstructured{Object: map[string]interface{}{
				"status": map[string]interface{}{"mounted": false, "phase": "Ready"},
			}},
			wantOK: false,
		},
		{
			name: "fusion conditions healthy true",
			obj: &unstructured.Unstructured{Object: map[string]interface{}{
				"status": map[string]interface{}{
					"conditions": []interface{}{
						map[string]interface{}{"type": "Success", "status": "True"},
						map[string]interface{}{"type": "Healthy", "status": "True"},
					},
				},
			}},
			wantOK: true,
		},
		{
			name: "healthy false wins over missing mounted",
			obj: &unstructured.Unstructured{Object: map[string]interface{}{
				"status": map[string]interface{}{
					"conditions": []interface{}{
						map[string]interface{}{"type": "Success", "status": "True"},
						map[string]interface{}{"type": "Healthy", "status": "False"},
					},
				},
			}},
			wantOK: false,
		},
		{
			name: "phase ready without mounted field",
			obj: &unstructured.Unstructured{Object: map[string]interface{}{
				"status": map[string]interface{}{"phase": "Ready"},
			}},
			wantOK: true,
		},
		{
			name: "recovering phase",
			obj: &unstructured.Unstructured{Object: map[string]interface{}{
				"status": map[string]interface{}{"phase": "Recovering"},
			}},
			wantOK: false,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ok, detail := ScaleFilesystemReportsHealthy(tc.obj)
			if ok != tc.wantOK {
				t.Fatalf("ScaleFilesystemReportsHealthy() ok=%v detail=%q want ok=%v", ok, detail, tc.wantOK)
			}
		})
	}
}
