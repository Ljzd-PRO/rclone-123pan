package _123pan

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProtocolFixturesAreSanitizedJSON(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "..", "protocol", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 4 {
		t.Fatalf("expected 4 protocol fixtures, got %d", len(paths))
	}
	for _, path := range paths {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !json.Valid(body) {
			t.Fatalf("%s is not valid JSON", path)
		}
		lower := strings.ToLower(string(body))
		for _, forbidden := range []string{
			"x-amz-credential=",
			"x-amz-signature=",
			"bearer ",
			"cookie:",
			"authorization:",
			"accesskeyid\": \"<",
			"secretaccesskey\": \"<",
			"sessiontoken\": \"<",
		} {
			if strings.Contains(lower, forbidden) {
				t.Fatalf("%s contains forbidden protocol material %q", path, forbidden)
			}
		}
	}
}
