package sqlite

// v4Briefs copies pr_briefs into unit_briefs and pr_tags into unit_tags as
// github units, fills each brief run's provider, unit_key, revision, input
// and intent from the columns v4 drops, and copies each Slack unit's
// extraction_cursors.brief into a legacy brief run its unit_briefs row uses.
//
// Revision and input are the inventory store's briefRevision and
// briefInputJSON of the dropped columns, so a migrated brief reads as made
// from the pull request it was made from and the upgrade briefs nothing
// again. The legacy runs fill owner, repo and number, NOT NULL until v4Drop.
var v4Briefs = execAll(
	`INSERT INTO unit_briefs (provider, unit_key, failures, attempt_of, run, last_run, wanted_at, retry_at, gave_up_at, gave_up_reason)
	SELECT 'github', owner || '/' || repo || '#' || number, failures, attempt_of, run, last_run, wanted_at, retry_at, gave_up_at, gave_up_reason
	FROM pr_briefs`,
	`INSERT INTO unit_tags (provider, unit_key, key, value, run)
	SELECT 'github', owner || '/' || repo || '#' || number, key, value, run FROM pr_tags`,
	`UPDATE brief_runs SET provider = 'github', unit_key = owner || '/' || repo || '#' || number, intent = 'brief',
	revision = head_oid || ' ' || body_edited_at,
	input = CASE WHEN json_valid(files) AND json_type(files) = 'array' AND json_array_length(files) > 0
		THEN json_object('changed_files', changed_files, 'files', json(files))
		ELSE json_object('changed_files', changed_files) END`,
	`INSERT INTO brief_runs (owner, repo, number, provider, unit_key, intent, trigger, version, attempt_of, started_at, ended_at, outcome, brief)
	SELECT '', '', 0, provider, unit_key, 'brief', 'legacy', 'legacy', '', at, at, 'ok', json_object('goal', brief)
	FROM extraction_cursors WHERE brief != '' ORDER BY provider, unit_key`,
	`INSERT INTO unit_briefs (provider, unit_key, run, last_run)
	SELECT provider, unit_key, id, id FROM brief_runs WHERE trigger = 'legacy' AND provider != 'github'`,
)
