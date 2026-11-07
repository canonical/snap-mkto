# Fixtures (all values invented)
Base: https://experienceleague.adobe.com/en/docs/marketo-developer/marketo/rest/
| File | Endpoint | Case | Source | Status |
|---|---|---|---|---|
| identity_token_ok.json | GET /identity/oauth/token | ok | authentication | VERIFIED |
| error_601/602/606/607/615/713.json | any | response-level errors[] (code is a string) | error-codes | VERIFIED shape; messages for 601/602 per docs table |
| error_611.json | any | HTTP 200 success:false, "other" | error-codes | VERIFIED |
| leads_one / leads_two_duplicates.json | GET /rest/v1/leads.json | 1 / 2 leads | lead-database/leads | VERIFIED shape |
| leads_zero.json | same | empty: success:true, result:[] | lead-database/leads | VERIFIED |
| leads_1006_response.json | same | response-level 1006 | error-codes | UNVERIFIED (code listed; no example) |
| leads_1006_record.json | same | record-level skipped+reasons | error-codes (pattern only) | UNVERIFIED |
| activity_types.json | GET /rest/v1/activities/types.json | 25 types | lead-database/activities | shape VERIFIED (only id 2 documented); ids/names for other types UNVERIFIED (from memory) |
| pagingtoken_ok.json | GET /rest/v1/activities/pagingtoken.json | ok | lead-database/activities | UNVERIFIED (docs show no response) |
| activities_page_empty_more.json | GET /rest/v1/activities.json | empty result, moreResult:true | lead-database/activities | VERIFIED rule, invented body |
| activities_final_page.json | same | 2 rows, moreResult:false | lead-database/activities | VERIFIED envelope; attribute names for Fill Out Form/Visit Webpage VERIFIED, Open Email not included |
| program_membership_one.json | GET /rest/v1/leads/{id}/programMembership.json | one | lead-database/leads | fields VERIFIED by summary; program id is `id` (no `programId`); paging UNVERIFIED |
| program_by_id_one.json | GET /rest/asset/v1/program/{id}.json | one | assets/programs | VERIFIED fields |
| programs_browse_page / _empty.json | GET /rest/asset/v1/programs.json | page / end | assets/programs | fields VERIFIED; empty body UNVERIFIED (docs silent) |
| usage_today / usage_last7days.json | GET /rest/v1/stats/usage[/last7days].json | ok | lead-database/usage | VERIFIED (7-day sample inferred) |
