-- The administrator console (CP21): one permission the catalogue lacked, and nothing else.
--
-- Everything the console does — invite, suspend, grant, revoke, end sessions — already had
-- a permission and a table. What it lacked was a name for "reset somebody's credentials":
-- setting a password in person, and resetting an authenticator for a person who has lost
-- both the phone and the recovery codes (the path CP17 left to this checkpoint). Neither is
-- an invitation and neither is a suspension, and a permission that means two things is a
-- permission nobody can revoke precisely.

-- +goose Up

INSERT INTO core.permission (code, resource, action, scope, description, is_sensitive) VALUES
  ('user.credential.reset', 'user', 'credential', 'reset',
   'Set a staff member''s password in person, reset their authenticator, or end their sessions', false)
ON CONFLICT (code) DO UPDATE SET
  resource = EXCLUDED.resource, action = EXCLUDED.action, scope = EXCLUDED.scope,
  description = EXCLUDED.description, is_sensitive = EXCLUDED.is_sensitive;

INSERT INTO core.role_permission (role_id, permission_code)
SELECT r.id, p.code FROM core.role r, core.permission p
 WHERE r.code = 'ADMIN' AND p.code = 'user.credential.reset'
ON CONFLICT DO NOTHING;

-- The purposes a step-up token may be minted for grew by two; the table does not enumerate
-- them (the application does), so nothing to change here.

-- +goose Down

DELETE FROM core.role_permission WHERE permission_code = 'user.credential.reset';
DELETE FROM core.permission WHERE code = 'user.credential.reset';
