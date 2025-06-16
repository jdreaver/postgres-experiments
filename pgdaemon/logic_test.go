package main

import (
	"reflect"
	"testing"
)

// TestClusterStatusFieldCount ensures all fields in ClusterStatus are
// accounted for in clusterStatusChanged. This test will fail if you add
// fields but forget to update the comparison function.
func TestClusterStatusFieldCount(t *testing.T) {
	v := reflect.ValueOf(ClusterStatus{})
	expectedFields := 7

	if v.NumField() != expectedFields {
		t.Errorf("ClusterStatus has %d fields, expected %d. You likely added a field but forgot to:",
			v.NumField(), expectedFields)
		t.Errorf("1. Update clusterStatusChanged() to compare the new field")
		t.Errorf("2. Update this test's expectedFields count to %d", v.NumField())

		// Show which fields exist
		t.Errorf("Current fields:")
		for i := range v.NumField() {
			field := v.Type().Field(i)
			t.Errorf("  - %s %s", field.Name, field.Type)
		}
	}
}

// TestStringSlicesEqual tests our helper function
func TestStringSlicesEqual(t *testing.T) {
	tests := []struct {
		name string
		a, b []string
		want bool
	}{
		{"both nil", nil, nil, true},
		{"both empty", []string{}, []string{}, true},
		{"equal", []string{"a", "b"}, []string{"a", "b"}, true},
		{"different length", []string{"a"}, []string{"a", "b"}, false},
		{"different content", []string{"a", "b"}, []string{"a", "c"}, false},
		{"nil vs empty", nil, []string{}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stringSlicesEqual(tt.a, tt.b); got != tt.want {
				t.Errorf("stringSlicesEqual(%v, %v) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}
