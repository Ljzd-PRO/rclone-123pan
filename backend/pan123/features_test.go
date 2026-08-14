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
	featuresObject := buildFeatures(context.Background(), &Fs{})
	if featuresObject.PartialUploads {
		t.Fatal("PartialUploads must remain false until incomplete-object visibility is proven")
	}
	if featuresObject.SlowHash {
		t.Fatal("ETag-backed MD5 must remain a fast hash")
	}
	if featuresObject.Copy != nil || featuresObject.Purge != nil || featuresObject.ListR != nil {
		t.Fatal("unsupported optional interfaces were advertised")
	}
}
