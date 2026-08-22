# Activity Log

## 2026-08-21
- **Task:** Separate Docker Compose files for dev and production.
- **Attempt/Action:** 
  - Analyzed `infra/docker/compose.yml` and `analytics-service/Dockerfile`.
  - Created `compose.yml` for production with no host port mappings (services communicate via `gelato_network`).
  - Created `compose.dev.yml` to override and expose ports for local dev/integration testing (`5672`, `15672`, `27017`, `8080`).
  - User resolved the port mismatch issue by setting `PORT=8080` in their `.env` files.
- **Outcome:** Successfully created isolated Compose environments for dev and production workflows.

## Attempt: Implement CI/CD Workflows for Monorepo
- **Attempted**: Created new branch feature/cicd-workflows, added github action workflows (template and analytics-service).
- **Hypothesis**: By creating paths-filtered workflows for each service folder, we can isolate builds and testing while enforcing common security rules.
- **Outcome**: Created .github/workflows/workflow.example.yaml and .github/workflows/analytics-service.yaml successfully.

## Attempt: Create Mock Files for Analytics Service
- **Attempted**: Created mock files (main.go, dummy_test.go, Dockerfile) and initialized Go module in the analytics-service directory.
- **Hypothesis**: These minimal files will satisfy the CI/CD workflow steps (go test, docker build) without needing full implementation logic yet.
- **Outcome**: Successfully generated files and generated go.mod/go.sum.

## Debug Session: Analytics Service Docker Build Failure
- **What was checked**: Docker build logs for analytics-service.
- **How it was checked**: Analyzed the build failure at go mod download.
- **Observed outcome**: Identified go.mod requires go >= 1.24.2 (running go 1.21.13).
- **Root Cause**: The local go mod init used Go 1.24.2, but the Dockerfile and CI were hardcoded to Go 1.21.
- **Resolution**: Upgraded Dockerfile and CI workflow to use Go 1.24 to match the module requirements.

### 2026-08-21 16:58:31
- **Attempted:** Create Qodo PR Agent CI/CD for GitHub Actions using Qwen model via Groq API.
- **Hypothesis:** By creating .github/workflows/pr-agent.yml using LiteLLM configuration for Groq, it should automatically route the PR-Agent reviews to the chosen Qwen model.
- **Outcome:** File successfully created, committed, and pushed to eature/cicd-workflows.

### 2026-08-21 17:00:25
- **Attempted:** Update PR Agent fallback model to openai/gpt-oss-120b.
- **Hypothesis:** Updating the YAML config CONFIG.FALLBACK_MODELS ensures it falls back to this model when Groq hits rate limits.
- **Outcome:** File successfully updated, committed, and pushed.

### 2026-08-21 17:13:02
- **Attempted:** Fix PR-Agent not providing suggestions and skipping synchronize event.
- **Hypothesis:** By default, PR-Agent requires explicit flags to auto-review and handle new commits. Adding github_action_config.auto_review and handle_push_trigger to the env block resolves this.
- **Outcome:** File successfully updated, committed, and pushed.

### 2026-08-21 17:17:35
- **Attempted:** Fix PR-Agent crash when using unknown model names.
- **Hypothesis:** PR-Agent doesn't know the context window size of \groq/qwen/qwen3.6-27b\. Setting \CONFIG.CUSTOM_MODEL_MAX_TOKENS: "32000" will bypass this check and allow the agent to run.
- **Outcome:** File successfully updated, committed, and pushed.

### 2026-08-21 17:28:00
- **Attempted:** Fix Invalid API Key error for Groq.
- **Hypothesis:** LiteLLM expects the environment variable \GROQ_API_KEY\ for models prefixed with \groq/\, but the workflow only exported \OPENAI_KEY\. Exporting \GROQ_API_KEY\ will allow LiteLLM to authenticate with Groq correctly.
- **Outcome:** File successfully updated, committed, and pushed.

### 2026-08-21 17:30:43
- **Attempted:** Fix PR-Agent parsing failure due to \<think>\ tags.
- **Hypothesis:** The model outputs a reasoning block (\<think>...\) before the YAML payload, which causes the YAML parser to return a string instead of a dictionary. Setting \CONFIG.CUSTOM_REASONING_MODEL: "true" should instruct PR-Agent to handle these reasoning models properly.
- **Outcome:** File successfully updated, committed, and pushed.

### 2026-08-21 17:36:25
- **Attempted:** Switch PR-Agent to non-reasoning Groq models.
- **Hypothesis:** By switching the base model to \groq/llama-3.3-70b-versatile\ and fallback to \groq/qwen-2.5-32b\, we prevent the Token Limit issue caused by the reasoning model's \<think>\ block.
- **Outcome:** File successfully updated, committed, and pushed.

### 2026-08-21 17:39:47
- **Attempted:** Fix Model Not Found / Decommissioned errors on Groq.
- **Hypothesis:** Groq has decommissioned \qwen-2.5-32b\ and \llama-3.3-70b-versatile\ may not be accessible to this API tier or is named differently. Switching to the standard, stable \llama-3.1-70b-versatile\ and \llama-3.1-8b-instant\ should resolve the API resolution errors.
- **Outcome:** File successfully updated, committed, and pushed.

### 2026-08-21 17:45:25
- **Attempted:** Fix decommissioned Llama 3.1 models on Groq.
- **Hypothesis:** Groq has deprecated Llama 3.1 and 3.3 models for this tier in favor of the active openai/gpt-oss series. By switching to \openai/gpt-oss-120b\ and \openai/gpt-oss-20b\ and re-enabling \CONFIG.CUSTOM_REASONING_MODEL\, it should bypass the model_not_found errors and correctly parse reasoning tokens.
- **Outcome:** File successfully updated, committed, and pushed.

### 2026-08-21 17:49:57
- **Attempted:** Switch PR-Agent from Groq to Gemini API.
- **Hypothesis:** By setting \GEMINI_API_KEY\ and updating \CONFIG.MODEL\ to \gemini/gemini-2.5-flash\ and fallback to \gemini/gemma-4-31b-it\, LiteLLM will route the requests to Google AI Studio instead of Groq. Also disabled reasoning model support since these are standard models.
- **Outcome:** File successfully updated, committed, and pushed.

### 2026-08-21 17:56:23
- **Attempted:** Fix Gemini 2.5 Flash unavailability and Gemma timeout.
- **Hypothesis:** Google AI Studio has disabled \gemini-2.5-flash\ for new users, demanding an upgrade to \gemini-3.6-flash\. Also, the \gemma-4-31b-it\ fallback timed out because PR-Agent's default timeout is 120s. Bumping it to 300s resolves this.
- **Outcome:** File successfully updated, committed, and pushed.

### 2026-08-21 17:58:21
- **Attempted:** Update Gemini models per user request.
- **Hypothesis:** Switching the base model to \gemini-3.7-flash\ and fallback to \gemini-3.6-flash\ will provide a faster and more reliable fallback than Gemma 4.
- **Outcome:** File successfully updated, committed, and pushed.

### 2026-08-22 10:33:00
- **Attempted:** Address all PR #2 review comments (CodeRabbit, CI failures).
- **Changes Applied:**
  1. Fix `gofmt -s` formatting in `tests/service_test.go` (CI failure root cause).
  2. Gate hardcoded credentials behind `GO_ENV` in `config.go` — local dev keeps fallbacks, production crashes loudly on missing secrets.
  3. Fix README PORT from 8080 to 3000 + replace hardcoded credentials with `<username>:<password>` placeholders.
  4. Add `sync.RWMutex` + buffered `Saved` channel to `MockRepository` for race-safe concurrent access.
  5. Replace `time.Sleep(1s)` in integration test with channel-based signaling (zero CPU waste, race-free).
  6. Replace hardcoded password in `generate-password.py` with `sys.argv` + `getpass` fallback.
  7. Exclude `_id` from `$set` in `analytics_repo.go` Save method (3-line fix instead of full `$inc` rewrite).
  8. Add `ErrInvalidPeriod` sentinel error — reject unknown period values with 400 instead of silent fallback.
  9. Add date validation in `getOrCreateAnalytics` + fix consumer to Nack poison messages without requeue.
  10. Recalculate `WasteRate` in `ProcessOrderSuccess` after `ScoopsSold` changes.
  11. Add error logging in analytics handler before 500 responses.
  12. Pin MongoDB image from `mongo:latest` to `mongo:7.0` in `compose.yml`.
- **Outcome:** All tests pass. `gofmt -s -l .` returns clean. Build succeeds.
