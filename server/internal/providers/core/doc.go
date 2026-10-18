// Package core is the provider core: the Go port of lib/providers/core in the
// Next app. It holds the role and instance types, provider definitions and the
// registry, config validation (with a JSON Schema description of each
// provider's settings), the config sources, the runtime that turns instances
// into clients, and the shared HTTP client.
//
// The rule that shapes everything here: secrets never leave the server. Config
// validation errors never contain the offending value, every error from the HTTP
// client is redacted before it is built, upstream response bodies are never
// echoed or logged, and nothing here marshals an Instance's config.
package core
