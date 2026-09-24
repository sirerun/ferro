# Provider usage mapping for bulk v2

Coordinator source check: 2026-09-24. This record supplies parsing requirements, not a claim of live provider qualification or pricing guarantees.

Official reference: https://openrouter.ai/docs/cookbook/administration/usage-accounting

Use non-streaming chat completions for the initial metadata path. Map usage.prompt_tokens, completion_tokens and total_tokens to nullable reported counters; map completion_tokens_details.reasoning_tokens and prompt_tokens_details.cached_tokens/cache_write_tokens separately. Missing values remain unknown, including a missing cost; explicit zero remains zero. usage.cost denotes the account charge, while cost_details.upstream_inference_cost is a different measure and must not substitute for it. Do not fetch a second endpoint implicitly.

Convert the provider decimal cost to integer micro-USD without binary floating-point arithmetic, only under an explicit compatible-provider currency mapping. Round a positive fractional micro-unit upward conservatively and record that representation policy; reject negative, malformed and overflowing amounts. Do not infer currency or account cost from arbitrary compatible-provider fields. Generic compatible providers can return tokens with unknown billed cost. No live price or hard-dollar guarantee is established.

The new metadata call performs one HTTP attempt and returns transmission/usage metadata even when decoding or content validation fails. Enforce response and error-body size caps before parsing. Legacy schema-format fallback remains separate; G02 must use the metadata-only adapter through the admission controller so fallback cannot bypass request accounting.
