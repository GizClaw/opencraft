-- Upgrade tool results written before flowcraft core v0.4.0. Older
-- builds stored one tool result as a flat string
-- ({"type": "tool_result", "result": {"call_id": "…", "content": "…"}});
-- the canonical shape is an ordered content payload
-- ({"parts": [{"type": "text", "text": "…"}]}). Every built-in tool
-- still answers with that same text — its JSON envelope — so the
-- string becomes one text part and nothing else moves: call ids,
-- error flags, part order and the parts around it stay as they are.
--
-- Both persisted copies of a message are rewritten. The archive is
-- what the chat resumes from, and a string content makes the whole
-- conversation undecodable; the memory window holds the same payload
-- and drops rows it cannot decode, which silently erased the tool
-- results of a resumed turn.
UPDATE archive_messages
SET content_json = json_object('parts', (
	SELECT json_group_array(
		CASE WHEN json_extract(je.value, '$.type') = 'tool_result'
		      AND json_type(je.value, '$.result.content') = 'text'
		     THEN json_set(
			je.value, '$.result.content',
			json_object('parts', json_array(
				json_object('type', 'text',
					'text', json_extract(je.value, '$.result.content')))))
		     ELSE je.value END
		ORDER BY je.key)
	FROM json_each(archive_messages.content_json, '$.parts') AS je
))
WHERE json_valid(content_json)
  AND EXISTS (
	SELECT 1 FROM json_each(content_json, '$.parts') AS je
	WHERE json_extract(je.value, '$.type') = 'tool_result'
	  AND json_type(je.value, '$.result.content') = 'text'
  );

UPDATE memory_items
SET payload = json_object('parts', (
	SELECT json_group_array(
		CASE WHEN json_extract(je.value, '$.type') = 'tool_result'
		      AND json_type(je.value, '$.result.content') = 'text'
		     THEN json_set(
			je.value, '$.result.content',
			json_object('parts', json_array(
				json_object('type', 'text',
					'text', json_extract(je.value, '$.result.content')))))
		     ELSE je.value END
		ORDER BY je.key)
	FROM json_each(memory_items.payload, '$.parts') AS je
))
WHERE json_valid(payload)
  AND EXISTS (
	SELECT 1 FROM json_each(payload, '$.parts') AS je
	WHERE json_extract(je.value, '$.type') = 'tool_result'
	  AND json_type(je.value, '$.result.content') = 'text'
  );
