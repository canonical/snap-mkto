// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"regexp"
	"testing"

	"github.com/canonical/snap-mkto/internal/marketotest"
)

// demoLeads and demoAccount are clean invented leads for the README GIFs (tools/demo_gif.py).
const (
	demoLeads = `{"success":true,"result":[
{"id":1001,"email":"jane.doe@example.com","leadScore":85,"leadStatus":"MQL","leadOwnerEmailAddress":"a.rep@example.com","updatedAt":"2026-11-06T09:15:00Z"},
{"id":1002,"email":"john.roe@example.com","leadScore":42,"leadStatus":"Nurture","leadOwnerEmailAddress":"a.rep@example.com","updatedAt":"2026-11-02T14:40:00Z"},
{"id":1003,"email":"sam.poe@example.com","leadScore":12,"leadStatus":"New","updatedAt":"2026-10-28T08:05:00Z"}]}`
	demoLeadsActs = `{"success":true,"moreResult":false,"result":[
{"id":1,"leadId":1001,"activityDate":"2026-11-07T10:00:00Z","activityTypeId":2,"primaryAttributeValue":"Contact Us"},
{"id":2,"leadId":1001,"activityDate":"2026-11-06T09:00:00Z","activityTypeId":11,"primaryAttributeValue":"Newsletter"},
{"id":3,"leadId":1001,"activityDate":"2026-11-06T08:55:00Z","activityTypeId":10,"primaryAttributeValue":"Newsletter"},
{"id":4,"leadId":1002,"activityDate":"2026-11-03T16:00:00Z","activityTypeId":10,"primaryAttributeValue":"Newsletter"},
{"id":5,"leadId":1002,"activityDate":"2026-11-01T11:00:00Z","activityTypeId":10,"primaryAttributeValue":"Webinar Invite"}]}`

	demoAccount = `{"success":true,"result":[
{"id":2001,"email":"priya.shah@example.com","firstName":"Priya","lastName":"Shah","title":"Head of Security","leadScore":92,"leadStatus":"MQL","leadOwnerEmailAddress":"a.rep@example.com"},
{"id":2002,"email":"marco.rossi@example.com","firstName":"Marco","lastName":"Rossi","title":"IT Director","leadScore":64,"leadStatus":"Nurture","leadOwnerEmailAddress":"a.rep@example.com"},
{"id":2003,"email":"ana.lima@example.com","firstName":"Ana","lastName":"Lima","title":"Facilities Manager","leadScore":35,"leadStatus":"New"},
{"id":2004,"email":"tom.becker@example.com","firstName":"Tom","lastName":"Becker","title":"Network Engineer","leadScore":18,"leadStatus":"Recycled"}]}`
	demoAccountActs = `{"success":true,"moreResult":false,"result":[
{"id":1,"leadId":2001,"activityDate":"2026-11-07T09:30:00Z","activityTypeId":2,"primaryAttributeValue":"Contact Us"},
{"id":2,"leadId":2001,"activityDate":"2026-11-05T15:00:00Z","activityTypeId":2,"primaryAttributeValue":"Webinar"},
{"id":3,"leadId":2001,"activityDate":"2026-11-04T10:00:00Z","activityTypeId":11,"primaryAttributeValue":"Newsletter"},
{"id":4,"leadId":2002,"activityDate":"2026-11-03T12:00:00Z","activityTypeId":10,"primaryAttributeValue":"Newsletter"},
{"id":5,"leadId":2002,"activityDate":"2026-11-01T12:00:00Z","activityTypeId":10,"primaryAttributeValue":"Webinar Invite"},
{"id":6,"leadId":2003,"activityDate":"2026-10-29T08:00:00Z","activityTypeId":2,"primaryAttributeValue":"Pricing"},
{"id":7,"leadId":2004,"activityDate":"2026-10-26T17:00:00Z","activityTypeId":10,"primaryAttributeValue":"Newsletter"}]}`
)

// TestDemoGoldens writes the outputs the README GIFs replay; the command lines here are the GIF captions.
func TestDemoGoldens(t *testing.T) {
	check := func(name, out, errOut string, code int) {
		t.Helper()
		if code != 0 {
			t.Fatalf("%s: exit %d: %s", name, code, errOut)
		}
		golden(t, name, out)
	}

	// mkto doctor
	out, errOut, code := doctor(t, doctorServer(t), doctorFields)
	check("demo_doctor.txt", regexp.MustCompile(`(?m)^✓ config: \S+config\.toml`).ReplaceAllString(out, "✓ config: <config>"), errOut, code)

	// mkto activity jane.doe@example.com --days 60
	s := storyServer(t)
	s.Handle(leadsPath, marketotest.Response{Body: "leads_one_scored.json"})
	out, errOut, code = fakeCLIFull(t, s, fixedNow, "", "lead_url = \"https://marketo.example.com/leads/{id}/detail\"\n", nil,
		"activity", "jane.doe@example.com", "--days", "60")
	check("demo_activity.txt", out, errOut, code)

	// mkto why jane.doe@example.com
	out, errOut, code = why(t, whyServer(t), whyConf)
	check("demo_why.txt", out, errOut, code)

	// mkto leads --from leads.csv --activity
	s = accountServer(t, marketotest.Response{Body: demoLeads})
	s.Handle(actsPath, marketotest.Response{Body: demoLeadsActs})
	from := writeInput(t, "email\njane.doe@example.com\njohn.roe@example.com\nsam.poe@example.com\nnobody@example.com\n")
	out, errOut, code = fakeCLINow(t, s, fixedNow, "leads", "--from", from, "--activity")
	check("demo_leads.txt", out, errOut, code)

	// mkto account 001EXAMPLE000001AAA
	s = accountServer(t, marketotest.Response{Body: demoAccount})
	s.Handle(actsPath, marketotest.Response{Body: demoAccountActs})
	out, errOut, code = account(t, s)
	check("demo_account.txt", out, errOut, code)
}
