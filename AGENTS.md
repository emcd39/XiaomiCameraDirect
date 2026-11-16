# Project agent roles

## Model and delegation policy

The user authorizes the following role-based delegation for future development
in this repository. Optimize total cost, including context transfer, retries,
and reviews. These are routing instructions; they do not change the model of
an already running chat.

| Role | Preferred model | Reasoning | Responsibility |
| --- | --- | --- | --- |
| Lead developer | `gpt-6.1-sol` | `medium` | Plan and implement ordinary Go/Python changes, integrate results, diagnose failures, and verify completion. |
| Test runner | `gpt-6-luna` | `low` | Execute existing test suites and report evidence; do not edit source or tests. |
| Small scoped implementation | `gpt-6-luna` | `medium` | Simple isolated fixes and ordinary test additions with clear acceptance criteria. |
| Complex debugging | `gpt-6.1-sol` | `high` | Streaming, concurrency, Xiaomi protocol behavior, and failures spanning components. |
| Escalation specialist | `gpt-6-astra` | `high` | A bounded difficult problem after an unsuccessful substantive analysis by the lead. |

Use the lead model as the default for new development when model selection is
available. Do not claim to switch an existing chat's model through this file.
If a preferred model is unavailable, disclose the fallback; do not silently
claim the requested model was used.

## Test runner contract

- Delegate long existing test suites to the test runner. For one short command,
  the lead may run it directly when delegation would add more overhead.
- Start model-specific subagents with a fresh context (`fork_turns="none"`
  where supported). Supply only the repository path, exact command, relevant
  project/skill instructions, prerequisites, and expected report format.
- The isolated integration suite is `./scripts/test-local.ps1`; read
  `tests/README.md` and applicable testing skills before running it. Choose
  verification appropriate to the changed code rather than running every
  suite for every edit.
- The runner may create normal test artifacts and disposable test resources.
  It must not modify source, tests, dependencies, or configuration to make a
  failing run pass, and must not interact with production devices unless that
  is explicitly within the user's task.
- Report the command, completion status, exit code, failed stage/test names,
  relevant error excerpts, and evidence/log paths. Never report success while
  a command is still running or when required stages were skipped.
- On failure, return evidence to the lead. Do not start an autonomous repair
  loop or repeatedly rerun unchanged failing commands.
- Keep logs in files and return a concise summary. Do not send full successful
  logs, credentials, or the entire chat history to another agent.
- Do not run suites concurrently when they share mutable test resources.

## Ownership and escalation

The lead owns the final implementation and verification. Give implementation
subagents non-overlapping scopes; preserve unrelated working-tree changes.
Creating regression tests or choosing test coverage is implementation work,
not the low-reasoning test-runner role.

Escalate reasoning/model only for a concrete unresolved problem. Give the
specialist the evidence, attempted approaches, and a specific question, then
return ordinary work to the lead. Avoid spawning agents for trivial tasks.

Preserve the existing Model Supervisor configuration. Supervisor review is
separate from test execution; include its overhead when considering cost and
never claim review occurred without a report.
