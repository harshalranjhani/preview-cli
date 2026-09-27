package server

import "testing"

func TestStripManagedImport(t *testing.T) {
	in := "example.com {\n\trespond \"ok\"\n}\n\n# preview-managed-import\nimport /etc/caddy/preview.caddy\n"
	got := StripManagedImport(in, "/etc/caddy/preview.caddy")
	if got != "example.com {\n\trespond \"ok\"\n}\n\n" {
		t.Fatalf("%q", got)
	}
}
