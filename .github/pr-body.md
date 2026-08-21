## Overview / Context
This PR introduces the foundational CI/CD workflows for the monorepo architecture using GitHub Actions. It aims to establish isolated build, test, and security scanning pipelines for individual microservices, ensuring that changes in one service exclusively trigger its respective workflow. To validate the pipeline, mock files for the nalytics-service have also been introduced.

## Proposed Changes
- **.github/workflows/workflow.example.yaml**: Created a reference CI/CD template outlining the standard jobs required for future microservices, including secret scanning (TruffleHog), code quality, testing, and Docker vulnerability scanning (Trivy).
- **.github/workflows/analytics-service.yaml**: Implemented the concrete CI/CD pipeline tailored for the Go-based nalytics-service. It leverages paths triggers to run only when the nalytics-service/** files change.
- **nalytics-service/***: Added a minimal Go Fiber application (main.go), initialized Go modules (go.mod, go.sum), a multi-stage Dockerfile, and a passing dummy test (	ests/dummy_test.go). These mock files act as the testing payload to verify the CI/CD pipeline logic.
- **.gemini/activity_log.md**: Appended development activity logs tracking the creation of workflows and mock files.

## How it Works / Architecture
- **Isolation via Paths**: GitHub Actions triggers (on.push.paths and on.pull_request.paths) are utilized to isolate workflow executions per microservice directory, preventing unnecessary builds and conserving CI minutes.
- **Security & Linting**: TruffleHog scans the repository history for leaked secrets. For the Go service, standard tools like ctions/setup-go and gofmt validate code formatting.
- **Container Scanning**: The CI builds the Docker image and immediately scans it for OS and library vulnerabilities (CRITICAL, HIGH severities) using Trivy, failing the pipeline if any are found.

## Verification
- Verified by committing mock changes strictly inside the nalytics-service folder.
- The pipeline correctly sets up the Go environment, verifies dependencies, passes the dummy unit test, builds the Docker image, and executes the Trivy security scan.
