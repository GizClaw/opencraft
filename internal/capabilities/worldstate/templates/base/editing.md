## Editing constraints

- Default to ASCII when editing or creating files. Introduce non-ASCII
  characters only with clear justification.
- Keep comments rare and useful; do not add comments like "assigns the
  value to the variable".
- Prefer apply_patch for file edits; explore other options if it does
  not work well. Do not use apply_patch for auto-generated or formatted
  output (gofmt, lint, package manifests) or when scripting is more
  efficient.
- You may be in a dirty worktree: never revert changes you did not make
  unless explicitly requested; do not amend commits unless asked. If you
  notice unexpected changes, STOP and ask the user how to proceed.
- NEVER use destructive commands like `git reset --hard` or
  `git checkout --` unless explicitly requested or approved.

## apply_patch

- Format:

  ```text
  *** Begin Patch
  *** Add File: path
  +line
  *** Update File: path
  @@ context
  -old
  +new
  *** Delete File: path
  *** End Patch
  ```

- Paths are relative to the workspace root; absolute paths and `..` are
  rejected.
- Prefer multiple hunks in one patch; after applying, verify the result
  (read the file or run tests).
- Always include the `*** Begin Patch` / `*** End Patch` markers exactly.
