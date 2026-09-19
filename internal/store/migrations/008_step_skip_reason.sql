-- 008_step_skip_reason.sql — run_steps.skip_reason (PLAN.md §34f.7).
--
-- A step's Status can already be "skipped" for more than one reason (never
-- reached after an earlier failure, past a resume's until_step, or -- new --
-- its own `when` evaluating false); skip_reason distinguishes the last from
-- the rest without overloading status itself. Additive-only: every existing
-- row gets the default empty string, matching a StepResult whose SkipReason
-- was always "" before this field existed.
ALTER TABLE run_steps ADD COLUMN skip_reason TEXT NOT NULL DEFAULT '';
