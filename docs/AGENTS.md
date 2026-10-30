# Project instructions

## Project overview

Exchange Rate Service is a backend application written in Go.

The application manages currency exchange rates according to
the requirements defined in the technical assignment.

## General principles

- Read the files in "docs" folder before doing anything.
- Follow idiomatic Go conventions.
- Prefer simple, readable, maintainable code.
- Follow YAGNI, KISS and DRY principles.
- Do not add dependencies without a clear justification.
- Do not implement functionality outside the requirements.
- Once you have received an assignment, create a plan for its execution, submit that plan to me, and proceed with carrying it out only after I have given my approval.

## Architecture

- Separate business logic from infrastructure.
- Use dependency injection where appropriate.
- Define interfaces where they provide practical value.
- Keep business logic independently testable.
- Avoid circular dependencies.

## Code quality

- Use descriptive names.
- Handle errors explicitly.
- Wrap errors with context where appropriate.
- Avoid global mutable state.
- Use context.Context for operations that require cancellation.

## Testing

- Write unit tests for business logic.
- Use table-driven tests where appropriate.
- Mock external dependencies when necessary.
- Add integration tests for database operations.
- Run existing tests after making changes.

## Agent behavior

- Before implementing a task, inspect the relevant existing code.
- Prefer minimal changes that solve the requested problem.
- Do not refactor unrelated code.
- Do not modify public interfaces without justification.
- Explain important architectural decisions.
- Report tests executed and their results.
- If requirements are ambiguous, ask for clarification rather
  than inventing business rules.
