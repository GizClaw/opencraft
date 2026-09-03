---
name: plan
description: Build or update a task plan when a request has multiple sequential steps; use when the user asks to plan or when a task is large enough that order and verification matter.
---

# Plan

When activated, turn the request into a concrete checklist before executing:

1. Break the request into steps small enough to verify one at a time; each step needs a clear outcome.
2. Submit the checklist with `update_plan`, keeping at most one step in_progress.
3. Update statuses as work progresses; a finished plan must not be re-submitted as pending.

Keep the plan visible to the user: it is the shared TODO list for the task.
