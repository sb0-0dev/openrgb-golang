# Contributing

Contributions are welcome, including bug fixes, improvements, documentation, and new features.

## Getting Started

Make sure you have Go installed.

Clone the repository:

```
git https://github.com/sb0-0dev/openrgb-go.git
cd openrgb-go
```

Download dependencies:
```
go mod download
```

Run the tests:
```
go test ./...
```
## Making Changes

Create a branch for your changes:
```
git checkout -b fix/my-change
```
Keep changes focused and follow the existing Go style and conventions.

Format your code before submitting:

```
gofmt -w .
```

You can also check your code with:
```
go vet ./...
```

If you add or change functionality, please add or update tests.

## Testing

All tests should pass before opening a pull request:
```
go test ./...
```

For more detailed test output:
```
go test -v ./...
```
## Pull Requests

When opening a pull request, please:

- Explain what you changed and why.
- Include tests for new or changed behavior.
- Make sure go test ./... passes.
- Run gofmt and go vet.
- Keep the pull request focused on one change where possible.
- Ensure your contribution meets the project's coding and quality standards.
- Pull requests that meet these standards may be prioritised for review and consideration.

## AI-Assisted Contributions

AI coding tools are permitted, but contributors remain responsible for all code they submit.

If you make significant use of AI to produce, modify, or review a contribution, please declare this in your pull request.Significant AI use includes, but is not limited to, generating substantial portions of code, designing an implementation, or substantially rewriting existing code.

Contributors should review AI-generated code carefully for correctness, security, maintainability, licensing concerns, and consistency with this project.

AI use does not replace the contributor's responsibility for understanding and maintaining their contribution.

## Commit Messages

Use clear, descriptive commit messages.

Examples:

Added support for custom timeouts
Updated package documentation

## Reporting Bugs

Please open an issue if you find a bug.

Include:

- A description of the problem.
- Steps to reproduce it.
- The expected behavior.
- The actual behavior.
- Your Go version.
- Any relevant code or error messages.

## Documentation

If your change affects the public API, please update the relevant Go documentation and examples.

Public functions, types, and packages should have clear GoDoc comments.

## Questions

If you're unsure about something, feel free to open an issue or discussion.

Thanks for contributing!