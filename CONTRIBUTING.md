# Contributing to Matrix-Governor

Thank you for your interest in contributing to **Matrix-Governor**! We welcome pull requests, feature suggestions, bug reports, and documentation improvements.

## 1. Development Workflow

1. **Fork and Clone**: Fork the repository and clone it to your local environment.
2. **Branching**: Create a feature or bugfix branch (`git checkout -b feat/your-feature-name`).
3. **Coding Standards**:
   - Follow standard Go conventions (`gofmt`, `golangci-lint`).
   - Maintain comprehensive unit tests (`go test -v ./pkg/...`).
   - Ensure all public functions and interfaces are thoroughly documented.
4. **Testing & Verification**:
   - Run local unit tests: `go test ./...`
   - Test KinD integration: verify with local 3-node KinD cluster.
5. **Submitting Pull Requests**: Submit a PR to `main` with a clear explanation of what problem your PR solves and how it was tested.

## 2. Code of Conduct

Please adhere to our [Code of Conduct](CODE_OF_CONDUCT.md) in all interactions.
