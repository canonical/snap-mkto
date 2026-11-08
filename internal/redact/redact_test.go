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
	"slices"
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
	13: "Change Data Value", 22: "Change Status in Progression", 46: "Interesting Moment", 100: "Change Score",
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
	p, err := Build(lead(), acts, types, Options{})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"lead":{"company":"Example Corp","industry":"Software","country":"Germany","numberOfEmployees":250,"role":"senior engineering leader"},` +
		`"events":[{"id":"E1","date":"2026-10-02T10:00:00Z","type":"Visit Webpage","url":"https://www.example.com/pricing"},` +
		`{"id":"E2","date":"2026-10-03T07:00:00Z","type":"Fill Out Form","asset":"Contact Us Form"},` +
		`{"id":"E3","date":"2026-10-04T08:00:00Z","type":"Interesting Moment"}]}`
	if got := string(p.JSON()); got != want {
		t.Fatalf("payload\n got %s\nwant %s", got, want)
	}
}

func TestBuild(t *testing.T) {
	for _, tc := range []struct {
		name string
		act  marketo.Activity
		want string // a fragment the payload must contain
		kind string // the one redaction expected in events[0], if any
	}{
		{name: "email inside a URL path", act: act(1, 3, at(1, 9, 0), "https://www.example.com/u/jane.doe@example.com/prefs", nil),
			want: `"url":"https://www.example.com/u/{redacted}/prefs"`, kind: "email or token in URL"},
		{name: "mkt_tok in a query string", act: act(1, 3, at(1, 9, 0), "https://www.example.com/pricing?mkt_tok=MDAwLUFCQy0xMjMAAAGExampleTok", nil),
			want: `"url":"https://www.example.com/pricing"}`},
		{name: "prefilled form URL with email in the query", act: act(1, 1, at(1, 9, 0), "www.example.com/contact?email=jane.doe%40example.com", nil),
			want: `"url":"https://www.example.com/contact"}`},
		{name: "userinfo URL", act: act(1, 3, at(1, 9, 0), "https://jane:s3cret@www.example.com/a", nil),
			want: `"url":"https://www.example.com/a"}`},
		{name: "token segment", act: act(1, 3, at(1, 9, 0), "https://www.example.com/r/AbCdEfGhIjKlMnOpQrStUvWx/next", nil),
			want: `"url":"https://www.example.com/r/{redacted}/next"}`, kind: "email or token in URL"},
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
			want: `"url":"https://www.example.com/call/{redacted}"}`, kind: "phone number"},
		{name: "phone with a slash in an asset", act: act(1, 10, at(1, 9, 0), "Tel 030/12345678", nil),
			want: `"asset":"Tel {redacted}"}`, kind: "phone number"},
		{name: "unparseable URL keeps type and date", act: act(1, 3, at(1, 9, 0), "/relative?x=1", nil),
			want: `{"id":"E1","date":"2026-10-01T09:00:00Z","type":"Click Link"}`},
		{name: "IP in an attribute is dropped", act: act(1, 10, at(1, 9, 0), "November Newsletter", map[string]any{"Client IP Address": "192.0.2.10"}),
			want: `"asset":"November Newsletter"}`},
		{name: "Interesting Moment text dropped", act: act(1, 46, at(1, 9, 0), "Jane Doe, 192.0.2.10, wants a call", nil),
			want: `{"id":"E1","date":"2026-10-01T09:00:00Z","type":"Interesting Moment"}`},
		{name: "other types keep type and date only", act: act(1, 999, at(1, 9, 0), "Jane Doe", map[string]any{"New Value": "+44 20 7946 0958"}),
			want: `{"id":"E1","date":"2026-10-01T09:00:00Z","type":"activity type 999"}`},
		{name: "asset types match case-insensitively", act: act(1, 22, at(1, 9, 0), "Webinar Q4 > Attended", nil),
			want: `"asset":"Webinar Q4 > Attended"}`},
		{name: "ISO date not flagged as a phone", act: act(1, 10, at(3, 9, 59), "November Newsletter", nil),
			want: `"date":"2026-10-03T09:00:00Z"`},
		{name: "first name inside an email asset name", act: act(1, 10, at(1, 9, 0), "Follow-up for Jane", nil),
			want: `"asset":"Follow-up for {redacted}"}`, kind: "lead's first name"},
		{name: "IP pasted into an asset name", act: act(1, 10, at(1, 9, 0), "Webinar 192.0.2.55 recap", nil),
			want: `"asset":"Webinar {redacted} recap"}`, kind: "IP address"},
	} {
		p, err := Build(lead(), []marketo.Activity{tc.act}, types, Options{})
		if err != nil {
			t.Errorf("%s: unexpected error: %v", tc.name, err)
			continue
		}
		got := string(p.JSON())
		if !strings.Contains(got, tc.want) {
			t.Errorf("%s: payload %s does not contain %s", tc.name, got, tc.want)
		}
		var want []Redaction
		if tc.kind != "" {
			want = []Redaction{{tc.kind, "events[0]." + map[bool]string{true: "url", false: "asset"}[strings.Contains(tc.want, `"url"`)]}}
		}
		if r := p.Redactions(); !slices.Equal(r, want) {
			t.Errorf("%s: Redactions = %v, want %v", tc.name, r, want)
		}
		for _, leak := range []string{"192.0.2.10", "192.0.2.55", "12345678", "jane", "Jane", "Doe", "s3cret", "+44", "Berlin", "Example Street", "10115", "AbCdEfGh", "mkt_tok", "?"} {
			if strings.Contains(got, leak) {
				t.Errorf("%s: payload leaks %q: %s", tc.name, leak, got)
			}
		}
	}
}

func TestBuildDropsUnknownLeadFields(t *testing.T) {
	p, err := Build(marketo.Lead{"firstName": "Jane", "phone": "+44 20 7946 0958", "custom__c": "x"}, nil, types, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(p.JSON()), `{"lead":{"role":"unknown role"}}`; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestBuildRedactsTheLeadProfile(t *testing.T) {
	l := lead()
	l["industry"] = "Call 555-010-0199 x"
	p, err := Build(l, nil, types, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got := string(p.JSON()); !strings.Contains(got, `"industry":"Call {redacted} x"`) || strings.Contains(got, "0199") {
		t.Errorf("payload %s", got)
	}
	if r := p.Redactions(); !slices.Equal(r, []Redaction{{"phone number", "lead.industry"}}) {
		t.Errorf("Redactions = %v", r)
	}
}

// TestBuildRedactsInPlace: every hit is replaced, the rest kept, and Redactions lists kind and path.
func TestBuildRedactsInPlace(t *testing.T) {
	l := lead()
	l["company"] = "Jane's Bakery"
	acts := []marketo.Activity{
		act(1, 10, at(1, 9, 0), "Validation Report 2026-10-11 01:20:10.Email 1", nil),
		act(2, 10, at(2, 9, 0), "Call 555-010-0199 after 2026-10-11 09:00", nil),
		act(3, 10, at(3, 9, 0), "Invite for bob.smith@example.org (EMEA)", nil),
		act(4, 10, at(4, 9, 0), "Follow-up for Jane", nil),
		act(5, 10, at(5, 9, 0), "Webinar 2001:db8::7 replay", nil),
		act(6, 10, at(6, 9, 0), "+44 20 7946 0958", nil),
	}
	p, err := Build(l, acts, types, Options{})
	if err != nil {
		t.Fatal(err)
	}
	got := string(p.JSON())
	for _, want := range []string{
		`"company":"{redacted}'s Bakery"`,
		`"asset":"Validation Report 2026-10-11 01:20:10.Email 1"`,
		`"asset":"Call {redacted} after 2026-10-11 09:00"`,
		`"asset":"Invite for {redacted} (EMEA)"`,
		`"asset":"Follow-up for {redacted}"`,
		`"asset":"Webinar {redacted} replay"`,
		`"type":"Open Email","asset":"{redacted}"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("payload lacks %s:\n%s", want, got)
		}
	}
	if q := string(p.Company().JSON()); q != `{"company":"{redacted}'s Bakery","industry":"Software","country":"Germany"}` {
		t.Errorf("company query %s", q)
	}
	for _, leak := range []string{"Jane", "555-010", "0199", "bob.smith", "example.org", "2001:db8", "+44", "7946"} {
		if strings.Contains(got, leak) || strings.Contains(string(p.Company().JSON()), leak) {
			t.Errorf("payload leaks %q", leak)
		}
	}
	want := []Redaction{
		{"lead's first name", "lead.company"}, {"phone number", "events[1].asset"}, {"email address", "events[2].asset"},
		{"lead's first name", "events[3].asset"}, {"IP address", "events[4].asset"}, {"phone number", "events[5].asset"},
	}
	if r := p.Redactions(); !slices.Equal(r, want) {
		t.Errorf("Redactions =\n%v\nwant\n%v", r, want)
	}
	if clean, _ := Build(lead(), nil, types, Options{}); len(clean.Redactions()) != 0 {
		t.Errorf("clean payload Redactions = %v", clean.Redactions())
	}
}

// TestBuildFailsClosed: if a hit survives redaction, Build aborts naming kind and path, never the value.
func TestBuildFailsClosed(t *testing.T) {
	defer func(f func(detector, string, string) (string, []string)) { redactFn = f }(redactFn)
	redactFn = func(_ detector, _, s string) (string, []string) { return s, nil } // a broken redactor
	_, err := Build(lead(), []marketo.Activity{act(1, 10, at(1, 9, 0), "Follow-up for Jane", nil)}, types, Options{})
	if err == nil || err.Error() != "redaction guard: lead's first name found in events[0].asset; nothing was sent" {
		t.Fatalf("error = %v", err)
	}
}

func TestCompanyQueryOnly(t *testing.T) {
	p, err := Build(lead(), nil, types, Options{})
	if err != nil {
		t.Fatal(err)
	}
	got := string(p.Company().JSON())
	if want := `{"company":"Example Corp","industry":"Software","country":"Germany"}`; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestAccessors(t *testing.T) {
	p, err := Build(lead(), []marketo.Activity{{ID: 1, TypeID: 2, Primary: "Contact Us Form"}}, types, Options{})
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
	if empty, _ := Build(lead(), nil, types, Options{}); empty.Events() == nil {
		t.Error("Events must not be nil, so --json prints []")
	}
}

func TestBuildRoleMailbox(t *testing.T) {
	l := marketo.Lead{"firstName": "Jane", "lastName": "Doe", "email": "marketing@example.com", "title": "Marketing Director"}
	p, err := Build(l, nil, types, Options{})
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
		p, err := Build(l, nil, types, Options{})
		if err != nil {
			t.Fatalf("%v: %v", tc.in, err)
		}
		got := string(p.JSON())
		if tc.want == "" && strings.Contains(got, "numberOfEmployees") || !strings.Contains(got, tc.want) {
			t.Errorf("%v: payload %s, want %q", tc.in, got, tc.want)
		}
	}
}

// TestPayloadScoreChangeNumbersOnly: a score change sends the field label and its
// numbers, never the Reason or any other attribute (D2).
func TestPayloadScoreChangeNumbersOnly(t *testing.T) {
	for _, tc := range []struct {
		name  string
		attrs map[string]any
		want  string // the event's JSON
	}{
		{"signed delta and old to new", map[string]any{"Change Value": "+25", "Old Value": "60", "New Value": "85", "Reason": "Filled out form for Jane"},
			`"change":"+25 (60→85)"`},
		{"bare JSON numbers", map[string]any{"Change Value": 25.0, "Old Value": 60.0, "New Value": 85.0},
			`"change":"+25 (60→85)"`},
		{"negative", map[string]any{"Change Value": "-10", "Old Value": "85", "New Value": "75"},
			`"change":"-10 (85→75)"`},
		{"delta from old and new", map[string]any{"Old Value": "60", "New Value": "85.5"},
			`"change":"+25.5 (60→85.5)"`},
		{"text in a number is dropped", map[string]any{"Change Value": "+25 by Jane", "Old Value": "sixty", "New Value": "85"},
			`"asset":"Behavior Score"}`},
		{"NaN is not a number", map[string]any{"Change Value": "NaN", "Old Value": "60", "New Value": "Inf"},
			`"asset":"Behavior Score"}`},
	} {
		p, err := Build(lead(), []marketo.Activity{act(1, 100, at(1, 9, 0), "Behavior Score", tc.attrs)}, types, Options{})
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		got := string(p.JSON())
		if want := `{"id":"E1","date":"2026-10-01T09:00:00Z","type":"Change Score","asset":"Behavior Score"`; !strings.Contains(got, want) || !strings.Contains(got, tc.want) {
			t.Errorf("%s: payload %s, want %s", tc.name, got, tc.want)
		}
		for _, leak := range []string{"Filled", "Jane", "sixty", "NaN", "Inf", "Reason"} {
			if strings.Contains(got, leak) {
				t.Errorf("%s: payload leaks %q: %s", tc.name, leak, got)
			}
		}
	}
}

// TestPayloadDataValueOnlyStatusField: only the configured status field's rows are
// sent, as old→new; every other field's row is dropped, and ids stay consecutive.
func TestPayloadDataValueOnlyStatusField(t *testing.T) {
	acts := []marketo.Activity{
		act(1, 13, at(1, 9, 0), "Phone", map[string]any{"Old Value": "", "New Value": "+44 20 7946 0958"}),
		act(2, 13, at(2, 9, 0), "Lead Status", map[string]any{"Old Value": "Nurture", "New Value": "MQL", "Reason": "Changed by Jane", "Source": "Campaign"}),
		act(3, 13, at(3, 9, 0), "Job Title", map[string]any{"Old Value": "CTO", "New Value": "VP"}),
		act(4, 22, at(4, 9, 0), "Webinar Q4", map[string]any{"Old Status": "Invited", "New Status": "Attended", "Reason": "Jane checked in"}),
		act(5, 10, at(5, 9, 0), "November Newsletter", nil),
	}
	p, err := Build(lead(), acts, types, Options{StatusField: " lead status "})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"lead":{"company":"Example Corp","industry":"Software","country":"Germany","numberOfEmployees":250,"role":"senior engineering leader"},"events":[` +
		`{"id":"E1","date":"2026-10-02T09:00:00Z","type":"Change Data Value","asset":"Lead Status","change":"Nurture→MQL"},` +
		`{"id":"E2","date":"2026-10-04T09:00:00Z","type":"Change Status in Progression","asset":"Webinar Q4","change":"→ Attended"},` +
		`{"id":"E3","date":"2026-10-05T09:00:00Z","type":"Open Email","asset":"November Newsletter"}]}`
	if got := string(p.JSON()); got != want {
		t.Fatalf("payload\n got %s\nwant %s", got, want)
	}
	unset, err := Build(lead(), acts, types, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got := string(unset.JSON()); strings.Contains(got, "Change Data Value") {
		t.Errorf("no status_field must drop every Change Data Value row: %s", got)
	}
}

func TestPayloadDropsEmailDelivered(t *testing.T) {
	acts := []marketo.Activity{
		act(1, 7, at(1, 9, 0), "November Newsletter", nil),
		act(2, 10, at(2, 9, 0), "November Newsletter", nil),
	}
	p, err := Build(lead(), acts, types, Options{})
	if err != nil {
		t.Fatal(err)
	}
	ev := p.Events()
	if len(ev) != 1 || ev[0].ID != "E1" || ev[0].Type != "Open Email" {
		t.Fatalf("Events = %+v", ev)
	}
}

// TestGuardScansChange: personal data in a status value is redacted in place, and a
// hit that survives aborts Build naming events[i].change.
func TestGuardScansChange(t *testing.T) {
	acts := []marketo.Activity{
		act(1, 13, at(1, 9, 0), "Lead Status", map[string]any{"Old Value": "Nurture", "New Value": "Call Jane on +44 20 7946 0958"}),
		act(2, 22, at(2, 9, 0), "Webinar Q4", map[string]any{"New Status": "Sent to bob.smith@example.org"}),
	}
	p, err := Build(lead(), acts, types, Options{StatusField: "Lead Status"})
	if err != nil {
		t.Fatal(err)
	}
	got := string(p.JSON())
	for _, want := range []string{`"change":"Nurture→Call {redacted} on {redacted}"`, `"change":"→ Sent to {redacted}"`} {
		if !strings.Contains(got, want) {
			t.Errorf("payload lacks %s:\n%s", want, got)
		}
	}
	want := []Redaction{{"phone number", "events[0].change"}, {"lead's first name", "events[0].change"}, {"email address", "events[1].change"}}
	if r := p.Redactions(); !slices.Equal(r, want) {
		t.Errorf("Redactions =\n%v\nwant\n%v", r, want)
	}

	defer func(f func(detector, string, string) (string, []string)) { redactFn = f }(redactFn)
	redactFn = func(_ detector, _, s string) (string, []string) { return s, nil } // a broken redactor
	_, err = Build(lead(), acts[:1], types, Options{StatusField: "Lead Status"})
	if err == nil || !strings.Contains(err.Error(), "found in events[0].change") || strings.Contains(err.Error(), "7946") {
		t.Fatalf("error = %v", err)
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

func TestPayloadRefusesPersonalStatusField(t *testing.T) {
	acts := []marketo.Activity{
		act(1, 13, at(1, 9, 0), "Email Address", map[string]any{"Old Value": "old@example.org", "New Value": "new@example.org"}),
		act(2, 13, at(2, 9, 0), "Last Name", map[string]any{"Old Value": "Maidenname", "New Value": "Doe"}),
		act(3, 13, at(3, 9, 0), "Street Address", map[string]any{"Old Value": "1 Example Way", "New Value": "2 Example Way"}),
	}
	for _, f := range []string{"Email Address", "Last Name", "Street Address", "Mobile Phone", "Postal Code", "IP Address"} {
		if !PersonalLabel(f) {
			t.Errorf("PersonalLabel(%q) = false", f)
		}
		p, err := Build(lead(), acts, types, Options{StatusField: f})
		if err != nil {
			t.Fatal(err)
		}
		if got := string(p.JSON()); strings.Contains(got, "Change Data Value") || strings.Contains(got, "example.org") || strings.Contains(got, "Maidenname") || strings.Contains(got, "Example Way") {
			t.Errorf("status_field %q: personal field changes sent: %s", f, got)
		}
	}
	for _, f := range []string{"Lead Status", "Lifecycle Stage", "MQL Status"} {
		if PersonalLabel(f) {
			t.Errorf("PersonalLabel(%q) = true, want false", f)
		}
	}
}

func TestPayloadCleansControlAndBidi(t *testing.T) {
	acts := []marketo.Activity{act(1, 10, at(1, 9, 0), "Invoice ‮fdp.exe Newsletter\u0085", nil)}
	p, err := Build(lead(), acts, types, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got := string(p.JSON()); strings.ContainsAny(got, "‮\u0085") {
		t.Errorf("bidi/C1 characters reached the payload: %q", got)
	}
}
