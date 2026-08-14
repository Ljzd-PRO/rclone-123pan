package pan123

import (
	"context"
	"testing"
)

func TestFeatureGolden(t *testing.T) {
	features := buildFeatures(context.Background(), &Fs{}).Enabled()
	wantEnabled := map[string]bool{
		"About":                   true,
		"CanHaveEmptyDirectories": true,
		"Command":                 true,
		"DirCacheFlush":           true,
		"DirMove":                 true,
		"Disconnect":              true,
		"Move":                    true,
		"PartialUploads":          true,
		"PutStream":               true,
		"UserInfo":                true,
	}
	for name, enabled := range features {
		if enabled != wantEnabled[name] {
			t.Errorf("feature %s enabled=%t, want %t", name, enabled, wantEnabled[name])
		}
	}
	for name := range wantEnabled {
		if _, found := features[name]; !found {
			t.Errorf("feature golden contains unknown feature %s", name)
		}
	}
}
