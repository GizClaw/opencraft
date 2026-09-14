-- Upgrade memory_items payloads written by older versions from a
-- text-only shape ({"text": "..."}) to the canonical content shape
-- ({"parts": [{"type": "text", "text": "..."}]}).
--
-- Legacy rows never stored tool_call parts, so a legacy role='tool'
-- row cannot be replayed as a valid tool message; its role is moved to
-- 'user' to match the worldstate text fallback semantics.
UPDATE memory_items
SET payload = json_object(
	'parts',
	json_array(json_object('type', 'text', 'text', json_extract(payload, '$.text')))
)
WHERE json_valid(payload)
  AND json_extract(payload, '$.parts') IS NULL
  AND json_extract(payload, '$.text') IS NOT NULL;

-- A role='tool' row is only replayable as a real tool message when its
-- payload carries a tool_result part. Legacy text-only rows (rewritten
-- to text parts above) cannot be, so they degrade to 'user'. Canonical
-- rows with a text prefix before their tool_result keep the tool role;
-- only rows with no tool_result part at all are demoted. Checking every
-- part (not just parts[0]) keeps future writers that render a preamble
-- before the result from silently losing the tool role.
UPDATE memory_items
SET role = 'user'
WHERE role = 'tool'
  AND json_valid(payload)
  AND json_extract(payload, '$.parts') IS NOT NULL
  AND NOT EXISTS (
    SELECT 1 FROM json_each(payload, '$.parts')
    WHERE json_extract(value, '$.type') = 'tool_result'
  );
