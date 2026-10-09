-- 010_step_artifacts.sql — run_steps.artifacts_json (ui steps).
--
-- A ui step leaves files behind -- screenshots, the logcat slice it saw,
-- the page source of a screen it failed on -- under
-- .sapien/artifacts/<run_id>/<step_id>/. This column records them on the
-- step ([{kind, name, path}]) so get_run and the inspector can show them.
-- Additive-only: NULL for every existing row and every call step.
ALTER TABLE run_steps ADD COLUMN artifacts_json TEXT;
