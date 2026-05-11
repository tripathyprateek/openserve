# openserve Model Catalog

The openserve catalog is a curated collection of large language models (LLMs) verified for quality, licensing compliance, and production readiness. All models in the catalog have been reviewed and cryptographically signed by the openserve maintainers.

## Catalog Structure

- **`schema.json`** - JSON Schema (draft-07) defining the structure and validation rules for all model manifests
- **`models/`** - Directory containing individual model manifest YAML files
- **`catalog.openserve.io`** - Public-facing catalog served via HTTPS

## Model Manifest Format

Each model is defined in a YAML file that adheres to `schema.json`. Required fields include:

| Field | Type | Description |
|-------|------|-------------|
| `id` | string | Unique identifier (lowercase alphanumeric, hyphens, dots) |
| `name` | string | Human-readable model name |
| `family` | enum | Model family: llama, mistral, qwen, gemma, phi, deepseek, falcon, other |
| `version` | string | Model version identifier |
| `hfRepo` | string | HuggingFace repository (org/repo format) |
| `hfRevision` | string | Git SHA or tag identifying the exact revision |
| `weightDigestSha256` | string | SHA256 hash of the resolved model weights |
| `license` | enum | apache-2.0, mit, llama3, llama2, mistral, gemma, other |
| `minGPUClass` | enum | Minimum GPU: l4, a100-40g, a100-80g |

Optional fields include `cosignManifest`, `parameterCount`, `maxContextLen`, `recommendedVLLMArgs`, `description`, `tags`, `addedAt`, and `addedBy`.

## Catalog Sign-Off Process

The openserve catalog follows a five-step verification and signing process to ensure trust and compliance:

### 1. Model Request

A community member or maintainer opens a **model request issue** using the provided template (`.github/ISSUE_TEMPLATE/request-a-model.yml`). The request includes:

- Model name and HuggingFace repository
- License information
- Parameter count and minimum GPU requirements
- Use case and justification
- Confirmation that the license permits commercial use

**Labels:** `model-request`

### 2. Automated Verification

Upon issue creation or maintainer trigger, an automated CI job performs preliminary verification:

1. **Clone and verify** the HuggingFace repository
2. **Download model weights** to a secure, isolated environment
3. **Compute SHA256** digest of the downloaded weights archive
4. **Verify license** against the declared license in the request
5. **Extract metadata** (parameter count, context length, etc.) from model configuration
6. **Report findings** back to the issue with computed SHA256 and any flagged issues

This job logs to a secure artifact store and never uploads weights.

### 3. Maintainer Review

A designated openserve maintainer reviews:

- Automated verification results
- License compliance (no commercial restrictions, proper attribution)
- Model quality and appropriateness for the openserve catalog
- Alignment with openserve roadmap and community needs

The maintainer may request additional information, clarification on licensing, or recommend deferral if concerns exist.

**Approval:** Maintainer comments with `:+1:` and approval on the issue.

### 4. Create and Sign Manifest

Upon approval, the maintainer:

1. **Creates a model manifest YAML** file in `catalog/models/` adhering to `schema.json`
2. **Fills in verified values**:
   - `weightDigestSha256` from automated job
   - `hfRevision` and `hfRepo` from HuggingFace
   - `minGPUClass`, `parameterCount`, `maxContextLen` from verification
   - `addedAt` (today), `addedBy` (GitHub username)
3. **Signs the manifest** using `cosign`:
   ```bash
   cosign sign-blob --key cosign.key catalog/models/model-id.yaml > catalog/models/model-id.yaml.sig
   ```
4. **Records the cosign URL** in `cosignManifest` field (points to signed bundle in release artifacts)
5. **Opens a pull request** adding the manifest to the repository

### 5. CI Validation and Merge

Upon PR creation:

1. **CI validates** the manifest against `schema.json`
2. **CI verifies** the cosign signature using the openserve public key
3. **CI checks** for duplicate model IDs and naming conflicts
4. **Maintainers review** the PR; any maintainer may merge

After merge:

1. **Automated job** packages updated catalog as JSON
2. **Publishes** to `catalog.openserve.io` with HTTPS and caching headers
3. **Publishes** catalog.json and manifest checksums for client verification
4. **Logs** catalog event in audit trail

## Validation and Verification

### Schema Validation

All manifests are automatically validated against `schema.json` on:
- Pull request creation (CI job: `validate-catalog`)
- Tag push (pre-release validation)
- Daily (scheduled compliance check)

### Signature Verification

Clients can verify manifest authenticity:

```bash
# Download manifest and signature
curl -o model.yaml https://catalog.openserve.io/models/llama-3-8b-instruct.yaml
curl -o model.yaml.sig https://catalog.openserve.io/models/llama-3-8b-instruct.yaml.sig

# Verify signature using openserve public key
cosign verify-blob --key cosign.pub \
  --signature model.yaml.sig \
  model.yaml
```

### Weight Verification

Clients can verify downloaded weights:

```bash
# Download weights and compute SHA256
sha256sum weights.tar.gz

# Compare against catalog manifest
cat model.yaml | grep weightDigestSha256
# Expected: SHA256 from weights.tar.gz
```

## Adding a Model to the Catalog

### Quick Start

1. **Open an issue** using the model request template
2. **Wait for automated verification** (usually completes within 1 hour)
3. **Request maintainer review** (tag `@openserve/maintainers`)
4. **Upon approval**, a maintainer creates the manifest and PR
5. **Merge triggers** automatic publication to `catalog.openserve.io`

### Direct Contribution (Advanced)

Experienced contributors may directly open a PR with a model manifest:

1. Create manifest file in `catalog/models/{id}.yaml`
2. Ensure all required fields are populated
3. Reference the SHA256 from HuggingFace or your own verification
4. CI will validate and notify you of any schema violations
5. A maintainer will review licensing and quality

## Troubleshooting

| Issue | Resolution |
|-------|-----------|
| "License not approved" | License must permit commercial use. Update the request and reopen. |
| "SHA256 mismatch" | Ensure you downloaded the exact revision. Check `hfRevision` in manifest. |
| "Schema validation failed" | Run `jsonschema catalog/models/model.yaml catalog/schema.json` locally to debug. |
| "Signature verification failed" | Ensure you're using the current openserve public key from `cosign.pub`. |

## References

- **Schema:** `catalog/schema.json`
- **Models:** `catalog/models/*.yaml`
- **Issue Template:** `.github/ISSUE_TEMPLATE/request-a-model.yml`
- **CI Jobs:** `.github/workflows/build.yml` (validate-catalog job)
- **Public Catalog:** https://catalog.openserve.io
- **Cosign Documentation:** https://docs.sigstore.dev/cosign/
