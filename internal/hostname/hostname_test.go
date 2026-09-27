package hostname

import "testing"

func TestSlug(t *testing.T) {
	cases := map[string]string{
		"My Cool_App":  "my-cool-app",
		"  Dashboard ": "dashboard",
		"---":          "",
		"API v2":       "api-v2",
		"already-ok":   "already-ok",
	}
	for in, want := range cases {
		if got := Slug(in); got != want {
			t.Fatalf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBuild(t *testing.T) {
	tmpl := "{{project}}--{{name}}--{{id}}.{{base_domain}}"
	got, err := Build(tmpl, "My Cool_App", "Auth Fix", "k7p2", "preview.example.com")
	if err != nil {
		t.Fatal(err)
	}
	want := "my-cool-app--auth-fix--k7p2.preview.example.com"
	if got != want {
		t.Fatalf("got %s", got)
	}
	unnamed, err := Build(tmpl, "dashboard", "", "k7p2", "preview.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if unnamed != "dashboard--k7p2.preview.example.com" {
		t.Fatalf("unnamed host = %s", unnamed)
	}
	projectHost, err := Build("{{name}}--{{id}}.{{project}}.{{base_domain}}", "dashboard", "checkout", "p3qd", "preview.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if projectHost != "checkout--p3qd.dashboard.preview.example.com" {
		t.Fatalf("project host = %s", projectHost)
	}
}

func TestBuildFitsDNSLabel(t *testing.T) {
	long := "this-is-a-very-long-project-name-that-cannot-fit-inside-a-single-dns-label-without-help"
	got, err := Build("{{project}}--{{name}}--{{id}}.{{base_domain}}", long, long, "ab12", "preview.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := Validate(got); err != nil {
		t.Fatal(err)
	}
}

func TestBuildRequiresID(t *testing.T) {
	_, err := Build("{{project}}.{{base_domain}}", "app", "name", "ab12", "preview.example.com")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestIDs(t *testing.T) {
	short, err := ShortID()
	if err != nil {
		t.Fatal(err)
	}
	if len(short) != 4 {
		t.Fatalf("id length %d", len(short))
	}
	full := PreviewID(short)
	if full != "pv_"+short {
		t.Fatal(full)
	}
	if RouteID(full) != "preview-pv-"+short {
		t.Fatal(RouteID(full))
	}
}
