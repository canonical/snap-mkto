// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

package redact

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/canonical/snap-mkto/internal/marketo"
)

// Ids and names as in testdata/marketo/activity_types.json.
var types = map[int]string{
	1: "Visit Webpage", 2: "Fill Out Form", 3: "Click Link", 7: "Email Delivered",
	9: "Unsubscribe Email", 10: "Open Email", 11: "Click Email", 12: "New Lead",
	13: "Change Data Value", 22: "Change Status in Progression", 46: "Interesting Moment",
}

func lead() marketo.Lead {
	return marketo.Lead{
		"id": json.Number("1001"), "firstName": "Jane", "lastName": "Doe", "email": "jane.doe@example.com",
		"company": "Example Corp", "industry": "Software", "country": "Germany",
		"numberOfEmployees": json.Number("250"), "title": "VP of Platform Engineering",
		"phone": "+44 20 7946 0958", "mobilePhone__c": "+49 30 901820", "city": "Berlin",
		"address": "1 Example Street", "postalCode": "10115", "linkedInURL__c": "https://www.example.com/in/janedoe",
	}
}

func at(day, hour, min int) time.Time { return time.Date(2026, 10, day, hour, min, 17, 0, time.UTC) }

func act(id, typeID int, date time.Time, primary string, attrs map[string]any) marketo.Activity {
	return marketo.Activity{ID: id, Date: date, TypeID: typeID, Primary: primary, Attributes: attrs}
}

func TestBuildFullPayload(t *testing.T) {
	acts := []marketo.Activity{
		act(5002, 2, time.Date(2026, 10, 3, 9, 41, 12, 0, time.FixedZone("CEST", 2*3600)), "Contact Us Form",
			map[string]any{"Client IP Address": "192.0.2.10", "Form Fields": `{"Email":"jane.doe@example.com"}`}),
		act(5001, 1, at(2, 10, 5), "www.example.com/pricing", map[string]any{
			"Client IP Address": "192.0.2.10", "Query Parameters": "utm_source=example",
			"Webpage URL": "/pricing?utm_source=example", "User Agent": "Mozilla/5.0 ExampleBrowser/1.0"}),
		act(5003, 46, at(4, 8, 0), "Jane asked for a call on +1 555 010 0199", nil),
		act(5004, 13, at(5, 8, 0), "Phone", map[string]any{"New Value": "+44 20 7946 0958"}),
	}
	p, err := Build(lead(), acts, types)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"lead":{"company":"Example Corp","industry":"Software","country":"Germany","numberOfEmployees":250,"role":"senior engineering leader"},` +
		`"events":[{"id":"E1","date":"2026-10-02T10:00:00Z","type":"Visit Webpage","url":"https://www.example.com/pricing"},` +
		`{"id":"E2","date":"2026-10-03T07:00:00Z","type":"Fill Out Form","asset":"Contact Us Form"},` +
		`{"id":"E3","date":"2026-10-04T08:00:00Z","type":"Interesting Moment"},` +
		`{"id":"E4","date":"2026-10-05T08:00:00Z","type":"Change Data Value"}]}`
	if got := string(p.JSON()); got != want {
		t.Fatalf("payload\n got %s\nwant %s", got, want)
	}
}

func TestBuild(t *testing.T) {
	for _, tc := range []struct {
		name    string
		act     marketo.Activity
		want    string // a fragment the payload must contain
		wantErr string
	}{
		{name: "email inside a URL path", act: act(1, 3, at(1, 9, 0), "https://www.example.com/u/jane.doe@example.com/prefs", nil),
			want: `"url":"https://www.example.com/u/{redacted}/prefs"`},
		{name: "mkt_tok in a query string", act: act(1, 3, at(1, 9, 0), "https://www.example.com/pricing?mkt_tok=MDAwLUFCQy0xMjMAAAGExampleTok", nil),
			want: `"url":"https://www.example.com/pricing"}`},
		{name: "prefilled form URL with email in the query", act: act(1, 1, at(1, 9, 0), "www.example.com/contact?email=jane.doe%40example.com", nil),
			want: `"url":"https://www.example.com/contact"}`},
		{name: "userinfo URL", act: act(1, 3, at(1, 9, 0), "https://jane:s3cret@www.example.com/a", nil),
			want: `"url":"https://www.example.com/a"}`},
		{name: "token segment", act: act(1, 3, at(1, 9, 0), "https://www.example.com/r/AbCdEfGhIjKlMnOpQrStUvWx/next", nil),
			want: `"url":"https://www.example.com/r/{redacted}/next"}`},
		{name: "schemeless URL with a URL in its query", act: act(1, 1, at(1, 9, 0), "www.example.com/go?next=https://x.example.com", nil),
			want: `"url":"https://www.example.com/go"}`},
		{name: "dated path", act: act(1, 1, at(1, 9, 0), "www.example.com/blog/2026/10/03/1", nil),
			want: `"url":"https://www.example.com/blog/2026/10/03/1"}`},
		{name: "dated path with a numbered slug", act: act(1, 1, at(1, 9, 0), "www.example.com/blog/2026/10/03/12345-title", nil),
			want: `"url":"https://www.example.com/blog/2026/10/03/12345-title"}`},
		{name: "numeric ids", act: act(1, 1, at(1, 9, 0), "www.example.com/p/12345/67890", nil),
			want: `"url":"https://www.example.com/p/12345/67890"}`},
		{name: "event path", act: act(1, 1, at(1, 9, 0), "www.example.com/events/2026/123456", nil),
			want: `"url":"https://www.example.com/events/2026/123456"}`},
		{name: "phone as a URL segment", act: act(1, 1, at(1, 9, 0), "www.example.com/call/+44-20-7946-0958", nil),
			wantErr: "redaction guard: phone number found in events[0].url; nothing was sent"},
		{name: "phone with a slash in an asset", act: act(1, 10, at(1, 9, 0), "Tel 030/12345678", nil),
			wantErr: "redaction guard: phone number found in events[0].asset; nothing was sent"},
		{name: "unparseable URL keeps type and date", act: act(1, 3, at(1, 9, 0), "/relative?x=1", nil),
			want: `{"id":"E1","date":"2026-10-01T09:00:00Z","type":"Click Link"}`},
		{name: "IP in an attribute is dropped", act: act(1, 10, at(1, 9, 0), "November Newsletter", map[string]any{"Client IP Address": "192.0.2.10"}),
			want: `"asset":"November Newsletter"}`},
		{name: "Interesting Moment text dropped", act: act(1, 46, at(1, 9, 0), "Jane Doe, 192.0.2.10, wants a call", nil),
			want: `{"id":"E1","date":"2026-10-01T09:00:00Z","type":"Interesting Moment"}`},
		{name: "other types keep type and date only", act: act(1, 13, at(1, 9, 0), "Phone", map[string]any{"New Value": "+44 20 7946 0958"}),
			want: `{"id":"E1","date":"2026-10-01T09:00:00Z","type":"Change Data Value"}`},
		{name: "asset types match case-insensitively", act: act(1, 22, at(1, 9, 0), "Webinar Q4 > Attended", nil),
			want: `"asset":"Webinar Q4 > Attended"}`},
		{name: "ISO date not flagged as a phone", act: act(1, 7, at(3, 9, 59), "November Newsletter", nil),
			want: `"date":"2026-10-03T09:00:00Z"`},
		{name: "first name inside an email asset name", act: act(1, 10, at(1, 9, 0), "Follow-up for Jane", nil),
			wantErr: "redaction guard: lead's first name found in events[0].asset; nothing was sent"},
		{name: "IP pasted into an asset name", act: act(1, 10, at(1, 9, 0), "Webinar 192.0.2.55 recap", nil),
			wantErr: "redaction guard: IP address found in events[0].asset; nothing was sent"},
	} {
		p, err := Build(lead(), []marketo.Activity{tc.act}, types)
		if tc.wantErr != "" {
			if err == nil || err.Error() != tc.wantErr {
				t.Errorf("%s: error = %v, want %q", tc.name, err, tc.wantErr)
			} else if strings.Contains(err.Error(), tc.act.Primary) || strings.Contains(err.Error(), "192.0.2.55") || strings.Contains(err.Error(), "Jane") {
				t.Errorf("%s: error leaks the value", tc.name)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: unexpected error: %v", tc.name, err)
			continue
		}
		got := string(p.JSON())
		if !strings.Contains(got, tc.want) {
			t.Errorf("%s: payload %s does not contain %s", tc.name, got, tc.want)
		}
		for _, leak := range []string{"192.0.2.10", "jane", "Jane", "Doe", "s3cret", "+44", "Berlin", "Example Street", "10115", "AbCdEfGh", "mkt_tok", "?"} {
			if strings.Contains(got, leak) {
				t.Errorf("%s: payload leaks %q: %s", tc.name, leak, got)
			}
		}
	}
}

func TestBuildDropsUnknownLeadFields(t *testing.T) {
	p, err := Build(marketo.Lead{"firstName": "Jane", "phone": "+44 20 7946 0958", "custom__c": "x"}, nil, types)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(p.JSON()), `{"lead":{"role":"unknown role"}}`; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestBuildGuardsTheLeadProfile(t *testing.T) {
	l := lead()
	l["industry"] = "Call 555-010-0199 x"
	_, err := Build(l, nil, types)
	if err == nil || err.Error() != "redaction guard: phone number found in lead.industry; nothing was sent" {
		t.Fatalf("error = %v", err)
	}
}

func TestCompanyQueryOnly(t *testing.T) {
	p, err := Build(lead(), nil, types)
	if err != nil {
		t.Fatal(err)
	}
	got := string(p.Company().JSON())
	if want := `{"company":"Example Corp","industry":"Software","country":"Germany"}`; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestAccessors(t *testing.T) {
	p, err := Build(lead(), []marketo.Activity{{ID: 1, TypeID: 2, Primary: "Contact Us Form"}}, types)
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Company().Name(); got != "Example Corp" {
		t.Errorf("Name = %q", got)
	}
	before := string(p.JSON())
	ev := p.Events()
	if len(ev) != 1 || ev[0].ID != "E1" {
		t.Fatalf("Events = %+v", ev)
	}
	ev[0].Asset = "changed"
	if string(p.JSON()) != before {
		t.Error("Events must return a copy")
	}
	if empty, _ := Build(lead(), nil, types); empty.Events() == nil {
		t.Error("Events must not be nil, so --json prints []")
	}
}

func TestBuildRoleMailbox(t *testing.T) {
	l := marketo.Lead{"firstName": "Jane", "lastName": "Doe", "email": "marketing@example.com", "title": "Marketing Director"}
	p, err := Build(l, nil, types)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(p.JSON()), `{"lead":{"role":"senior marketing leader"}}`; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestBuildNumberOfEmployees(t *testing.T) {
	for _, tc := range []struct {
		in   any
		want string
	}{
		{json.Number("250"), `"numberOfEmployees":250`},
		{"10000000", `"numberOfEmployees":10000000`},
		{"55501001", ""},
		{" 42 ", `"numberOfEmployees":42`},
		{json.Number("0"), ""},
		{json.Number("-5"), ""},
		{"about 50", ""},
	} {
		l := lead()
		l["numberOfEmployees"] = tc.in
		p, err := Build(l, nil, types)
		if err != nil {
			t.Fatalf("%v: %v", tc.in, err)
		}
		got := string(p.JSON())
		if tc.want == "" && strings.Contains(got, "numberOfEmployees") || !strings.Contains(got, tc.want) {
			t.Errorf("%v: payload %s, want %q", tc.in, got, tc.want)
		}
	}
}

const marketoPkg = "github.com/canonical/snap-mkto/internal/marketo"

// marketoUse lists files in dir that import internal/marketo, and exported
// funcs and methods whose parameters use one of its types.
func marketoUse(t *testing.T, dir string) (imports, params []string) {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, path := range files {
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		pkgs := map[string]bool{}
		for _, imp := range f.Imports {
			if p, _ := strconv.Unquote(imp.Path.Value); p == marketoPkg {
				imports = append(imports, filepath.Base(path))
				name := "marketo"
				if imp.Name != nil {
					name = imp.Name.Name
				}
				pkgs[name] = true
			}
		}
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || !fn.Name.IsExported() || len(pkgs) == 0 {
				continue
			}
			ast.Inspect(fn.Type.Params, func(n ast.Node) bool {
				if sel, ok := n.(*ast.SelectorExpr); ok {
					if x, ok := sel.X.(*ast.Ident); ok && pkgs[x.Name] {
						params = append(params, fn.Name.Name)
					}
				}
				return true
			})
		}
	}
	return imports, params
}

func TestMarketoUseDetects(t *testing.T) {
	dir := t.TempDir()
	src := "package llm\nimport m \"" + marketoPkg + "\"\n" +
		"func Narrate(acts []m.Activity) {}\nfunc ok(l m.Lead) {}\n"
	if err := os.WriteFile(filepath.Join(dir, "llm.go"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "clean.go"), []byte("package llm\nfunc Clean(s string) {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	imports, params := marketoUse(t, dir)
	if len(imports) != 1 || imports[0] != "llm.go" || len(params) != 1 || params[0] != "Narrate" {
		t.Fatalf("imports %v, params %v; want [llm.go], [Narrate]", imports, params)
	}
}

func TestLLMTakesOnlyRedactTypes(t *testing.T) {
	dir := filepath.Join("..", "llm")
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		t.Fatal("internal/llm is missing: the type-enforcement check has nothing to scan")
	}
	imports, params := marketoUse(t, dir)
	if len(imports) > 0 {
		t.Errorf("internal/llm files import internal/marketo: %v", imports)
	}
	if len(params) > 0 {
		t.Errorf("exported internal/llm functions take internal/marketo types: %v", params)
	}
	if m := providerWithoutRedact(t, dir); len(m) > 0 {
		t.Errorf("llm.Provider methods without a redact.* parameter: %v", m)
	}
}

// providerWithoutRedact lists the methods of the Provider interface in dir that take no redact.* parameter.
func providerWithoutRedact(t *testing.T, dir string) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	var bad []string
	found := false
	for _, path := range files {
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			ts, ok := n.(*ast.TypeSpec)
			if !ok || ts.Name.Name != "Provider" {
				return true
			}
			found = true
			for _, m := range ts.Type.(*ast.InterfaceType).Methods.List {
				redacted := false
				ast.Inspect(m.Type.(*ast.FuncType).Params, func(n ast.Node) bool {
					if sel, ok := n.(*ast.SelectorExpr); ok {
						if x, ok := sel.X.(*ast.Ident); ok && x.Name == "redact" {
							redacted = true
						}
					}
					return true
				})
				if !redacted && len(m.Names) > 0 {
					bad = append(bad, m.Names[0].Name)
				}
			}
			return false
		})
	}
	if !found {
		t.Fatalf("no Provider interface in %s", dir)
	}
	return bad
}

func TestProviderWithoutRedactDetects(t *testing.T) {
	dir := t.TempDir()
	src := "package llm\nimport \"" + "github.com/canonical/snap-mkto/internal/redact" + "\"\n" +
		"type Provider interface {\n\tA(p redact.Payload) error\n\tB(s string) error\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "llm.go"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := providerWithoutRedact(t, dir); len(got) != 1 || got[0] != "B" {
		t.Errorf("got %v, want [B]", got)
	}
}
