# Fixtures (all values invented)
Base: https://experienceleague.adobe.com/en/docs/marketo-developer/marketo/rest/
| File | Endpoint | Case | Source | Status |
|---|---|---|---|---|
| identity_token_ok.json | GET /identity/oauth/token | ok | authentication | VERIFIED |
| error_601/602/606/607/615/713.json | any | response-level errors[] (code is a string) | error-codes | VERIFIED shape; messages for 601/602 per docs table |
| error_611.json | any | HTTP 200 success:false, "other" | error-codes | VERIFIED |
| leads_one / leads_two_duplicates.json | GET /rest/v1/leads.json | 1 / 2 leads | lead-database/leads | VERIFIED shape |
| leads_one_scored.json | GET /rest/v1/leads.json | 1 lead with profile fields (leadScore, leadStatus, leadOwnerEmailAddress, unsubscribed, emailInvalid, sfdcLeadId, sfdcContactId null, sfdcAccountId empty) | lead-database/leads | envelope VERIFIED; field names other than unsubscribed UNVERIFIED |
| leads_zero.json | same | empty: success:true, result:[] | lead-database/leads | VERIFIED |
| leads_1006_response.json | same | response-level 1006 | error-codes | UNVERIFIED (code listed; no example) |
| leads_1006_record.json | same | record-level skipped+reasons | error-codes (pattern only) | UNVERIFIED |
| activity_types.json | GET /rest/v1/activities/types.json | 25 types | lead-database/activities | shape VERIFIED (only id 2 documented); ids/names for other types UNVERIFIED (from memory) |
| pagingtoken_ok.json | GET /rest/v1/activities/pagingtoken.json | ok | lead-database/activities | UNVERIFIED (docs show no response) |
| activities_page_empty_more.json | GET /rest/v1/activities.json | empty result, moreResult:true | lead-database/activities | VERIFIED rule, invented body |
| activities_final_page.json | same | 2 rows, moreResult:false | lead-database/activities | VERIFIED envelope; attribute names for Fill Out Form/Visit Webpage VERIFIED, Open Email not included |
| program_membership_one.json | GET /rest/v1/leads/{id}/programMembership.json | one | lead-database/leads | fields VERIFIED by summary; program id is `id` (no `programId`); paging UNVERIFIED; dates written in the asset-API shape (`…Z+0000`), confirmed for programs on a real instance 2026-11-07; membership dates (membershipDate/updatedAt) CONFIRMED on a real instance 2026-11-08 |
| program_by_id_one.json | GET /rest/asset/v1/program/{id}.json | one | assets/programs | VERIFIED fields; date format confirmed on a real instance 2026-11-07 (`2026-11-01T01:08:44Z+0000`) |
| programs_browse_page / _empty.json | GET /rest/asset/v1/programs.json | page / end | assets/programs | fields VERIFIED; date format confirmed on a real instance 2026-11-07; empty body UNVERIFIED (docs silent) |
| usage_today / usage_last7days.json | GET /rest/v1/stats/usage[/last7days].json | ok | lead-database/usage | VERIFIED (7-day sample inferred) |
| activities_story_page.json | GET /rest/v1/activities.json | 12 story rows (types 12,13,2,100,10,11,22,8,27,9), moreResult:false | lead-database/activities | envelope and top-level fields id/marketoGUID/leadId/activityDate/activityTypeId/primaryAttributeValueId/primaryAttributeValue/attributes VERIFIED; `campaignId` UNVERIFIED (not in any docs example; present on some rows only); attribute names for Fill Out Form VERIFIED, Change Data Value Old Value/New Value/Reason/Source UNVERIFIED (docs show Reason/Source only on leadchanges); Change Score, Click Email, Open Email, bounces, progression, New Lead attributes UNVERIFIED |
| activity_types_story.json | GET /rest/v1/activities/types.json | 10 story types | lead-database/activities | shape VERIFIED (only id 2 documented); all other ids, primaryAttribute and attributes UNVERIFIED (from memory; id 27 Email Bounced Soft not in activity_types.json) |
| smart_campaign_one.json | GET /rest/asset/v1/smartCampaign/{id}.json | one | assets/smart-campaigns | VERIFIED fields (docs example); `type` and date format follow program asset shape; folder omitted as in docs |
| program_tokens_one.json | GET /rest/asset/v1/folder/{id}/tokens.json?folderType=Program | one program, 2 text tokens | assets/tokens | envelope and fields name/type/value/computedUrl VERIFIED; token names written WITHOUT `my.` prefix (docs example has none); whether API ever returns `my.` UNVERIFIED; `text` type name and folder.type "Program" UNVERIFIED (docs example uses Folder/date) |
| leads_describe.json | GET /rest/v1/leads/describe.json | 14 fields | lead-database/leads | fields id/displayName/dataType/length/rest{name,readOnly} VERIFIED by summary; `soap` child, ids and per-field readOnly values UNVERIFIED; `sfdcLeadOwnerId` (T42): REST name, label "SFDC Lead Owner Id", type string, length 50 and readOnly VERIFIED on Erekle's instance (2026-11-09 `mkto fields --search owner`) |
| leads_account_page1/2.json | GET /rest/v1/leads.json?filterType=sfdcAccountId | paged | lead-database/leads | UNVERIFIED: docs document nextPageToken/moreResult for other endpoints, not Get Leads by Filter Type; shape follows the generic envelope |
| error_1003.json | any | response-level 1003 | error-codes, lead-database/leads | code VERIFIED; message text per leads page ("Too many results match the filter"); error-codes table says "Invalid data" -- docs disagree |
| error_603.json | any | access denied | error-codes | code and message VERIFIED |
| leads_batch.json | GET /rest/v1/leads.json | 3 of 4 emails found; missing one simply absent | lead-database/leads | VERIFIED (absent-not-flagged behaviour per docs pattern) |
| list_membership_one.json | GET /rest/v1/leads/{id}/listMembership.json | one | lead-database/leads | fields listId/createdAt/updatedAt, nextPageToken, moreResult VERIFIED by summary; date format UNVERIFIED |
| usage_today_users.json | GET /rest/v1/stats/usage.json | two users | lead-database/usage | VERIFIED (users[{userId,count}]) |
| stats_errors.json | GET /rest/v1/stats/errors.json | one day | lead-database/usage | VERIFIED (errors[{errorCode,count}]) |
