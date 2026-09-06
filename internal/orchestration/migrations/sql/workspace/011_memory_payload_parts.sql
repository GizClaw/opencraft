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

UPDATE memory_items
SET role = 'user'
WHERE role = 'tool'
  AND json_extract(payload, '$.parts[0].type') = 'text';
