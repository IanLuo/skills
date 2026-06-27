# Dev Task TDD/BDD Guide

Read this when a coding task changes behavior, fixes a regression, or affects a
user-facing workflow.

## Preferred Loop

1. Characterize current behavior with a focused test or command.
2. Write the smallest failing test when the expected behavior is clear.
3. Implement the narrowest change that makes the test pass.
4. Run the focused check again.
5. Run broader verification when shared code, integrations, or public behavior changed.

## BDD Shape

For user-visible behavior, express the target as:

- Given the relevant state,
- When the user or system does the action,
- Then the observable result changes.

Use that shape in test names, scenario notes, or PR summaries when it clarifies intent.

## Exceptions

Skip writing a new failing test only when:

- the task is a mechanical rename or documentation-only change,
- the repo has no viable test harness and adding one is out of scope,
- an existing failing test already captures the issue,
- the user explicitly asks for exploratory or prototype work.

When skipping, record why and use the best available verification command.
