import { describe, expect, it } from 'vitest';
import {
  looksLikeUnifiedDiff,
  parseUnifiedDiff,
  recoverJsonContent,
} from './diff';

const sampleDiff = `diff --git a/frontend/src/components/ChatView.tsx b/frontend/src/components/ChatView.tsx
index cd3b385..d838463 100644
--- a/frontend/src/components/ChatView.tsx
+++ b/frontend/src/components/ChatView.tsx
@@ -81,6 +82,11 @@ function groupToolCalls(
 const out: (AssistantItem | ToolCallItem[])[] = [];
 let cur: ToolCallItem[] | null = null;
 for (const item of items) {
+    // update_plan renders once in the top-left plan panel instead of
+    // as transcript cards, so its calls are dropped from the flow.
+    if (item.kind === 'tool_call' && item.tool.name === 'update_plan') {
+      continue;
+    }
 if (item.kind === 'tool_call' && !nonGroupedTools.has(item.tool.name)) {
 if (!cur) cur = [];
 cur.push(item);
@@ -935,90 +942,94 @@ export function ChatView() {
 const messages = conv?.messages ?? [];
-const busy = conv?.busy ?? false;
+const busy2 = conv?.busy ?? false;
 const turnArtifacts = conv?.turnArtifacts ?? [];
`;

describe('parseUnifiedDiff', () => {
  it('detects git diffs', () => {
    expect(looksLikeUnifiedDiff(sampleDiff)).toBe(true);
    expect(looksLikeUnifiedDiff('plain text')).toBe(false);
    expect(looksLikeUnifiedDiff('diff --git a/x b/x\n@@ -1 +1 @@')).toBe(true);
  });

  it('parses files, hunks and line numbers', () => {
    const files = parseUnifiedDiff(sampleDiff);
    expect(files).not.toBeNull();
    expect(files!.length).toBe(1);
    const f = files![0];
    expect(f.path).toBe('frontend/src/components/ChatView.tsx');
    expect(f.action).toBe('update');
    expect(f.added).toBe(6);
    expect(f.removed).toBe(1);
    expect(f.lines.length).toBeGreaterThan(0);
    // The first hunk starts at old line 81; the first context line must
    // carry old_num 81 and new_num 82 (the "+" lines shifted the new
    // side by five).
    const first = f.lines.find((l) => l.old_num === 81);
    expect(first?.kind).toBe('context');
    expect(first?.new_num).toBe(82);
    // The second hunk re-seeds counters from its "@@" header even
    // though git emits it directly after the first hunk's content.
    const second = f.lines.find((l) => l.old_num === 935);
    expect(second?.kind).toBe('context');
    expect(second?.new_num).toBe(942);
  });

  it('handles add/delete file actions', () => {
    const files = parseUnifiedDiff(`diff --git a/new.txt b/new.txt
new file mode 100644
--- /dev/null
+++ b/new.txt
@@ -0,0 +1,2 @@
+hello
+world
`);
    expect(files![0].action).toBe('add');
    expect(files![0].added).toBe(2);

    const del = parseUnifiedDiff(`diff --git a/old.txt b/old.txt
deleted file mode 100644
--- a/old.txt
+++ /dev/null
@@ -1,2 +0,0 @@
-bye
-now
`);
    expect(del![0].action).toBe('delete');
    expect(del![0].removed).toBe(2);
  });

  it('returns null for non-diff text', () => {
    expect(parseUnifiedDiff('hello world')).toBeNull();
    expect(parseUnifiedDiff('')).toBeNull();
  });

  it('recovers content from a corrupted read_file envelope', () => {
    // Historical archives occasionally store a raw newline inside the
    // JSON string, which breaks strict parsing. The envelope shape is
    // fixed, so the content is recoverable.
    const corrupt =
      '{"content":"diff --git a/x b/x\\nindex 1..2 100644\\n--- a/x\\n+++ b/x\\n@@ -1 +1 @@\\n' +
      '-old\\n+new\n' + // <-- raw newline, breaks JSON.parse
      '","file_path":"/tmp/x.diff","is_truncated":false}';
    expect(() => JSON.parse(corrupt)).toThrow();
    const recovered = recoverJsonContent(corrupt);
    expect(recovered).not.toBeNull();
    expect(recovered!).toContain('diff --git a/x b/x');
    expect(recovered!).toContain('-old');
    expect(recovered!).toContain('+new');
    // Recovered content parses as a unified diff.
    expect(parseUnifiedDiff(recovered!)).not.toBeNull();
  });

  it('returns null when no content envelope is present', () => {
    expect(recoverJsonContent('plain text')).toBeNull();
    expect(recoverJsonContent('{"other": 1}')).toBeNull();
  });
});
