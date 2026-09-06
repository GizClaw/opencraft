---
name: review
description: Review an artifact before it is finalized or merged — code, a document, a design, a plan, or research output; use when the user asks to review, critique, sanity-check, or double-check work.
---

# Review

Review only: identify findings and risks. Do not rewrite the artifact or
open a fix unless the user asks after the review.

## Before reviewing

- Confirm what is in scope: a diff or PR, a document, a design mockup,
  a plan, or a finished piece of work. If the workspace contains
  unrelated changes, review what the user points at, not everything.
- Understand the artifact's purpose and audience before judging it.
  Read enough of the surrounding material to evaluate intent and
  impact; do not speculate about things you have not inspected.

## What counts as a finding

Judge the artifact by its own standards:

- Code: correctness, regressions, security, performance, test gaps.
- Documents: accuracy, structure, clarity, consistency, whether they
  actually serve their stated purpose.
- Designs: intent, coherence, usability, feasibility, whether visual
  choices are deliberate rather than default.
- Plans and research: missing decisions, weak evidence, unstated
  assumptions, unverifiable claims.

Flag an issue when it is meaningful, discrete and actionable, relevant
to this artifact, and something the author would plausibly fix once
aware. Do not invent problems for the sake of having findings; do not
flag personal style preferences unless a documented convention applies.

## Findings format

Lead with findings, ordered by severity:

- `[P0]` blocking: the artifact is wrong, harmful, or unusable as
  delivered.
- `[P1]` urgent: should be fixed before this is finalized or merged.
- `[P2]` normal: should be fixed eventually.
- `[P3]` low: nice to have.

For each finding include:

- One distinct issue per finding; keep the body to a short paragraph.
- Why it is a problem and the situation that triggers it; state when
  severity depends on assumptions.
- Concrete references: file and line for code, section/page for
  documents, element/flow for designs.
- A concrete suggestion only when you have one, kept minimal.

Keep tone matter-of-fact: no flattery, no accusation. If there are no
qualifying findings, say so explicitly.

## After findings

- List open questions or assumptions separately, if any.
- Mention residual risks or gaps (missing checks, unverified claims,
  untested edge cases) when they matter.
- Keep any positive summary brief and secondary.
- Do not start fixing or rewriting unless the user asks.
